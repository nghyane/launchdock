package launchdock

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Tool struct {
	Name     string
	Desc     string
	Binary   string
	Provider string
	Config   func(LaunchConfig, string)
	ExecArgs func(LaunchConfig, string) ([]string, []string)
}

var tools = []Tool{
	{Name: "claude-code", Desc: "Claude Code CLI", Binary: "claude", Provider: "anthropic", ExecArgs: execClaudeCode},
	{Name: "codex", Desc: "OpenAI Codex CLI", Binary: "codex", Provider: "openai", Config: configCodex, ExecArgs: execCodex},
	{Name: "opencode", Desc: "OpenCode", Binary: "opencode", Config: configOpenCode, ExecArgs: execOpenCode},
	{Name: "droid", Desc: "Factory Droid CLI", Binary: "droid", Config: configDroid, ExecArgs: execDroid},
	{Name: "pi", Desc: "Pi agent", Binary: "pi", Config: configPi, ExecArgs: execPi},
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
	_, err := exec.LookPath(binary)
	return err == nil
}

func candidateModelIDs(cfg LaunchConfig, provider string) []string {
	models := cfg.FilterModels(provider)
	if len(models) == 0 {
		models = cfg.Models
	}
	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids
}

func launchTool(t *Tool, cfg LaunchConfig, model string) {
	args, env := t.ExecArgs(cfg, model)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), env...)

	fmt.Printf("  Launching: %s %s\n\n", t.Binary, strings.Join(args[1:], " "))
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "✗ exec failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func execClaudeCode(cfg LaunchConfig, model string) ([]string, []string) {
	return []string{"claude", "--model", model}, []string{
		"ANTHROPIC_BASE_URL=" + cfg.RawURL,
		"ANTHROPIC_AUTH_TOKEN=" + cfg.APIKey,
	}
}

func configCodex(cfg LaunchConfig, model string) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".codex")
	path := filepath.Join(dir, "config.toml")
	existing, _ := os.ReadFile(path)
	cleaned := stripCodexProviderBlock(string(existing), "launchdock")
	cleaned = stripCodexProviderBlock(cleaned, "llm-mux")
	_ = os.MkdirAll(dir, 0755)
	block := fmt.Sprintf("\n[model_providers.launchdock]\nname = \"launchdock\"\nbase_url = \"%s\"\nenv_key = \"LAUNCHDOCK_KEY\"\n", cfg.BaseURL)
	content := strings.TrimRight(cleaned, "\n") + "\n" + block
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Write error: %v\n", err)
		return
	}
	fmt.Printf("  ✓ Wrote %s\n", path)
}

func execCodex(cfg LaunchConfig, model string) ([]string, []string) {
	return []string{"codex", "-c", `model_provider="launchdock"`, "--model", model}, []string{"LAUNCHDOCK_KEY=" + cfg.APIKey}
}

func stripCodexProviderBlock(content, provider string) string {
	lines := strings.Split(content, "\n")
	var out []string
	skip := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == fmt.Sprintf("model_provider = %q", provider) {
			continue
		}
		if trimmed == fmt.Sprintf("[model_providers.%s]", provider) {
			skip = true
			continue
		}
		if skip {
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				skip = false
			} else {
				continue
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func configOpenCode(cfg LaunchConfig, model string) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "opencode")
	path := filepath.Join(dir, "opencode.json")
	models := map[string]any{}
	for _, m := range cfg.Models {
		models[m.ID] = map[string]any{"name": m.ID}
	}
	config := map[string]any{}
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		if err := json.Unmarshal(existing, &config); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ Read error: invalid JSON in %s: %v\n", path, err)
			return
		}
	}
	config["$schema"] = "https://opencode.ai/config.json"
	providers, _ := config["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers["launchdock"] = map[string]any{
		"npm":     "@ai-sdk/openai-compatible",
		"name":    "launchdock",
		"options": map[string]any{"baseURL": cfg.BaseURL},
		"models":  models,
	}
	config["provider"] = providers
	writeJSONConfig(path, dir, config)
}

func execOpenCode(cfg LaunchConfig, model string) ([]string, []string) {
	return []string{"opencode"}, nil
}

func configDroid(cfg LaunchConfig, model string) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".factory")
	path := filepath.Join(dir, "config.json")
	var customModels []map[string]any
	for _, m := range cfg.Models {
		customModels = append(customModels, map[string]any{
			"model_display_name": m.ID + " [launchdock]",
			"model":              m.ID,
			"base_url":           cfg.BaseURL + "/",
			"api_key":            cfg.APIKey,
			"provider":           "generic-chat-completion-api",
			"max_tokens":         128000,
		})
	}
	writeJSONConfig(path, dir, map[string]any{"custom_models": customModels})
}

func execDroid(cfg LaunchConfig, model string) ([]string, []string) { return []string{"droid"}, nil }

func configPi(cfg LaunchConfig, model string) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".pi", "agent")
	var modelIDs []map[string]any
	for _, m := range cfg.Models {
		modelIDs = append(modelIDs, map[string]any{"id": m.ID})
	}
	writeJSONConfig(filepath.Join(dir, "models.json"), dir, map[string]any{
		"providers": map[string]any{
			"launchdock": map[string]any{
				"baseUrl": cfg.BaseURL,
				"api":     "openai-completions",
				"apiKey":  cfg.APIKey,
				"models":  modelIDs,
			},
		},
	})
	writeJSONConfig(filepath.Join(dir, "settings.json"), dir, map[string]any{
		"defaultProvider": "launchdock",
		"defaultModel":    model,
	})
}

func execPi(cfg LaunchConfig, model string) ([]string, []string) { return []string{"pi"}, nil }

func writeJSONConfig(path, dir string, config any) {
	data, _ := json.MarshalIndent(config, "", "  ")
	_ = os.MkdirAll(dir, 0755)
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("  ⚠ %s exists — overwriting\n", path)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Write error: %v\n", err)
		return
	}
	fmt.Printf("  ✓ Wrote %s\n", path)
}
