package clipboard

import (
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSelectBackend(t *testing.T) {
	tests := []struct {
		name, platform, wayland, display       string
		binaries                               []string
		copyCommand, readCommand, clearCommand command
	}{
		{name: "mac", platform: "darwin", binaries: []string{"pbcopy", "pbpaste"}, copyCommand: command{"/bin/pbcopy", nil}, readCommand: command{"/bin/pbpaste", nil}, clearCommand: command{"/bin/pbcopy", nil}},
		{name: "windows", platform: "windows", binaries: []string{"clip.exe", "powershell"}, copyCommand: command{"/bin/clip.exe", nil}},
		{name: "wayland", platform: "linux", wayland: "wayland-0", display: ":0", binaries: []string{"wl-copy", "wl-paste", "xclip"}, copyCommand: command{"/bin/wl-copy", nil}, readCommand: command{"/bin/wl-paste", []string{"--no-newline"}}, clearCommand: command{"/bin/wl-copy", []string{"--clear"}}},
		{name: "xclip", platform: "linux", display: ":0", binaries: []string{"xclip", "xsel"}, copyCommand: command{"/bin/xclip", []string{"-selection", "clipboard"}}, readCommand: command{"/bin/xclip", []string{"-selection", "clipboard", "-o"}}, clearCommand: command{"/bin/xclip", []string{"-selection", "clipboard"}}},
		{name: "xsel", platform: "linux", display: ":0", binaries: []string{"xsel"}, copyCommand: command{"/bin/xsel", []string{"--clipboard", "--input"}}, readCommand: command{"/bin/xsel", []string{"--clipboard", "--output"}}, clearCommand: command{"/bin/xsel", []string{"--clipboard", "--input"}}},
		{name: "headless", platform: "linux", binaries: []string{"xclip", "xsel"}},
		{name: "missing binaries", platform: "linux", display: ":0"},
		{name: "wayland no X fallback", platform: "linux", wayland: "wayland-0", display: ":0", binaries: []string{"xclip"}},
		{name: "unsupported", platform: "freebsd", display: ":0", binaries: []string{"xclip"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getenv := func(name string) string {
				if name == "WAYLAND_DISPLAY" {
					return test.wayland
				}
				if name == "DISPLAY" {
					return test.display
				}
				t.Fatalf("unexpected environment lookup: %s", name)
				return ""
			}
			lookPath := func(name string) (string, error) {
				for _, binary := range test.binaries {
					if name == binary {
						return "/bin/" + name, nil
					}
				}
				return "", errors.New("not found")
			}
			got := selectBackend(test.platform, getenv, lookPath)
			want := Clipboard{test.copyCommand, test.readCommand, test.clearCommand}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, want %#v", got, want)
			}
		})
	}
}

func TestUnavailable(t *testing.T) {
	clipboard := Clipboard{}
	if clipboard.Copy("test") == nil {
		t.Fatal("copy succeeded")
	}
	if _, err := clipboard.Read(); err == nil {
		t.Fatal("read succeeded")
	}
	if clipboard.Clear() == nil {
		t.Fatal("clear succeeded")
	}
}

func TestCommandHelper(t *testing.T) {
	switch os.Getenv("CLIPBOARD_TEST_HELPER") {
	case "echo":
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(1)
		}
		os.Stdout.Write(contents)
		os.Exit(0)
	case "fail":
		os.Stderr.WriteString("private clipboard content")
		os.Exit(1)
	case "timeout":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
}

func TestRun(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	selected := command{path: executable, args: []string{"-test.run=^TestCommandHelper$"}}
	t.Run("stdin and exact read", func(t *testing.T) {
		t.Setenv("CLIPBOARD_TEST_HELPER", "echo")
		want := "clipboard value\n"
		got, err := run(selected, want, true)
		if err != nil || got != want {
			t.Fatalf("output mismatch: %q, %v", got, err)
		}
	})
	t.Run("redacted failure", func(t *testing.T) {
		t.Setenv("CLIPBOARD_TEST_HELPER", "fail")
		got, err := run(selected, "private clipboard content", true)
		if err == nil || got != "" || strings.Contains(err.Error(), "private") {
			t.Fatal("unredacted failure")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		t.Setenv("CLIPBOARD_TEST_HELPER", "timeout")
		started := time.Now()
		if _, err := run(selected, "", false); err == nil {
			t.Fatal("command did not time out")
		}
		if time.Since(started) > 4*time.Second {
			t.Fatal("timeout exceeded budget")
		}
	})
}
