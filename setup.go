package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// --- Setup config (resolved at runtime) ---

type SetupConfig struct {
	BaseURL      string // e.g. http://localhost:8090/v1
	RawURL       string // e.g. http://localhost:8090 (no /v1)
	APIKey       string
	Models       []string // dynamic from /v1/models
	DefaultModel string
}

func resolveSetupConfig() SetupConfig {
	port := "8090"
	// Check --port flag
	for i, arg := range os.Args {
		if arg == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	raw := fmt.Sprintf("http://localhost:%s", port)
	base := raw + "/v1"
	cfg := SetupConfig{
		BaseURL: base,
		RawURL:  raw,
		APIKey:  "llm-mux",
	}

	// Try fetch models from running mux
	cfg.Models = fetchModelsFromMux(base)
	if len(cfg.Models) > 0 {
		cfg.DefaultModel = cfg.Models[0]
	} else {
		cfg.DefaultModel = "claude-sonnet-4-20250514"
		cfg.Models = []string{"claude-sonnet-4-20250514", "claude-opus-4-20250514", "gpt-5.4"}
	}

	return cfg
}

func fetchModelsFromMux(baseURL string) []string {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL + "/models")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&result) != nil {
		return nil
	}
	var models []string
	for _, m := range result.Data {
		models = append(models, m.ID)
	}
	return models
}

// --- Tool registry ---

type Tool struct {
	Name   string
	Desc   string
	Binary string // binary name for detection
	Setup  func(cfg SetupConfig)
}

var tools = []Tool{
	{"cursor", "Cursor IDE", "cursor", setupCursor},
	{"cline", "Cline / Roo-Cline (VSCode)", "", setupCline},
	{"aider", "Aider CLI", "aider", setupAider},
	{"continue", "Continue.dev", "", setupContinue},
	{"codex", "OpenAI Codex CLI", "codex", setupCodex},
	{"claude-code", "Claude Code CLI", "claude", setupClaudeCode},
	{"opencode", "OpenCode", "opencode", setupOpenCode},
	{"droid", "Factory Droid CLI", "droid", setupDroid},
	{"pi", "Pi agent", "pi", setupPi},
	{"env", "Shell environment variables", "", setupEnv},
}

func findTool(name string) *Tool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

func isInstalled(binary string) bool {
	if binary == "" {
		return false
	}
	_, err := exec.LookPath(binary)
	return err == nil
}

// --- Entry point ---

func handleSetupCommand() {
	// Parse --port flag and remove from args
	var toolName string
	for _, arg := range os.Args[2:] {
		if arg == "--port" || strings.HasPrefix(arg, "--") {
			continue
		}
		if toolName == "" {
			toolName = strings.ToLower(arg)
		}
	}

	if toolName == "" || toolName == "--check" {
		if toolName == "--check" {
			handleSetupCheck()
			return
		}
		handleSetupInteractive()
		return
	}

	// Alias
	if toolName == "claude" {
		toolName = "claude-code"
	}

	t := findTool(toolName)
	if t == nil {
		fmt.Fprintf(os.Stderr, "Unknown tool: %s\n\n", toolName)
		printSetupHelp()
		os.Exit(1)
	}

	cfg := resolveSetupConfig()

	// Check if binary installed
	if t.Binary != "" && !isInstalled(t.Binary) {
		fmt.Fprintf(os.Stderr, "⚠ %s (%s) not found in PATH\n", t.Name, t.Binary)
		fmt.Fprintf(os.Stderr, "  Setup will continue but config may not be used.\n\n")
	}

	if len(cfg.Models) > 3 {
		fmt.Printf("⚡ Connected to llm-mux — %d models available\n", len(cfg.Models))
	} else {
		fmt.Println("⚠ Could not connect to llm-mux — using defaults")
		fmt.Println("  Start mux first: llm-mux &")
	}
	fmt.Println()

	t.Setup(cfg)
}

func handleSetupInteractive() {
	cfg := resolveSetupConfig()

	fmt.Println("llm-mux setup — detected tools:\n")

	found := 0
	for _, t := range tools {
		if t.Binary == "" {
			continue
		}
		status := "  ✗ not found"
		if isInstalled(t.Binary) {
			status = "  ✓ installed"
			found++
		}
		fmt.Printf("  %-12s %-30s %s\n", t.Name, t.Desc, status)
	}

	fmt.Printf("\n  %d tool(s) detected. Run: llm-mux setup <tool>\n", found)

	if len(cfg.Models) > 0 {
		fmt.Printf("\n  Models available (%d):\n", len(cfg.Models))
		for _, m := range cfg.Models {
			fmt.Printf("    %s\n", m)
		}
	}
	fmt.Println()
	printSetupHelp()
}

func handleSetupCheck() {
	fmt.Println("llm-mux setup --check\n")
	cfg := resolveSetupConfig()

	if len(cfg.Models) > 0 {
		fmt.Printf("✓ llm-mux running at %s (%d models)\n", cfg.RawURL, len(cfg.Models))
	} else {
		fmt.Printf("✗ llm-mux not running at %s\n", cfg.RawURL)
	}
	fmt.Println()
}

func printSetupHelp() {
	fmt.Print(`Usage: llm-mux setup [tool] [--port PORT]

  llm-mux setup              Detect installed tools
  llm-mux setup <tool>       Configure a specific tool
  llm-mux setup --check      Verify mux is running

Tools: cursor, cline, aider, continue, codex, claude-code,
       opencode, droid, pi, env

`)
}

// --- Tool setup implementations ---

func setupCursor(cfg SetupConfig) {
	fmt.Printf(`┌─ Cursor IDE ────────────────────────────────────────────────┐
│                                                             │
│  1. Open Cursor → Settings (⌘,) → Models                   │
│  2. Set "OpenAI API Key" to: %s
│  3. Set "Override OpenAI Base URL" to:                       │
│     %s
│  4. Select model: %s
│                                                             │
│  Note: Cursor sends both Chat Completions and Responses     │
│  API formats. llm-mux handles both automatically.           │
│                                                             │
│  Tip: Enable "HTTP Compatibility Mode" → HTTP/1.1 if        │
│  you see TLS errors.                                        │
└─────────────────────────────────────────────────────────────┘
`, pad(cfg.APIKey, 25), pad(cfg.BaseURL, 25), pad(cfg.DefaultModel, 25))
}

func setupCline(cfg SetupConfig) {
	fmt.Printf(`┌─ Cline / Roo-Cline (VSCode) ─────────────────────────────────┐
│                                                              │
│  1. Open VSCode → Extensions → Cline → Settings              │
│  2. API Provider: "OpenAI Compatible"                        │
│  3. Base URL: %s
│  4. API Key:  %s
│  5. Model ID: %s
│                                                              │
│  Or add to settings.json:                                    │
│  "cline.apiProvider": "openai-compatible",                   │
│  "cline.openAiBaseUrl": "%s",
│  "cline.openAiApiKey": "%s",
│  "cline.openAiModelId": "%s"
└──────────────────────────────────────────────────────────────┘
`, pad(cfg.BaseURL, 26), pad(cfg.APIKey, 26), pad(cfg.DefaultModel, 26),
		cfg.BaseURL, cfg.APIKey, cfg.DefaultModel)
}

func setupClaudeCode(cfg SetupConfig) {
	fmt.Printf(`┌─ Claude Code CLI ────────────────────────────────────────────┐
│                                                              │
│  Claude Code uses /v1/messages (Anthropic format) natively.  │
│                                                              │
│  Option 1: Environment variables                             │
│                                                              │
│    export ANTHROPIC_BASE_URL=%s
│    export ANTHROPIC_AUTH_TOKEN=%s
│    export ANTHROPIC_API_KEY=""                                │
│    claude --model %s
│                                                              │
│  Option 2: One-liner                                         │
│                                                              │
│    ANTHROPIC_BASE_URL=%s \
│    ANTHROPIC_AUTH_TOKEN=%s \
│    ANTHROPIC_API_KEY="" \                                     │
│    claude --model %s
│                                                              │
│  Use case: Multiple Claude Code instances sharing            │
│  the same credential pool with automatic rotation.           │
└──────────────────────────────────────────────────────────────┘
`, pad(cfg.RawURL, 22), pad(cfg.APIKey, 22), pad(cfg.DefaultModel, 22),
		cfg.RawURL, cfg.APIKey, cfg.DefaultModel)
}

func setupAider(cfg SetupConfig) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".aider.conf.yml")

	var modelLines []string
	modelLines = append(modelLines, fmt.Sprintf("model: openai/%s", cfg.DefaultModel))
	for _, m := range cfg.Models {
		if m != cfg.DefaultModel {
			modelLines = append(modelLines, fmt.Sprintf("# model: openai/%s", m))
		}
	}

	content := fmt.Sprintf("## llm-mux configuration\n\nopenai-api-base: %s\nopenai-api-key: %s\n%s\n",
		cfg.BaseURL, cfg.APIKey, strings.Join(modelLines, "\n"))

	if err := writeConfigSafe(path, []byte(content)); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Wrote %s\n", path)
	fmt.Println("  Run: aider")
}

func setupContinue(cfg SetupConfig) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".continue")
	path := filepath.Join(dir, "config.yaml")

	var entries []string
	for _, m := range cfg.Models {
		entries = append(entries, fmt.Sprintf(
			"  - name: %s (via llm-mux)\n    provider: openai\n    model: %s\n    apiBase: %s\n    apiKey: %s",
			m, m, cfg.BaseURL, cfg.APIKey))
	}
	block := "\n  # llm-mux models\n" + strings.Join(entries, "\n")

	if _, err := os.Stat(path); err == nil {
		fmt.Printf("Found existing %s\nAdd under 'models:' section:\n%s\n", path, block)
	} else {
		os.MkdirAll(dir, 0755)
		content := "name: llm-mux\nversion: 1.0.0\nschema: v1\nmodels:\n" + block + "\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Wrote %s\n", path)
	}
}

func setupCodex(cfg SetupConfig) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".codex", "config.toml")

	existing, _ := os.ReadFile(path)
	if strings.Contains(string(existing), "llm-mux") {
		fmt.Printf("⚠ %s already configured for llm-mux\n", path)
		return
	}

	block := fmt.Sprintf("\nmodel_provider = \"llm-mux\"\n\n[model_providers.llm-mux]\nname = \"llm-mux\"\nbase_url = \"%s\"\nenv_key = \"LLM_MUX_KEY\"\n", cfg.BaseURL)
	newContent := string(existing) + block
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Updated %s\n", path)
	fmt.Printf("  Run: codex -m %s\n", cfg.DefaultModel)
}

func setupOpenCode(cfg SetupConfig) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "opencode")
	path := filepath.Join(dir, "opencode.json")

	models := map[string]any{}
	for _, m := range cfg.Models {
		models[m] = map[string]any{"name": m}
	}

	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			"llm-mux": map[string]any{
				"npm":     "@ai-sdk/openai-compatible",
				"name":    "llm-mux",
				"options": map[string]any{"baseURL": cfg.BaseURL},
				"models":  models,
			},
		},
	}

	writeJSONConfigSafe(path, dir, config)
}

func setupDroid(cfg SetupConfig) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".factory")
	path := filepath.Join(dir, "config.json")

	var customModels []map[string]any
	for _, m := range cfg.Models {
		customModels = append(customModels, map[string]any{
			"model_display_name": m + " [llm-mux]",
			"model":              m,
			"base_url":           cfg.BaseURL + "/",
			"api_key":            cfg.APIKey,
			"provider":           "generic-chat-completion-api",
			"max_tokens":         128000,
		})
	}

	writeJSONConfigSafe(path, dir, map[string]any{"custom_models": customModels})
	fmt.Println("  Run: droid")
}

func setupPi(cfg SetupConfig) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".pi", "agent")

	var modelIDs []map[string]any
	for _, m := range cfg.Models {
		modelIDs = append(modelIDs, map[string]any{"id": m})
	}

	modelsConfig := map[string]any{
		"providers": map[string]any{
			"llm-mux": map[string]any{
				"baseUrl": cfg.BaseURL,
				"api":     "openai-completions",
				"apiKey":  cfg.APIKey,
				"models":  modelIDs,
			},
		},
	}
	settingsConfig := map[string]any{
		"defaultProvider": "llm-mux",
		"defaultModel":    cfg.DefaultModel,
	}

	writeJSONConfigSafe(filepath.Join(dir, "models.json"), dir, modelsConfig)
	writeJSONConfigSafe(filepath.Join(dir, "settings.json"), dir, settingsConfig)
	fmt.Println("  Run: pi")
}

func setupEnv(cfg SetupConfig) {
	fmt.Printf(`# Add to ~/.zshrc or ~/.bashrc:

export OPENAI_API_BASE=%s
export OPENAI_API_KEY=%s

# For Anthropic SDK / Claude Code:
export OPENAI_BASE_URL=%s
export ANTHROPIC_BASE_URL=%s

# Then use any OpenAI-compatible tool:
#   aider --model openai/%s
#   python -c "from openai import OpenAI; c=OpenAI(); ..."
`, cfg.BaseURL, cfg.APIKey, cfg.BaseURL, cfg.RawURL, cfg.DefaultModel)
}

// --- Helpers ---

func writeConfigSafe(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		backup := path + ".bak"
		if err := os.Rename(path, backup); err != nil {
			return fmt.Errorf("backup %s: %w", path, err)
		}
		fmt.Printf("  Backed up existing to %s\n", backup)
	}
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)
	return os.WriteFile(path, data, 0644)
}

func writeJSONConfigSafe(path, dir string, config any) {
	data, _ := json.MarshalIndent(config, "", "  ")
	os.MkdirAll(dir, 0755)

	if _, err := os.Stat(path); err == nil {
		fmt.Printf("⚠ %s already exists\n", path)
		fmt.Println("  Merge this config:")
		fmt.Println(string(data))
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Wrote %s\n", path)
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
