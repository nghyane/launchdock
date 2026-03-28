//go:build windows

package main

const (
	ansiReset  = ""
	ansiBold   = ""
	ansiDim    = ""
	ansiCyan   = ""
	ansiGreen  = ""
	ansiYellow = ""
)

type checkboxItem struct {
	label    string
	desc     string
	checked  bool
	disabled bool
}

func isTerminal(fd int) bool {
	return false
}

func runCheckbox(title string, items []checkboxItem) []int {
	var out []int
	for i, item := range items {
		if item.checked && !item.disabled {
			out = append(out, i)
		}
	}
	return out
}

func runPicker(title string, options []string) int {
	if len(options) == 0 {
		return -1
	}
	return 0
}

func runConfirm(prompt string) bool {
	return true
}
