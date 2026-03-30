package providers

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	authpkg "github.com/nghiahoang/launchdock/internal/auth"
)

// Pool manages credentials with round-robin selection and cooldown.
type Pool struct {
	mu           sync.Mutex
	creds        []authpkg.Credential
	cursor       int
	refreshMu    sync.Mutex
	refreshLocks map[string]*sync.Mutex
}

func NewPool(creds []authpkg.Credential) *Pool {
	return &Pool{creds: creds, refreshLocks: make(map[string]*sync.Mutex)}
}

// Pick selects the next available credential for the given provider.
// Skips credentials that are cooled down or expired (and can't refresh).
func (p *Pool) Pick(provider string) (*authpkg.Credential, error) {
	return p.PickMatching(provider, nil, func(*authpkg.Credential) bool { return true })
}

func (p *Pool) PickMatching(provider string, exclude *authpkg.Credential, match func(*authpkg.Credential) bool) (*authpkg.Credential, error) {
	n := len(p.creds)
	if n == 0 {
		return nil, fmt.Errorf("no credentials available")
	}
	for _, idx := range p.pickCandidateIndices(provider, exclude) {
		c := &p.creds[idx]
		if match != nil && !match(c) {
			continue
		}
		if p.needsRefresh(c) {
			if err := p.refresh(c); err != nil {
				slog.Warn("credential refresh failed", "label", c.Label, "error", err)
				continue
			}
		}
		p.mu.Lock()
		p.cursor = (idx + 1) % n
		p.mu.Unlock()
		return c, nil
	}
	return nil, fmt.Errorf("no available credential for provider %q", provider)
}

// PickNext selects the next credential after a failed attempt (for retry).
func (p *Pool) PickNext(provider string, exclude *authpkg.Credential) (*authpkg.Credential, error) {
	return p.PickNextMatching(provider, exclude, func(*authpkg.Credential) bool { return true })
}

func (p *Pool) PickNextMatching(provider string, exclude *authpkg.Credential, match func(*authpkg.Credential) bool) (*authpkg.Credential, error) {
	for _, idx := range p.pickCandidateIndices(provider, exclude) {
		c := &p.creds[idx]
		if match != nil && !match(c) {
			continue
		}
		if p.needsRefresh(c) {
			if err := p.refresh(c); err != nil {
				slog.Warn("fallback credential refresh failed", "label", c.Label, "error", err)
				continue
			}
		}
		p.mu.Lock()
		p.cursor = (idx + 1) % len(p.creds)
		p.mu.Unlock()
		return c, nil
	}
	return nil, fmt.Errorf("no fallback credential for provider %q", provider)
}

// Cooldown marks a credential as temporarily unavailable.
func (p *Pool) Cooldown(c *authpkg.Credential, d time.Duration) {
	if !p.hasAlternativeCredential(c.Provider, c) {
		slog.Info("skipping credential cooldown; no alternative available", "label", c.Label, "provider", c.Provider)
		return
	}
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

func (p *Pool) pickCandidateIndices(provider string, exclude *authpkg.Credential) []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.creds)
	now := time.Now()
	start := p.cursor
	var result []int
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		c := &p.creds[idx]
		if c.Provider != provider || c == exclude {
			continue
		}
		if now.Before(c.CooldownUntil) {
			slog.Debug("credential on cooldown", "label", c.Label, "until", c.CooldownUntil)
			continue
		}
		result = append(result, idx)
	}
	return result
}

func (p *Pool) needsRefresh(c *authpkg.Credential) bool {
	if c.AuthType != authpkg.AuthOAuth || c.RefreshToken == "" || c.ExpiresAt.IsZero() {
		return false
	}
	return time.Until(c.ExpiresAt) <= 5*time.Minute
}

func (p *Pool) hasAlternativeCredential(provider string, current *authpkg.Credential) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.creds {
		c := &p.creds[i]
		if c.Provider == provider && c != current {
			return true
		}
	}
	return false
}

func (p *Pool) lockForCredential(c *authpkg.Credential) *sync.Mutex {
	key := c.Provider + ":" + c.Source + ":" + c.Label
	if c.ID != "" {
		key = c.ID
	}
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	if lock, ok := p.refreshLocks[key]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	p.refreshLocks[key] = lock
	return lock
}

// refresh runs outside the main pool lock to avoid blocking all requests.
func (p *Pool) refresh(c *authpkg.Credential) error {
	lock := p.lockForCredential(c)
	lock.Lock()
	defer lock.Unlock()

	p.mu.Lock()
	if time.Now().Before(c.CooldownUntil) {
		until := c.CooldownUntil
		p.mu.Unlock()
		return fmt.Errorf("credential on cooldown until %s", until.Format(time.RFC3339))
	}
	if !p.needsRefresh(c) {
		p.mu.Unlock()
		return nil
	}
	provider := c.Provider
	authType := c.AuthType
	refreshToken := c.RefreshToken
	label := c.Label
	managedID := c.ID
	managed := c.Managed
	p.mu.Unlock()

	switch {
	case provider == "openai" && authType == authpkg.AuthOAuth && refreshToken != "":
		at, rt, exp, err := authpkg.RefreshOpenAIOAuth(refreshToken)
		if err != nil {
			if managed && managedID != "" && authpkg.IsTerminalOpenAIRefreshError(err) {
				if derr := authpkg.SetConfigCredentialDisabled(managedID, true); derr != nil {
					slog.Warn("disable stale OpenAI credential failed", "label", label, "id", managedID, "error", derr)
				} else {
					slog.Warn("disabled stale OpenAI credential", "label", label, "id", managedID)
				}
			}
			p.Cooldown(c, 45*time.Second)
			return err
		}
		p.mu.Lock()
		c.AccessToken = at
		c.RefreshToken = rt
		c.ExpiresAt = exp
		p.mu.Unlock()
		if managed && managedID != "" {
			if err := authpkg.PersistManagedCredentialState(managedID, rt, c.AccountID, c.Email); err != nil {
				slog.Warn("persist managed OpenAI token failed", "label", label, "error", err)
			}
		}
		slog.Info("refreshed OpenAI OAuth token", "label", label, "expires", exp)
		return nil

	case provider == "anthropic" && authType == authpkg.AuthOAuth && refreshToken != "":
		at, rt, exp, err := authpkg.RefreshClaudeOAuth(refreshToken)
		if err != nil {
			p.Cooldown(c, 45*time.Second)
			// Fallback: try CLI refresh
			slog.Warn("direct OAuth refresh failed, trying CLI fallback", "error", err)
			if cliErr := authpkg.RefreshViaCLI("claude -p . --model haiku --text hi"); cliErr != nil {
				return fmt.Errorf("claude refresh failed (direct: %w, cli: %v)", err, cliErr)
			}
			creds, kerr := authpkg.LoadFromKeychain()
			if kerr != nil || len(creds) == 0 {
				return fmt.Errorf("re-read keychain after CLI refresh: %w", kerr)
			}
			p.mu.Lock()
			c.AccessToken = creds[0].AccessToken
			c.RefreshToken = creds[0].RefreshToken
			c.ExpiresAt = creds[0].ExpiresAt
			p.mu.Unlock()
			if managed && managedID != "" {
				if err := authpkg.PersistManagedCredentialState(managedID, creds[0].RefreshToken, c.AccountID, c.Email); err != nil {
					slog.Warn("persist managed Claude token failed", "label", label, "error", err)
				}
			}
			slog.Info("refreshed Claude OAuth token via CLI", "label", label)
			return nil
		}
		p.mu.Lock()
		c.AccessToken = at
		c.RefreshToken = rt
		c.ExpiresAt = exp
		p.mu.Unlock()
		if managed && managedID != "" {
			if err := authpkg.PersistManagedCredentialState(managedID, rt, c.AccountID, c.Email); err != nil {
				slog.Warn("persist managed Claude token failed", "label", label, "error", err)
			}
		}
		slog.Info("refreshed Claude OAuth token directly", "label", label, "expires", exp)
		return nil

	default:
		return fmt.Errorf("cannot refresh credential type %s/%s", c.Provider, c.AuthType)
	}
}

func (p *Pool) RefreshCredential(c *authpkg.Credential) error {
	return p.refresh(c)
}
