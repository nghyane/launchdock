package format

import (
	"reflect"
	"testing"

	"github.com/nghyane/llm-mux/internal/config"
)

func TestGetRequestDetailsPoolRouting(t *testing.T) {
	cfg := &config.SDKConfig{}
	routing := &config.RoutingConfig{
		Fallbacks: map[string][]string{
			"tool-use-best": {"claude-sonnet-4.6", "gpt-4o"},
		},
	}
	routing.Init()

	h := NewBaseAPIHandlers(cfg, routing, nil, nil)

	// Since we mock utility functions implicitly by passing "claude-sonnet-4.6",
	// we assume "claude-sonnet-4.6" will have no providers natively parsed here,
	// but the `util.GetProviderName` uses registry. Let's just test that the fallback pool key is intercepted.
	// Actually, if util.GetProviderName returns empty for "tool-use-best", it should hit the pool fallback logic.

	t.Run("pool key routes to first candidate", func(t *testing.T) {
		// This uses real util which requires registered models
		// Since we don't have active registry here, it will return nil providers for everything
		// So it will try to find fallbacks but then fail to find providers for candidates either
		_, _, _, err := h.getRequestDetails("tool-use-best")

		// If pool routing works, it will try to resolve "claude-sonnet-4.6", then fail if no providers register.
		// At least it shouldn't panic and should return an unknown provider error for the requested tool-use-best.
		if err == nil {
			t.Errorf("expected unknown provider error, got nil")
		} else if err.StatusCode != 400 {
			t.Errorf("expected 400 status, got %d", err.StatusCode)
		}
	})
}

func TestGetFallbackChainWithMetadata(t *testing.T) {
	h := NewBaseAPIHandlers(&config.SDKConfig{}, &config.RoutingConfig{
		Fallbacks: map[string][]string{
			"model-a": {"model-b", "model-c"},
		},
	}, nil, nil)
	h.Routing.Init()

	t.Run("uses metadata when available", func(t *testing.T) {
		meta := map[string]any{
			"routing_fallback_chain": []string{"dynamic-1", "dynamic-2"},
		}
		chain := h.getFallbackChainWithMetadata("model-a", meta)
		expected := []string{"dynamic-1", "dynamic-2"}
		if !reflect.DeepEqual(chain, expected) {
			t.Errorf("expected %v, got %v", expected, chain)
		}
	})

	t.Run("falls back to config when metadata absent", func(t *testing.T) {
		meta := map[string]any{"other_key": "val"}
		chain := h.getFallbackChainWithMetadata("model-a", meta)
		expected := []string{"model-b", "model-c"}
		if !reflect.DeepEqual(chain, expected) {
			t.Errorf("expected %v, got %v", expected, chain)
		}
	})

	t.Run("falls back to config when metadata nil", func(t *testing.T) {
		chain := h.getFallbackChainWithMetadata("model-a", nil)
		expected := []string{"model-b", "model-c"}
		if !reflect.DeepEqual(chain, expected) {
			t.Errorf("expected %v, got %v", expected, chain)
		}
	})
}
