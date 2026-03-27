package main

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Pool manages credentials with round-robin selection and cooldown.
type Pool struct {
	mu         sync.Mutex
	creds      []Credential
	cursor     int
	refreshMu  sync.Mutex // separate lock for refresh operations
	refreshing map[string]bool
}

func NewPool(creds []Credential) *Pool {
	return &Pool{
		creds:      creds,
		refreshing: make(map[string]bool),
	}
}

// Pick selects the next available credential for the given provider.
// Skips credentials that are cooled down or expired (and can't refresh).
func (p *Pool) Pick(provider string) (*Credential, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.creds)
	if n == 0 {
		return nil, fmt.Errorf("no credentials available")
	}

	now := time.Now()
	start := p.cursor
	var needsRefresh *Credential

	for i := 0; i < n; i++ {
		idx := (start + i) % n
		c := &p.creds[idx]

		if c.Provider != provider {
			continue
		}
		if now.Before(c.CooldownUntil) {
			slog.Debug("credential on cooldown", "label", c.Label, "until", c.CooldownUntil)
			continue
		}

		if c.IsExpired() {
			// Remember first expired cred, try to find a non-expired one first
			if needsRefresh == nil {
				needsRefresh = c
			}
			continue
		}

		p.cursor = (idx + 1) % n
		return c, nil
	}

	// All matching creds expired — try refresh outside lock
	if needsRefresh != nil {
		p.mu.Unlock()
		err := p.refresh(needsRefresh)
		p.mu.Lock()
		if err == nil && !needsRefresh.IsExpired() {
			return needsRefresh, nil
		}
		slog.Warn("credential refresh failed", "label", needsRefresh.Label, "error", err)
	}

	return nil, fmt.Errorf("no available credential for provider %q", provider)
}

// PickNext selects the next credential after a failed attempt (for retry).
func (p *Pool) PickNext(provider string, exclude *Credential) (*Credential, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.creds)
	now := time.Now()

	for i := 0; i < n; i++ {
		idx := (p.cursor + i) % n
		c := &p.creds[idx]

		if c.Provider != provider || c == exclude {
			continue
		}
		if now.Before(c.CooldownUntil) || c.IsExpired() {
			continue
		}

		p.cursor = (idx + 1) % n
		return c, nil
	}

	return nil, fmt.Errorf("no fallback credential for provider %q", provider)
}

// Cooldown marks a credential as temporarily unavailable.
func (p *Pool) Cooldown(c *Credential, d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c.CooldownUntil = time.Now().Add(d)
	slog.Info("credential cooldown", "label", c.Label, "duration", d)
}

// Count returns total credentials (optionally filtered by provider).
func (p *Pool) Count(provider string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if provider == "" {
		return len(p.creds)
	}
	n := 0
	for _, c := range p.creds {
		if c.Provider == provider {
			n++
		}
	}
	return n
}

// Providers returns unique provider names.
func (p *Pool) Providers() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := map[string]bool{}
	var result []string
	for _, c := range p.creds {
		if !seen[c.Provider] {
			seen[c.Provider] = true
			result = append(result, c.Provider)
		}
	}
	return result
}

// refresh runs outside the main pool lock to avoid blocking all requests.
func (p *Pool) refresh(c *Credential) error {
	// Prevent concurrent refreshes for the same credential
	p.refreshMu.Lock()
	if p.refreshing[c.Label] {
		p.refreshMu.Unlock()
		// Another goroutine is refreshing — wait briefly then check
		time.Sleep(2 * time.Second)
		if !c.IsExpired() {
			return nil // other refresh succeeded
		}
		return fmt.Errorf("concurrent refresh in progress for %s", c.Label)
	}
	p.refreshing[c.Label] = true
	p.refreshMu.Unlock()

	defer func() {
		p.refreshMu.Lock()
		delete(p.refreshing, c.Label)
		p.refreshMu.Unlock()
	}()

	switch {
	case c.Provider == "openai" && c.AuthType == AuthOAuth && c.RefreshToken != "":
		at, rt, exp, err := RefreshOAuth(openAIOAuthEndpoint, openAIClientID, c.RefreshToken)
		if err != nil {
			return err
		}
		p.mu.Lock()
		c.AccessToken = at
		c.RefreshToken = rt
		c.ExpiresAt = exp
		p.mu.Unlock()
		slog.Info("refreshed OpenAI OAuth token", "label", c.Label, "expires", exp)
		return nil

	case c.Provider == "anthropic" && c.AuthType == AuthOAuth:
		if err := RefreshViaCLI("claude -p . --model haiku --text hi"); err != nil {
			return fmt.Errorf("claude CLI refresh: %w", err)
		}
		creds, err := LoadFromKeychain()
		if err != nil || len(creds) == 0 {
			return fmt.Errorf("re-read keychain after refresh: %w", err)
		}
		p.mu.Lock()
		c.AccessToken = creds[0].AccessToken
		c.RefreshToken = creds[0].RefreshToken
		c.ExpiresAt = creds[0].ExpiresAt
		p.mu.Unlock()
		slog.Info("refreshed Claude OAuth token", "label", c.Label, "expires", c.ExpiresAt)
		return nil

	default:
		return fmt.Errorf("cannot refresh credential type %s/%s", c.Provider, c.AuthType)
	}
}
