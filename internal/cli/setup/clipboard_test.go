package setup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/aulaglobal"
	"aulaglobal-mcp/internal/termui"
	"aulaglobal-mcp/internal/testutil"
)

// fakeClipboard never invokes desktop commands.
type fakeClipboard struct {
	contents                   string
	copyErr, readErr, clearErr error
	copied                     string
	reads, clears              int
}

func (clipboard *fakeClipboard) Copy(text string) error {
	clipboard.copied = text
	return clipboard.copyErr
}

func (clipboard *fakeClipboard) Read() (string, error) {
	clipboard.reads++
	return clipboard.contents, clipboard.readErr
}

func (clipboard *fakeClipboard) Clear() error {
	clipboard.clears++
	if clipboard.clearErr == nil {
		clipboard.contents = ""
	}
	return clipboard.clearErr
}

func TestSetupClipboard(t *testing.T) {
	unavailable := errors.New("unavailable")
	tests := []struct {
		name      string
		clipboard fakeClipboard
		wantClear bool
	}{
		{name: "copy-ok"},
		{name: "copy-unavailable", clipboard: fakeClipboard{copyErr: unavailable}},
		{name: "clipboard-cleared", clipboard: fakeClipboard{contents: "abc123"}, wantClear: true},
		{name: "clipboard-changed", clipboard: fakeClipboard{contents: "something else"}},
		{name: "clipboard-read-unavailable", clipboard: fakeClipboard{contents: "abc123", readErr: unavailable}},
		{name: "clipboard-clear-failed", clipboard: fakeClipboard{contents: "abc123", clearErr: unavailable}},
		{name: "clipboard-whitespace", clipboard: fakeClipboard{contents: "abc123\n"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			moodle := moodleFake(t, "fake-token")
			defer moodle.Close()
			root := t.TempDir()
			var out strings.Builder
			ui := termui.UI{In: strings.NewReader(""), Out: &out, Password: func() ([]byte, error) { return []byte("abc123"), nil }}
			clipboard := &test.clipboard
			err := acquire(context.Background(), aulaglobal.New(moodle.URL, nil), filepath.Join(root, "secrets", "token"), filepath.Join(root, "data"), ui, clipboard)
			if err != nil {
				t.Fatal(err)
			}
			if clipboard.copied != cookieCommand || clipboard.reads != 1 {
				t.Fatal("unexpected clipboard operations")
			}
			wantAttempts := 0
			if test.wantClear || test.name == "clipboard-clear-failed" {
				wantAttempts = 1
			}
			if clipboard.clears != wantAttempts {
				t.Fatalf("clear calls: %d, want %d", clipboard.clears, wantAttempts)
			}
			got := out.String()
			if strings.Contains(got, "· Portapapeles vaciado.") != test.wantClear {
				t.Fatal(got)
			}
			if strings.Contains(got, "✓ Comando copiado") != (clipboard.copyErr == nil) {
				t.Fatal(got)
			}
			if strings.Contains(got, "abc123") || strings.Contains(got, "fake-token") {
				t.Fatal("secret in transcript")
			}
			testutil.Golden(t, "setup-"+test.name, stableTranscript(got, root))
		})
	}
}

func TestSetupClipboardExchangeFailed(t *testing.T) {
	root := t.TempDir()
	var out strings.Builder
	clipboard := &fakeClipboard{contents: "abc123"}
	ui := termui.UI{In: strings.NewReader(""), Out: &out, Password: func() ([]byte, error) { return []byte("abc123"), nil }}
	err := acquire(context.Background(), aulaglobal.New("http://127.0.0.1:1", nil), filepath.Join(root, "secrets", "token"), filepath.Join(root, "data"), ui, clipboard)
	if err == nil || clipboard.reads != 0 || clipboard.clears != 0 {
		t.Fatal("clipboard accessed after failed exchange")
	}
}
