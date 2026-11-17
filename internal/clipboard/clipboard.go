// Package clipboard accesses the system clipboard through installed commands.
package clipboard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var errUnavailable = errors.New("clipboard unavailable")

type command struct {
	path string
	args []string
}

// Clipboard uses the commands available when New is called.
type Clipboard struct {
	copyCommand  command
	readCommand  command
	clearCommand command
}

// New selects a clipboard backend for this desktop session.
func New() Clipboard {
	return selectBackend(runtime.GOOS, os.Getenv, exec.LookPath)
}

func selectBackend(platform string, getenv func(string) string, lookPath func(string) (string, error)) Clipboard {
	find := func(name string, args ...string) command {
		path, err := lookPath(name)
		if err != nil {
			return command{}
		}
		return command{path: path, args: args}
	}
	switch platform {
	case "darwin":
		write := find("pbcopy")
		return Clipboard{write, find("pbpaste"), write}
	case "windows":
		// clip.exe writes reliably via stdin. Reading and clearing are unsupported.
		return Clipboard{copyCommand: find("clip.exe")}
	case "linux":
		if getenv("WAYLAND_DISPLAY") != "" {
			return Clipboard{find("wl-copy"), find("wl-paste", "--no-newline"), find("wl-copy", "--clear")}
		}
		if getenv("DISPLAY") != "" {
			if write := find("xclip", "-selection", "clipboard"); write.path != "" {
				return Clipboard{write, find("xclip", "-selection", "clipboard", "-o"), write}
			}
			write := find("xsel", "--clipboard", "--input")
			return Clipboard{write, find("xsel", "--clipboard", "--output"), write}
		}
	}
	return Clipboard{}
}

// Copy writes text to the clipboard without passing it in command arguments.
func (clipboard Clipboard) Copy(text string) error {
	_, err := run(clipboard.copyCommand, text, false)
	return err
}

// Read returns clipboard text exactly, without trimming whitespace.
func (clipboard Clipboard) Read() (string, error) {
	return run(clipboard.readCommand, "", true)
}

// Clear empties the clipboard. Windows does not support this operation.
func (clipboard Clipboard) Clear() error {
	_, err := run(clipboard.clearCommand, "", false)
	return err
}

func run(selected command, input string, capture bool) (string, error) {
	if selected.path == "" {
		return "", errUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, selected.path, selected.args...)
	process.Stdin = strings.NewReader(input)
	// Nil outputs go to the null device. Selection owners can fork and keep
	// stdout/stderr open without making Wait wait for inherited output pipes.
	var output bytes.Buffer
	if capture {
		process.Stdout = &output
	}
	process.WaitDelay = 100 * time.Millisecond
	if err := process.Run(); err != nil {
		// Command output and errors must never expose clipboard contents.
		return "", errUnavailable
	}
	return output.String(), nil
}
