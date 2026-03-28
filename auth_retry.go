package launchdock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
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

func doWithCredentialRetry(pool *Pool, providerName string, cred *Credential, attempt func(*Credential) (*http.Response, error)) (*http.Response, *Credential, error) {
	refreshedSame := false
	fallbackTried := false

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
			if !refreshedSame && cred.AuthType == AuthOAuth && cred.RefreshToken != "" {
				refreshedSame = true
				if err := pool.refresh(cred); err == nil {
					slog.Warn("auth failure recovered by refresh", "provider", providerName, "credential", cred.Label)
					continue
				}
			}
			if !fallbackTried {
				fallbackTried = true
				pool.Cooldown(cred, 45*time.Second)
				nextCred, err := pool.PickNext(providerName, cred)
				if err == nil {
					slog.Warn("retrying with fallback credential after auth failure", "provider", providerName, "credential", cred.Label, "fallback", nextCred.Label)
					cred = nextCred
					continue
				}
			}
			return rebuildErrorResponse(resp, errBody), cred, nil
		}

		if isRetryable(resp.StatusCode) && !fallbackTried {
			slog.Warn("retryable upstream error, trying next credential", "status", resp.StatusCode, "credential", cred.Label, "body", string(errBody))
			switch resp.StatusCode {
			case 429:
				pool.Cooldown(cred, 60*time.Second)
			case 529, 503:
				pool.Cooldown(cred, 30*time.Second)
			}
			nextCred, err := pool.PickNext(providerName, cred)
			if err == nil {
				fallbackTried = true
				cred = nextCred
				continue
			}
		}

		return rebuildErrorResponse(resp, errBody), cred, nil
	}
}

func rebuildErrorResponse(resp *http.Response, body []byte) *http.Response {
	return &http.Response{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Header:     resp.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func ensureOKOrRetry(pool *Pool, providerName string, cred *Credential, attempt func(*Credential) (*http.Response, error)) (*http.Response, *Credential, error) {
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
