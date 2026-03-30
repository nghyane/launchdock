//go:build darwin || linux

package launchdock

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	authpkg "github.com/nghiahoang/launchdock/internal/auth"
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
				provider := runPicker("Add credential:", []string{"Claude", "OpenAI"})
				switch provider {
				case 0:
					_, _ = authpkg.RunOAuthFlow("Claude Account")
				case 1:
					_, _ = authpkg.RunOpenAIOAuthFlow("OpenAI Account")
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
					if err := authpkg.RemoveConfigCredential(selected.ID); err != nil {
						fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					}
				}
			})
			views = LoadCredentialViews()
		case 'e':
			if len(views) == 0 || !views[cursor].Managed {
				continue
			}
			_ = authpkg.ToggleConfigCredentialDisabled(views[cursor].ID)
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
	width := authTerminalWidth()
	height := authTerminalHeight()
	header := "Launchdock Auth"
	help := "(j/k or ↑↓ move, a add, e enable/disable, x remove, r refresh, q quit)"
	availableHelp := width - visibleWidth(header) - 1
	if availableHelp < 0 {
		availableHelp = 0
	}
	headerLine := header
	if availableHelp > 0 {
		headerLine += " " + truncate(help, availableHelp)
	}
	fmt.Printf("%s%s%s\n\n", ansiBold, truncate(headerLine, width), ansiReset)

	if len(views) == 0 {
		fmt.Println("  No credentials found.")
		fmt.Println("  Press 'a' to add Claude or OpenAI login.")
		return
	}

	nameWidth := 24
	if width < 40 {
		nameWidth = 16
	}
	statusWidth := 9
	rowSummaryWidth := width - visibleWidth("❯ ") - nameWidth - 1 - statusWidth - 1
	if rowSummaryWidth < 0 {
		rowSummaryWidth = 0
	}
	listHeight := height - 14
	if listHeight < 3 {
		listHeight = 3
	}
	start, end := authVisibleRange(len(views), cursor, listHeight)
	if start > 0 {
		fmt.Printf("  %s↑ %d more%s\n", ansiDim, start, ansiReset)
	}
	for i := start; i < end; i++ {
		v := views[i]
		prefix := "  "
		if i == cursor {
			prefix = fmt.Sprintf("%s❯%s ", ansiCyan, ansiReset)
		}
		fmt.Printf("%s%-*s %s%-*s%s %s\n", prefix, nameWidth, padRight(truncate(authDisplayName(v), nameWidth), nameWidth), authStatusColor(v), statusWidth, padRight(truncate(authStatusLabel(v), statusWidth), statusWidth), ansiReset, truncate(authRowSummary(v), rowSummaryWidth))
	}
	if end < len(views) {
		fmt.Printf("  %s↓ %d more%s\n", ansiDim, len(views)-end, ansiReset)
	}

	v := views[cursor]
	fmt.Printf("\n%sDetails%s\n", ansiBold, ansiReset)
	detailWidth := width - visibleWidth("  Label:    ")
	if detailWidth < 0 {
		detailWidth = 0
	}
	printDetailLine("Label", v.Label, detailWidth)
	if v.Email != "" {
		printDetailLine("Email", v.Email, detailWidth)
	}
	printDetailLine("Provider", authProviderLabel(v.Provider), detailWidth)
	printDetailLine("Type", string(v.AuthType), detailWidth)
	printDetailLine("Source", v.Source, detailWidth)
	if v.Managed {
		printDetailLine("ID", v.ID, detailWidth)
	}
	printDetailLineStyled("Status", authStatusColor(v), authStatusLabel(v), detailWidth)
	if v.StatusMessage != "" {
		printDetailLine("Note", v.StatusMessage, detailWidth)
	}
	if v.AccountID != "" {
		printDetailLine("Account", v.AccountID, detailWidth)
	}
	if len(v.CompatibleTools) > 0 {
		printDetailLine("Tools", strings.Join(v.CompatibleTools, ", "), detailWidth)
	}
	if v.Managed {
		printDetailLine("Actions", "e enable/disable, x remove", detailWidth)
	} else {
		printDetailLine("Actions", fmt.Sprintf("read-only (%s source)", v.SourceKind), detailWidth)
	}
}

func printDetailLine(label string, value string, width int) {
	printDetailLineStyled(label, "", value, width)
}

func printDetailLineStyled(label string, style string, value string, width int) {
	prefix := fmt.Sprintf("  %-8s ", label+":")
	available := width - visibleWidth(prefix)
	if available < 0 {
		available = 0
	}
	trimmed := truncate(value, available)
	if style == "" {
		fmt.Printf("%s%s\n", prefix, trimmed)
		return
	}
	fmt.Printf("%s%s%s%s\n", prefix, style, trimmed, ansiReset)
}

func authTerminalWidth() int {
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 0 {
			return n
		}
	}
	if width, _, err := getTerminalSize(int(os.Stdout.Fd())); err == nil && width > 0 {
		return width
	}
	return 100
}

func authTerminalHeight() int {
	if lines := os.Getenv("LINES"); lines != "" {
		if n, err := strconv.Atoi(lines); err == nil && n > 0 {
			return n
		}
	}
	if _, height, err := getTerminalSize(int(os.Stdout.Fd())); err == nil && height > 0 {
		return height
	}
	return 24
}

func authVisibleRange(total int, cursor int, maxRows int) (start int, end int) {
	if total <= 0 || maxRows <= 0 || total <= maxRows {
		return 0, total
	}
	start = cursor - maxRows/2
	if start < 0 {
		start = 0
	}
	end = start + maxRows
	if end > total {
		end = total
		start = end - maxRows
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func visibleWidth(s string) int {
	return len([]rune(s))
}

func padRight(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(r))
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
	if authStatusLabel(v) == "relogin" {
		return "login again or remove this account"
	}
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
	if err := authpkg.SaveAPIKeyToConfig(provider, label, key); err != nil {
		fmt.Fprintf(os.Stderr, "Save failed: %v\n", err)
	}
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 0 {
		return ""
	}
	if max == 1 {
		return string(runes[:1])
	}
	return string(runes[:max-1]) + "…"
}
