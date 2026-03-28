package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const muxBaseURL = "http://localhost:8090/v1"
const muxAPIKey = "llm-mux"

func handleSetupCommand() {
	if len(os.Args) < 3 {
		printSetupHelp()
		return
	}

	tool := strings.ToLower(os.Args[2])
	switch tool {
	case "cursor":
		setupCursor()
	case "cline":
		setupCline()
	case "aider":
		setupAider()
	case "continue":
		setupContinue()
	case "codex":
		setupCodex()
	case "opencode":
		setupOpenCode()
	case "claude", "claude-code":
		setupClaudeCode()
	case "droid":
		setupDroid()
	case "pi":
		setupPi()
	case "env":
		setupEnv()
	default:
		fmt.Fprintf(os.Stderr, "Unknown tool: %s\n\n", tool)
		printSetupHelp()
		os.Exit(1)
	}
}

func printSetupHelp() {
	fmt.Print(`Usage: llm-mux setup <tool>

Supported tools:
  cursor      Cursor IDE (manual — print instructions)
  cline       Cline VSCode extension (manual — print instructions)
  aider       Aider CLI (auto-write ~/.aider.conf.yml)
  continue    Continue.dev (auto-add model to config.yaml)
  codex       OpenAI Codex CLI (auto-write config.toml)
  claude-code Claude Code CLI (native /v1/messages)
  opencode    OpenCode (auto-write config.json)
  droid       Factory Droid CLI (auto-write config.json)
  pi          Pi agent (auto-write models.json)
  env         Print shell export commands

`)
}

// --- Claude Code (env vars — native /v1/messages) ---

func setupClaudeCode() {
	fmt.Print(`
┌─ Claude Code CLI Setup ─────────────────────────────────────┐
│                                                             │
│  Claude Code connects via /v1/messages (Anthropic format).  │
│  llm-mux supports this natively — same as Ollama does.      │
│                                                             │
│  Option 1: Environment variables                            │
│                                                             │
│    export ANTHROPIC_BASE_URL=http://localhost:8090           │
│    export ANTHROPIC_AUTH_TOKEN=llm-mux                       │
│    export ANTHROPIC_API_KEY=""                                │
│    claude --model claude-sonnet-4-20250514                    │
│                                                             │
│  Option 2: One-liner                                        │
│                                                             │
│    ANTHROPIC_BASE_URL=http://localhost:8090 \                │
│    ANTHROPIC_AUTH_TOKEN=llm-mux \                            │
│    ANTHROPIC_API_KEY="" \                                     │
│    claude --model claude-sonnet-4-20250514                    │
│                                                             │
│  This routes through mux's /v1/messages endpoint with       │
│  pool rotation, credential management, and caching.         │
│                                                             │
│  Use case: Run multiple Claude Code instances sharing       │
│  the same credential pool with automatic rotation.          │
└─────────────────────────────────────────────────────────────┘
`)
}

// --- Cursor (manual) ---

func setupCursor() {
	fmt.Print(`
┌─ Cursor IDE Setup ──────────────────────────────────────────┐
│                                                             │
│  1. Open Cursor → Settings (⌘,) → Models                   │
│  2. Set "OpenAI API Key" to: llm-mux                        │
│  3. Set "Override OpenAI Base URL" to:                       │
│     http://localhost:8090/v1                                 │
│  4. Select model: claude-sonnet-4-20250514                   │
│                                                             │
│  Note: Cursor may send Responses API format for some        │
│  models. llm-mux handles both /v1/chat/completions and      │
│  /v1/responses automatically.                               │
│                                                             │
│  Tip: Enable "HTTP Compatibility Mode" → HTTP/1.1 in        │
│  Cursor Settings → Network if you see TLS errors.           │
└─────────────────────────────────────────────────────────────┘
`)
}

// --- Cline (manual) ---

func setupCline() {
	fmt.Print(`
┌─ Cline / Roo-Cline VSCode Setup ────────────────────────────┐
│                                                              │
│  1. Open VSCode → Extensions → Cline → Settings              │
│  2. Set API Provider: "OpenAI Compatible"                    │
│  3. Base URL: http://localhost:8090/v1                        │
│  4. API Key:  llm-mux                                        │
│  5. Model ID: claude-sonnet-4-20250514                       │
│                                                              │
│  Or add to settings.json:                                    │
│  "cline.apiProvider": "openai-compatible",                   │
│  "cline.openAiBaseUrl": "http://localhost:8090/v1",          │
│  "cline.openAiApiKey": "llm-mux",                           │
│  "cline.openAiModelId": "claude-sonnet-4-20250514"           │
└──────────────────────────────────────────────────────────────┘
`)
}

// --- Aider (auto-write) ---

func setupAider() {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".aider.conf.yml")

	content := fmt.Sprintf(`## llm-mux configuration
## Routes to Claude/GPT via local proxy

openai-api-base: %s
openai-api-key: %s
model: openai/claude-sonnet-4-20250514

## Alternative models:
# model: openai/claude-opus-4-20250514
# model: openai/gpt-5.4
`, muxBaseURL, muxAPIKey)

	if err := writeConfigSafe(path, []byte(content)); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Wrote %s\n", path)
	fmt.Println("  Run: aider")
}

// --- Continue (auto-add model) ---

func setupContinue() {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".continue")
	path := filepath.Join(dir, "config.yaml")

	// Don't overwrite — append model entry
	entry := fmt.Sprintf(`
  # llm-mux models
  - name: Claude Sonnet (via llm-mux)
    provider: openai
    model: claude-sonnet-4-20250514
    apiBase: %s
    apiKey: %s
  - name: Claude Opus (via llm-mux)
    provider: openai
    model: claude-opus-4-20250514
    apiBase: %s
    apiKey: %s
  - name: GPT-5.4 (via llm-mux)
    provider: openai
    model: gpt-5.4
    apiBase: %s
    apiKey: %s
`, muxBaseURL, muxAPIKey, muxBaseURL, muxAPIKey, muxBaseURL, muxAPIKey)

	if _, err := os.Stat(path); err == nil {
		fmt.Printf("Found existing %s\n", path)
		fmt.Println("Add these model entries under 'models:' section:")
		fmt.Println(entry)
	} else {
		os.MkdirAll(dir, 0755)
		content := fmt.Sprintf("name: llm-mux\nversion: 1.0.0\nschema: v1\nmodels:\n%s", entry)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Wrote %s\n", path)
	}
}

// --- Codex (auto-write model_providers) ---

func setupCodex() {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".codex", "config.toml")

	existing, _ := os.ReadFile(path)
	content := string(existing)

	if strings.Contains(content, "llm-mux") {
		fmt.Printf("⚠ %s already configured for llm-mux\n", path)
		return
	}

	// Add model_providers block
	block := fmt.Sprintf(`
model_provider = "llm-mux"

[model_providers.llm-mux]
name = "llm-mux"
base_url = "%s"
env_key = "LLM_MUX_KEY"
`, muxBaseURL)

	newContent := content + block
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Updated %s\n", path)
	fmt.Println("  Run: codex -m claude-sonnet-4-20250514")
}

// --- OpenCode (auto-write) ---

func setupOpenCode() {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "opencode")
	path := filepath.Join(dir, "opencode.json")

	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			"llm-mux": map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "llm-mux",
				"options": map[string]any{
					"baseURL": muxBaseURL,
				},
				"models": map[string]any{
					"claude-sonnet-4-20250514": map[string]any{"name": "Claude Sonnet"},
					"claude-opus-4-20250514":   map[string]any{"name": "Claude Opus"},
					"gpt-5.4":                  map[string]any{"name": "GPT-5.4"},
				},
			},
		},
	}

	data, _ := json.MarshalIndent(config, "", "  ")
	os.MkdirAll(dir, 0755)

	if _, err := os.Stat(path); err == nil {
		fmt.Printf("⚠ %s already exists\n", path)
		fmt.Println("  Merge this provider block:")
		fmt.Println(string(data))
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Wrote %s\n", path)
}

// --- Droid (auto-write ~/.factory/config.json) ---

func setupDroid() {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".factory")
	path := filepath.Join(dir, "config.json")

	config := map[string]any{
		"custom_models": []map[string]any{
			{
				"model_display_name": "Claude Sonnet [llm-mux]",
				"model":              "claude-sonnet-4-20250514",
				"base_url":           muxBaseURL + "/",
				"api_key":            muxAPIKey,
				"provider":           "generic-chat-completion-api",
				"max_tokens":         128000,
			},
			{
				"model_display_name": "Claude Opus [llm-mux]",
				"model":              "claude-opus-4-20250514",
				"base_url":           muxBaseURL + "/",
				"api_key":            muxAPIKey,
				"provider":           "generic-chat-completion-api",
				"max_tokens":         128000,
			},
			{
				"model_display_name": "GPT-5.4 [llm-mux]",
				"model":              "gpt-5.4",
				"base_url":           muxBaseURL + "/",
				"api_key":            muxAPIKey,
				"provider":           "generic-chat-completion-api",
				"max_tokens":         128000,
			},
		},
	}

	data, _ := json.MarshalIndent(config, "", "  ")
	os.MkdirAll(dir, 0755)

	if _, err := os.Stat(path); err == nil {
		fmt.Printf("⚠ %s already exists\n", path)
		fmt.Println("  Merge these custom_models entries:")
		fmt.Println(string(data))
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Wrote %s\n", path)
	fmt.Println("  Run: droid")
}

// --- Pi (auto-write ~/.pi/agent/models.json + settings.json) ---

func setupPi() {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".pi", "agent")

	// models.json
	modelsPath := filepath.Join(dir, "models.json")
	modelsConfig := map[string]any{
		"providers": map[string]any{
			"llm-mux": map[string]any{
				"baseUrl": muxBaseURL,
				"api":     "openai-completions",
				"apiKey":  muxAPIKey,
				"models": []map[string]any{
					{"id": "claude-sonnet-4-20250514"},
					{"id": "claude-opus-4-20250514"},
					{"id": "gpt-5.4"},
				},
			},
		},
	}
	modelsData, _ := json.MarshalIndent(modelsConfig, "", "  ")

	// settings.json
	settingsPath := filepath.Join(dir, "settings.json")
	settingsConfig := map[string]any{
		"defaultProvider": "llm-mux",
		"defaultModel":    "claude-sonnet-4-20250514",
	}
	settingsData, _ := json.MarshalIndent(settingsConfig, "", "  ")

	os.MkdirAll(dir, 0755)

	if _, err := os.Stat(modelsPath); err == nil {
		fmt.Printf("⚠ %s already exists\n", modelsPath)
		fmt.Println("  Merge this provider:")
		fmt.Println(string(modelsData))
	} else {
		if err := os.WriteFile(modelsPath, modelsData, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing models.json: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Wrote %s\n", modelsPath)
	}

	if _, err := os.Stat(settingsPath); err == nil {
		fmt.Printf("⚠ %s already exists — update manually:\n", settingsPath)
		fmt.Println(string(settingsData))
	} else {
		if err := os.WriteFile(settingsPath, settingsData, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing settings.json: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Wrote %s\n", settingsPath)
	}
	fmt.Println("  Run: pi")
}

// --- Env vars ---

func setupEnv() {
	fmt.Printf(`# Add to your shell profile (~/.zshrc, ~/.bashrc):

export OPENAI_API_BASE=%s
export OPENAI_API_KEY=%s

# Or for specific tools:
export OPENAI_BASE_URL=%s     # OpenAI SDK
export ANTHROPIC_BASE_URL=%s  # Anthropic SDK (if needed)

# Then use any OpenAI-compatible tool:
#   aider --model openai/claude-sonnet-4-20250514
#   python -c "from openai import OpenAI; c=OpenAI(); ..."
`, muxBaseURL, muxAPIKey, muxBaseURL, "http://localhost:8090")
}

// --- Helpers ---

func writeConfigSafe(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		// Backup existing
		backup := path + ".bak"
		if err := os.Rename(path, backup); err != nil {
			return fmt.Errorf("backup %s: %w", path, err)
		}
		fmt.Printf("  Backed up existing to %s\n", backup)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
