package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	authpkg "github.com/nghiahoang/launchdock/internal/auth"
	providerspkg "github.com/nghiahoang/launchdock/internal/providers"
)

func isAuthFailure(provider string, statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized && statusCode != http.StatusForbidden {
		return false
	}
	msg := strings.ToLower(authFailureMessage(provider, body))
	switch provider {
	case "anthropic":
		return strings.Contains(msg, "authentication") ||
			strings.Contains(msg, "api key") ||
			strings.Contains(msg, "oauth") ||
			strings.Contains(msg, "token") ||
			strings.Contains(msg, "unauthorized") ||
			strings.Contains(msg, "forbidden") ||
			strings.Contains(msg, "expired") ||
			strings.Contains(msg, "invalid")
	case "openai":
		return strings.Contains(msg, "auth") ||
			strings.Contains(msg, "token") ||
			strings.Contains(msg, "unauthorized") ||
			strings.Contains(msg, "forbidden") ||
			strings.Contains(msg, "expired") ||
			strings.Contains(msg, "invalid")
	default:
		return statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden
	}
}

func authFailureMessage(provider string, body []byte) string {
	switch provider {
	case "openai":
		var payload struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
				Type    string `json:"type"`
			} `json:"error"`
			Detail struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"detail"`
		}
		if json.Unmarshal(body, &payload) == nil {
			return strings.TrimSpace(payload.Error.Message + " " + payload.Error.Code + " " + payload.Error.Type + " " + payload.Detail.Code + " " + payload.Detail.Message)
		}
	case "anthropic":
		var payload struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &payload) == nil {
			return strings.TrimSpace(payload.Error.Message + " " + payload.Error.Type)
		}
	}
	return string(body)
}

func doWithCredentialRetry(pool *providerspkg.Pool, providerName string, cred *authpkg.Credential, attempt func(*authpkg.Credential) (*http.Response, error)) (*http.Response, *authpkg.Credential, error) {
	return doWithCredentialRetryMatching(pool, providerName, cred, func(*authpkg.Credential) bool { return true }, attempt)
}

type retryState struct {
	refreshedSame bool
	fallbackTried bool
}

func doWithCredentialRetryMatching(pool *providerspkg.Pool, providerName string, cred *authpkg.Credential, match func(*authpkg.Credential) bool, attempt func(*authpkg.Credential) (*http.Response, error)) (*http.Response, *authpkg.Credential, error) {
	state := retryState{}

	for {
		resp, err := attempt(cred)
		if err != nil {
			return nil, cred, err
		}
		if resp.StatusCode == http.StatusOK {
			return resp, cred, nil
		}

		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if isAuthFailure(providerName, resp.StatusCode, errBody) {
			if !state.refreshedSame && cred.AuthType == authpkg.AuthOAuth && cred.RefreshToken != "" {
				state.refreshedSame = true
				if err := pool.RefreshCredential(cred); err == nil {
					slog.Warn("auth failure recovered by refresh", "provider", providerName, "credential", cred.Label)
					continue
				}
			}
			nextCred, ok := pickFallbackCredential(pool, providerName, cred, match, &state, 45*time.Second, "auth failure")
			if ok {
				cred = nextCred
				continue
			}
			return rebuildErrorResponse(resp, errBody), cred, nil
		}

		if isRetryable(resp.StatusCode) {
			slog.Warn("retryable upstream error, trying next credential", "status", resp.StatusCode, "credential", cred.Label, "body", string(errBody))
			cooldown := 0 * time.Second
			switch resp.StatusCode {
			case 429:
				cooldown = 60 * time.Second
			case 529, 503:
				cooldown = 30 * time.Second
			}
			nextCred, ok := pickFallbackCredential(pool, providerName, cred, match, &state, cooldown, "retryable upstream error")
			if ok {
				cred = nextCred
				continue
			}
		}

		return rebuildErrorResponse(resp, errBody), cred, nil
	}
}

func pickFallbackCredential(pool *providerspkg.Pool, providerName string, cred *authpkg.Credential, match func(*authpkg.Credential) bool, state *retryState, cooldown time.Duration, reason string) (*authpkg.Credential, bool) {
	if state.fallbackTried {
		return nil, false
	}
	state.fallbackTried = true
	if cooldown > 0 {
		pool.Cooldown(cred, cooldown)
	}
	nextCred, err := pool.PickNextMatching(providerName, cred, match)
	if err != nil {
		return nil, false
	}
	slog.Warn("retrying with fallback credential", "provider", providerName, "reason", reason, "credential", cred.Label, "fallback", nextCred.Label)
	return nextCred, true
}

func ensureOKOrRetryMatching(pool *providerspkg.Pool, providerName string, cred *authpkg.Credential, match func(*authpkg.Credential) bool, attempt func(*authpkg.Credential) (*http.Response, error)) (*http.Response, *authpkg.Credential, error) {
	resp, nextCred, err := doWithCredentialRetryMatching(pool, providerName, cred, match, attempt)
	if err != nil {
		return nil, nextCred, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nextCred, nil
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return nil, nextCred, fmt.Errorf("upstream status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func rebuildErrorResponse(resp *http.Response, body []byte) *http.Response {
	return &http.Response{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Header:     resp.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func ensureOKOrRetry(pool *providerspkg.Pool, providerName string, cred *authpkg.Credential, attempt func(*authpkg.Credential) (*http.Response, error)) (*http.Response, *authpkg.Credential, error) {
	resp, nextCred, err := doWithCredentialRetry(pool, providerName, cred, attempt)
	if err != nil {
		return nil, nextCred, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nextCred, nil
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return nil, nextCred, fmt.Errorf("upstream status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
