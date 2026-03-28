package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func handleAuthCommand() {
	if len(os.Args) < 3 {
		if isTerminal(int(os.Stdin.Fd())) {
			handleAuthInteractive()
			return
		}
		printAuthHelp()
		os.Exit(1)
	}

	switch os.Args[2] {
	case "list":
		handleAuthList()
	case "export":
		handleAuthExport()
	case "import":
		handleAuthImport()
	case "login":
		handleAuthLogin()
	case "push":
		handleAuthPush()
	case "remove":
		handleAuthRemove()
	default:
		fmt.Fprintf(os.Stderr, "Unknown auth command: %s\n\n", os.Args[2])
		printAuthHelp()
		os.Exit(1)
	}
}

func handleAuthRemove() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "Usage: launchdock auth remove <credential-id>")
		os.Exit(1)
	}
	id := os.Args[3]
	if err := removeConfigCredential(id); err != nil {
		fmt.Fprintf(os.Stderr, "Remove failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Removed managed credential: %s\n", id)
}

func handleAuthList() {
	views := LoadCredentialViews()
	if len(views) == 0 {
		fmt.Println("No credentials found.")
		return
	}
	for i, v := range views {
		fmt.Println(authListLine(i, v))
	}
}

func handleAuthLogin() {
	provider := "claude"
	if len(os.Args) >= 4 {
		provider = strings.ToLower(os.Args[3])
	}
	switch provider {
	case "claude", "anthropic":
		label := "Claude Account"
		if len(os.Args) >= 5 {
			label = os.Args[4]
		}
		cred, err := RunOAuthFlow(label)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Auth failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "\nAuthenticated: %s\n", cred.Label)
		fmt.Fprintf(os.Stderr, "Token expires: %s\n", cred.ExpiresAt.Format(time.RFC3339))
		fmt.Fprintf(os.Stderr, "Saved to: %s\n", configPath())
	case "openai", "codex":
		handleOpenAILogin()
	default:
		fmt.Fprintf(os.Stderr, "Unsupported auth provider: %s\n", provider)
		fmt.Fprintf(os.Stderr, "  Supported: claude, openai\n")
		os.Exit(1)
	}
}

func handleOpenAILogin() {
	cred, err := RunOpenAIOAuthFlow("OpenAI Account")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Auth failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\nAuthenticated: %s\n", cred.Label)
	if !cred.ExpiresAt.IsZero() {
		fmt.Fprintf(os.Stderr, "Token expires: %s\n", cred.ExpiresAt.Format(time.RFC3339))
	}
	if cred.AccountID != "" {
		fmt.Fprintf(os.Stderr, "Account ID: %s\n", cred.AccountID)
	}
	fmt.Fprintf(os.Stderr, "Saved to: %s\n", configPath())
}

func printAuthHelp() {
	fmt.Fprint(os.Stderr, `Usage:
  launchdock auth                          Interactive credential manager
  launchdock auth list
  launchdock auth export [credential-id ...]
  launchdock auth import
  launchdock auth login claude [label]
  launchdock auth login openai
  launchdock auth push <ssh-target> [credential-id ...]
  launchdock auth remove <credential-id>

`)
}
