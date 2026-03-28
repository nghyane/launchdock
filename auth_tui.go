//go:build darwin || linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func handleAuthInteractive() {
	views := LoadCredentialViews()
	fd := int(os.Stdin.Fd())
	oldState, err := makeRaw(fd)
	if err != nil {
		handleAuthList()
		return
	}
	defer restore(fd, oldState)
	defer fmt.Print(ansiShowCur)
	fmt.Print(ansiHideCur)

	cursor := 0
	for {
		if cursor >= len(views) && len(views) > 0 {
			cursor = len(views) - 1
		}
		renderAuthManager(views, cursor)
		key, isArrow := readKey(fd)
		if isArrow {
			switch key {
			case 'A':
				if len(views) > 0 {
					cursor--
					if cursor < 0 {
						cursor = len(views) - 1
					}
				}
			case 'B':
				if len(views) > 0 {
					cursor++
					if cursor >= len(views) {
						cursor = 0
					}
				}
			}
			continue
		}

		switch key {
		case 'q', 3:
			clearAuthScreen()
			return
		case 'k':
			if len(views) > 0 {
				cursor--
				if cursor < 0 {
					cursor = len(views) - 1
				}
			}
		case 'j':
			if len(views) > 0 {
				cursor++
				if cursor >= len(views) {
					cursor = 0
				}
			}
		case 'r':
			views = LoadCredentialViews()
		case 'a':
			oldState = suspendAuthRaw(fd, oldState, func() {
				provider := runPicker("Add credential:", []string{"Claude", "OpenAI", "Anthropic API key", "OpenAI API key"})
				switch provider {
				case 0:
					_, _ = RunOAuthFlow("Claude Account")
				case 1:
					_, _ = RunOpenAIOAuthFlow("OpenAI Account")
				case 2:
					addManagedAPIKeyInteractive("anthropic")
				case 3:
					addManagedAPIKeyInteractive("openai")
				}
			})
			views = LoadCredentialViews()
		case 'x':
			if len(views) == 0 || !views[cursor].Managed {
				continue
			}
			selected := views[cursor]
			oldState = suspendAuthRaw(fd, oldState, func() {
				if runConfirm("Remove managed credential " + selected.Label + "?") {
					if err := removeConfigCredential(selected.ID); err != nil {
						fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					}
				}
			})
			views = LoadCredentialViews()
		case 'e':
			if len(views) == 0 || !views[cursor].Managed {
				continue
			}
			_ = toggleConfigCredentialDisabled(views[cursor].ID)
			views = LoadCredentialViews()
		}
	}
}

func suspendAuthRaw(fd int, oldState *termios, fn func()) *termios {
	restore(fd, oldState)
	fmt.Print(ansiShowCur)
	fn()
	newState, err := makeRaw(fd)
	if err == nil {
		fmt.Print(ansiHideCur)
		return newState
	}
	return oldState
}

func renderAuthManager(views []CredentialView, cursor int) {
	clearAuthScreen()
	fmt.Printf("%sLaunchdock Auth%s %s(j/k or ↑↓ move, a add, e enable/disable, x remove, r refresh, q quit)%s\n\n", ansiBold, ansiReset, ansiDim, ansiReset)

	if len(views) == 0 {
		fmt.Println("  No credentials found.")
		fmt.Println("  Press 'a' to add Claude or OpenAI login.")
		return
	}

	for i, v := range views {
		prefix := "  "
		if i == cursor {
			prefix = fmt.Sprintf("%s❯%s ", ansiCyan, ansiReset)
		}
		fmt.Printf("%s%-24s %s%-9s%s %s\n", prefix, truncate(authDisplayName(v), 24), authStatusColor(v), authStatusLabel(v), ansiReset, truncate(authRowSummary(v), 48))
	}

	v := views[cursor]
	fmt.Printf("\n%sDetails%s\n", ansiBold, ansiReset)
	fmt.Printf("  Label:    %s\n", v.Label)
	if v.Email != "" {
		fmt.Printf("  Email:    %s\n", v.Email)
	}
	fmt.Printf("  Provider: %s\n", authProviderLabel(v.Provider))
	fmt.Printf("  Type:     %s\n", v.AuthType)
	fmt.Printf("  Source:   %s\n", v.Source)
	if v.Managed {
		fmt.Printf("  ID:       %s\n", v.ID)
	}
	fmt.Printf("  Status:   %s%s%s\n", authStatusColor(v), authStatusLabel(v), ansiReset)
	if v.StatusMessage != "" {
		fmt.Printf("  Note:     %s\n", truncate(v.StatusMessage, 80))
	}
	if v.AccountID != "" {
		fmt.Printf("  Account:  %s\n", v.AccountID)
	}
	if len(v.CompatibleTools) > 0 {
		fmt.Printf("  Tools:    %s\n", strings.Join(v.CompatibleTools, ", "))
	}
	if v.Managed {
		fmt.Printf("  Actions:  e enable/disable, x remove\n")
	} else {
		fmt.Printf("  Actions:  read-only (%s source)\n", v.SourceKind)
	}
}

func clearAuthScreen() {
	fmt.Print("\033[H\033[2J")
}

func authStatusColor(v CredentialView) string {
	switch authStatusLabel(v) {
	case "ready":
		return ansiGreen
	case "disabled":
		return ansiDim
	default:
		return ansiYellow
	}
}

func authRowSummary(v CredentialView) string {
	if v.Managed {
		return "managed by launchdock"
	}
	return authSourceLabel(v)
}

func addManagedAPIKeyInteractive(provider string) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Enter %s API key: ", providerDisplayName(provider))
	key, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "Read failed: %v\n", err)
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		fmt.Fprintln(os.Stderr, "API key is empty")
		return
	}
	label := providerDisplayName(provider) + " API key"
	if err := saveAPIKeyToConfig(provider, label, key); err != nil {
		fmt.Fprintf(os.Stderr, "Save failed: %v\n", err)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}
