package launchdock

import (
	"fmt"
	"os"
	"strings"
)

func handleLaunchCommand() {
	var toolName, modelFlag string
	configOnly := false
	skipNext := false
	args := os.Args[2:]

	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		switch arg {
		case "--config":
			configOnly = true
		case "--port":
			skipNext = true
		case "--model":
			if i+1 < len(args) {
				modelFlag = args[i+1]
			}
			skipNext = true
		default:
			if strings.HasPrefix(arg, "--") {
				continue
			}
			if toolName == "" {
				toolName = strings.ToLower(arg)
			}
		}
	}

	if toolName == "" {
		handleLaunchInteractive()
		return
	}
	if toolName == "claude" {
		toolName = "claude-code"
	}

	t := findTool(toolName)
	if t == nil {
		fmt.Fprintf(os.Stderr, "Unknown tool: %s\n\n", toolName)
		printLaunchHelp()
		os.Exit(1)
	}
	if !isInstalled(t.Binary) {
		fmt.Fprintf(os.Stderr, "✗ %s not found in PATH\n", t.Binary)
		fmt.Fprintf(os.Stderr, "  Install %s first.\n", t.Desc)
		os.Exit(1)
	}

	cfg := resolveLaunchConfig()
	validateLaunchReadiness(cfg, t)
	if !configOnly {
		if err := ensureServerRunning(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "✗ failed to start launchdock: %v\n", err)
			os.Exit(1)
		}
	}

	model := resolveLaunchModel(cfg, t, modelFlag)
	fmt.Printf("  %s%s%s → %s\n", ansiBold, t.Name, ansiReset, model)
	if t.Config != nil {
		t.Config(cfg, model)
	}
	if configOnly {
		fmt.Printf("\n  %s✓ Configured%s\n", ansiGreen, ansiReset)
		return
	}
	launchTool(t, cfg, model)
}

func validateLaunchReadiness(cfg LaunchConfig, t *Tool) {
	if !cfg.HasCreds {
		fmt.Fprintf(os.Stderr, "✗ No credentials found\n")
		fmt.Fprintf(os.Stderr, "  Run: launchdock auth login claude\n")
		fmt.Fprintf(os.Stderr, "  Or set: ANTHROPIC_API_KEY / OPENAI_API_KEY\n")
		os.Exit(1)
	}
	if t.Provider == "" || cfg.HasProvider(t.Provider) {
		return
	}
	fmt.Fprintf(os.Stderr, "✗ No %s credentials found for %s\n", providerDisplayName(t.Provider), t.Name)
	fmt.Fprintf(os.Stderr, "  Found providers: %s\n", strings.Join(discoveredProviders(cfg), ", "))
	if t.Provider == "anthropic" {
		fmt.Fprintf(os.Stderr, "  Run: launchdock auth login claude\n")
	} else {
		fmt.Fprintf(os.Stderr, "  Add %s credentials via local auth or env vars\n", providerDisplayName(t.Provider))
	}
	os.Exit(1)
}

func resolveLaunchModel(cfg LaunchConfig, t *Tool, forced string) string {
	if forced != "" {
		return forced
	}
	candidates := candidateModelIDs(cfg, t.Provider)
	if len(candidates) == 1 {
		return candidates[0]
	}
	if len(candidates) > 1 && isTerminal(int(os.Stdin.Fd())) {
		fmt.Printf("\n  %s⚡ %d models available%s\n\n", ansiBold, len(candidates), ansiReset)
		idx := runPicker("Select model for "+t.Name+":", candidates)
		if idx < 0 {
			fmt.Println("  Cancelled.")
			os.Exit(0)
		}
		return candidates[idx]
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return "claude-sonnet-4-20250514"
}

func handleLaunchInteractive() {
	cfg := resolveLaunchConfig()
	if !isTerminal(int(os.Stdin.Fd())) {
		handleLaunchList(cfg)
		return
	}
	if !cfg.HasCreds {
		fmt.Fprintf(os.Stderr, "✗ No credentials found\n")
		fmt.Fprintf(os.Stderr, "  Run: launchdock auth login claude\n")
		os.Exit(1)
	}
	if err := ensureServerRunning(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "✗ failed to start launchdock: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n  %s⚡ %d models available%s\n\n", ansiBold, len(cfg.Models), ansiReset)
	var items []checkboxItem
	var launchable []*Tool
	for i := range tools {
		t := &tools[i]
		installed := isInstalled(t.Binary)
		desc := t.Desc
		if installed {
			desc += fmt.Sprintf(" %s(✓)%s", ansiGreen, ansiReset)
		} else {
			desc += fmt.Sprintf(" %s(not found)%s", ansiDim, ansiReset)
		}
		items = append(items, checkboxItem{label: t.Name, desc: desc, disabled: !installed})
		launchable = append(launchable, t)
	}
	selected := runCheckbox("Select tool to launch:", items)
	if selected == nil || len(selected) == 0 {
		return
	}
	t := launchable[selected[0]]
	validateLaunchReadiness(cfg, t)
	model := resolveLaunchModel(cfg, t, "")
	fmt.Printf("\n  %s%s%s → %s\n", ansiBold, t.Name, ansiReset, model)
	if t.Config != nil {
		t.Config(cfg, model)
	}
	launchTool(t, cfg, model)
}

func handleLaunchList(cfg LaunchConfig) {
	fmt.Print("launchdock launch — available tools:\n\n")
	for _, t := range tools {
		status := "✗ not found"
		if isInstalled(t.Binary) {
			status = "✓ installed"
		}
		fmt.Printf("  %-12s %-25s %s\n", t.Name, t.Desc, status)
	}
	if cfg.HasCreds {
		fmt.Printf("\n  Models (%d):\n", len(cfg.Models))
		for _, m := range cfg.Models {
			fmt.Printf("    %s (%s)\n", m.ID, m.Provider)
		}
	}
	fmt.Println()
	printLaunchHelp()
}

func printLaunchHelp() {
	fmt.Print(`Usage: launchdock launch [tool] [--model MODEL] [--config] [--port PORT]

  launchdock launch                  Interactive tool & model picker
  launchdock launch <tool>           Launch tool with model picker
  launchdock launch <tool> --model X Launch tool with specific model
  launchdock launch <tool> --config  Write config only (don't launch)

Tools: claude-code (claude), codex, opencode, droid, pi

`)
}
