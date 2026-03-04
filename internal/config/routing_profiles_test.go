package config

import "testing"

func TestRoutingProfilesMaterializeIntoAliasAndFallback(t *testing.T) {
	r := &RoutingConfig{
		Profiles: map[string]RoutingProfile{
			"chat-fast": {
				Primary:   "gemini-2.5-flash-lite",
				Fallbacks: []string{"gpt-4o-mini", "claude-haiku-4.5"},
			},
		},
	}

	r.Init()

	if got := r.ResolveModelAlias("chat-fast"); got != "gemini-2.5-flash-lite" {
		t.Fatalf("expected profile alias to resolve to primary, got %q", got)
	}
	chain := r.GetFallbackChain("chat-fast")
	if len(chain) != 2 || chain[0] != "gpt-4o-mini" || chain[1] != "claude-haiku-4.5" {
		t.Fatalf("unexpected profile fallback chain: %v", chain)
	}
}

func TestRoutingProfilesRespectsPreexistingAliasOwnership(t *testing.T) {
	r := &RoutingConfig{
		Aliases: map[string]string{
			"chat-fast": "gpt-4.1",
		},
		Profiles: map[string]RoutingProfile{
			"chat-fast": {
				Primary:   "gemini-2.5-flash-lite",
				Fallbacks: []string{"gpt-4o-mini", "claude-haiku-4.5"},
			},
		},
	}

	r.Init()

	if got := r.ResolveModelAlias("chat-fast"); got != "gpt-4.1" {
		t.Fatalf("expected preexisting alias to be preserved, got %q", got)
	}
	if chain := r.GetFallbackChain("chat-fast"); len(chain) != 0 {
		t.Fatalf("expected no injected fallback for preowned alias, got %v", chain)
	}
}

func TestRoutingProfilesMaterializeAliasWithoutFallback(t *testing.T) {
	r := &RoutingConfig{
		Profiles: map[string]RoutingProfile{
			"chat-fast": {
				Primary: "gemini-2.5-flash-lite",
			},
		},
	}

	r.Init()

	if got := r.ResolveModelAlias("chat-fast"); got != "gemini-2.5-flash-lite" {
		t.Fatalf("expected profile alias to resolve to primary, got %q", got)
	}
	if chain := r.GetFallbackChain("chat-fast"); len(chain) != 0 {
		t.Fatalf("expected no fallback chain for profile without fallbacks, got %v", chain)
	}
}

func TestRoutingProfilesRespectsPreexistingFallbackChain(t *testing.T) {
	r := &RoutingConfig{
		Fallbacks: map[string][]string{
			"chat-fast": {"gpt-4.1"},
		},
		Profiles: map[string]RoutingProfile{
			"chat-fast": {
				Primary:   "gemini-2.5-flash-lite",
				Fallbacks: []string{"gpt-4o-mini", "claude-haiku-4.5"},
			},
		},
	}

	r.Init()

	if got := r.ResolveModelAlias("chat-fast"); got != "gemini-2.5-flash-lite" {
		t.Fatalf("expected profile alias to resolve to primary, got %q", got)
	}
	chain := r.GetFallbackChain("chat-fast")
	if len(chain) != 1 || chain[0] != "gpt-4.1" {
		t.Fatalf("expected preexisting fallback chain to be preserved, got %v", chain)
	}
}
