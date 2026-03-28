//go:build linux

package launchdock

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// --- ANSI helpers ---

const (
	ansiReset     = "\033[0m"
	ansiBold      = "\033[1m"
	ansiDim       = "\033[2m"
	ansiCyan      = "\033[36m"
	ansiGreen     = "\033[32m"
	ansiYellow    = "\033[33m"
	ansiClearLine = "\033[2K"
	ansiHideCur   = "\033[?25l"
	ansiShowCur   = "\033[?25h"
)

// --- Raw terminal (no external deps, Linux) ---

type termios syscall.Termios

const (
	ioctlTCGETS = 0x5401
	ioctlTCSETS = 0x5402
)

func tcGet(fd int) (*termios, error) {
	var t termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(ioctlTCGETS), uintptr(unsafe.Pointer(&t)))
	if errno != 0 {
		return nil, errno
	}
	return &t, nil
}

func tcSet(fd int, t *termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(ioctlTCSETS), uintptr(unsafe.Pointer(t)))
	if errno != 0 {
		return errno
	}
	return nil
}

func makeRaw(fd int) (*termios, error) {
	old, err := tcGet(fd)
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := tcSet(fd, &raw); err != nil {
		return nil, err
	}
	return old, nil
}

func restore(fd int, old *termios) {
	tcSet(fd, old)
}

// readKey reads a single keypress from stdin in raw mode.
func readKey(fd int) (byte, bool) {
	buf := make([]byte, 3)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return 0, false
	}
	if n == 3 && buf[0] == 0x1b && buf[1] == '[' {
		return buf[2], true
	}
	return buf[0], false
}

// --- Checkbox multi-select ---

type checkboxItem struct {
	label    string
	desc     string
	checked  bool
	disabled bool
}

func runCheckbox(title string, items []checkboxItem) []int {
	fd := int(os.Stdin.Fd())
	oldState, err := makeRaw(fd)
	if err != nil {
		var result []int
		for i, item := range items {
			if !item.disabled {
				result = append(result, i)
			}
		}
		return result
	}
	defer restore(fd, oldState)
	defer fmt.Print(ansiShowCur)
	fmt.Print(ansiHideCur)

	cursor := 0
	for cursor < len(items) && items[cursor].disabled {
		cursor++
	}
	if cursor >= len(items) {
		cursor = 0
	}

	render := func() {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("\r%s%s%s %s(↑↓ move, space toggle, enter confirm, a all, q quit)%s\r\n",
			ansiBold, title, ansiReset, ansiDim, ansiReset))
		for i, item := range items {
			sb.WriteString("\r")
			if i == cursor {
				sb.WriteString(fmt.Sprintf("%s>%s ", ansiCyan, ansiReset))
			} else {
				sb.WriteString("  ")
			}
			if item.disabled {
				sb.WriteString(fmt.Sprintf("%s[ ] %-14s %s%s", ansiDim, item.label, item.desc, ansiReset))
			} else if item.checked {
				sb.WriteString(fmt.Sprintf("%s[x]%s %-14s %s", ansiGreen, ansiReset, item.label, item.desc))
			} else {
				sb.WriteString(fmt.Sprintf("[ ] %-14s %s", item.label, item.desc))
			}
			sb.WriteString("\r\n")
		}
		fmt.Print(sb.String())
	}

	clearRender := func() {
		lines := len(items) + 1
		for i := 0; i < lines; i++ {
			fmt.Printf("\033[A" + ansiClearLine)
		}
	}

	render()

	for {
		key, isArrow := readKey(fd)
		if isArrow {
			switch key {
			case 'A':
				for {
					cursor--
					if cursor < 0 {
						cursor = len(items) - 1
					}
					if !items[cursor].disabled {
						break
					}
				}
			case 'B':
				for {
					cursor++
					if cursor >= len(items) {
						cursor = 0
					}
					if !items[cursor].disabled {
						break
					}
				}
			}
		} else {
			switch key {
			case ' ':
				if !items[cursor].disabled {
					items[cursor].checked = !items[cursor].checked
				}
			case 'a':
				allChecked := true
				for _, item := range items {
					if !item.disabled && !item.checked {
						allChecked = false
						break
					}
				}
				for i := range items {
					if !items[i].disabled {
						items[i].checked = !allChecked
					}
				}
			case 13:
				clearRender()
				var result []int
				for i, item := range items {
					if item.checked {
						result = append(result, i)
					}
				}
				return result
			case 'q', 3:
				clearRender()
				return nil
			case 'k':
				for {
					cursor--
					if cursor < 0 {
						cursor = len(items) - 1
					}
					if !items[cursor].disabled {
						break
					}
				}
			case 'j':
				for {
					cursor++
					if cursor >= len(items) {
						cursor = 0
					}
					if !items[cursor].disabled {
						break
					}
				}
			}
		}
		clearRender()
		render()
	}
}

func runPicker(title string, options []string) int {
	fd := int(os.Stdin.Fd())
	oldState, err := makeRaw(fd)
	if err != nil {
		return 0
	}
	defer restore(fd, oldState)
	defer fmt.Print(ansiShowCur)
	fmt.Print(ansiHideCur)

	cursor := 0

	render := func() {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("\r%s%s%s %s(↑↓ move, enter select, q quit)%s\r\n",
			ansiBold, title, ansiReset, ansiDim, ansiReset))
		for i, opt := range options {
			sb.WriteString("\r")
			if i == cursor {
				sb.WriteString(fmt.Sprintf("  %s> %s%s", ansiCyan, opt, ansiReset))
			} else {
				sb.WriteString(fmt.Sprintf("    %s", opt))
			}
			sb.WriteString("\r\n")
		}
		fmt.Print(sb.String())
	}

	clearRender := func() {
		lines := len(options) + 1
		for i := 0; i < lines; i++ {
			fmt.Printf("\033[A" + ansiClearLine)
		}
	}

	render()

	for {
		key, isArrow := readKey(fd)
		if isArrow {
			switch key {
			case 'A':
				cursor--
				if cursor < 0 {
					cursor = len(options) - 1
				}
			case 'B':
				cursor++
				if cursor >= len(options) {
					cursor = 0
				}
			}
		} else {
			switch key {
			case 13:
				clearRender()
				return cursor
			case 'q', 3:
				clearRender()
				return -1
			case 'k':
				cursor--
				if cursor < 0 {
					cursor = len(options) - 1
				}
			case 'j':
				cursor++
				if cursor >= len(options) {
					cursor = 0
				}
			}
		}
		clearRender()
		render()
	}
}

func runConfirm(prompt string) bool {
	fd := int(os.Stdin.Fd())
	oldState, err := makeRaw(fd)
	if err != nil {
		return true
	}
	defer restore(fd, oldState)

	fmt.Printf("\r%s%s%s %s[Y/n]%s ", ansiBold, prompt, ansiReset, ansiDim, ansiReset)

	for {
		key, isArrow := readKey(fd)
		if isArrow {
			continue
		}
		switch key {
		case 'y', 'Y', 13:
			fmt.Printf("%syes%s\r\n", ansiGreen, ansiReset)
			return true
		case 'n', 'N':
			fmt.Printf("%sno%s\r\n", ansiYellow, ansiReset)
			return false
		case 'q', 3:
			fmt.Printf("%sno%s\r\n", ansiYellow, ansiReset)
			return false
		}
	}
}

func isTerminal(fd int) bool {
	_, err := tcGet(fd)
	return err == nil
}
