package setup

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/aulaglobal"
	"aulaglobal-mcp/internal/termui"
	"aulaglobal-mcp/internal/testutil"
)

func TestSetupOffline(t *testing.T) {
	var site string
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "launch.php") {
			c, err := r.Cookie("MoodleSessionag")
			if err != nil || c.Value != "abc123" {
				t.Error("cookie incorrect")
			}
			hash := md5.Sum([]byte(site + r.URL.Query().Get("passport")))
			w.Header().Set("Location", "moodlemobile://token="+base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(hash[:])+":::fake-token:::private")))
			w.WriteHeader(303)
			return
		}
		r.ParseForm()
		if r.Form.Get("wstoken") != "fake-token" {
			t.Error("token incorrect")
		}
		fmt.Fprint(w, `{"fullname":"Ana","userid":1}`)
	}))
	defer moodle.Close()
	site = moodle.URL
	root := t.TempDir()
	secrets := filepath.Join(root, "secrets")
	os.Mkdir(secrets, 0700)
	token := filepath.Join(secrets, "token")
	for _, name := range []string{"token.meta.json", "token.private", ".token-123", "token.notes"} {
		os.WriteFile(filepath.Join(secrets, name), []byte("old"), 0600)
	}
	var out strings.Builder
	attempt := 0
	ui := termui.UI{In: strings.NewReader(""), Out: &out, Password: func() ([]byte, error) {
		attempt++
		if attempt == 1 {
			return []byte(""), nil
		}
		return []byte(` "MoodleSessionag=abc123" `), nil
	}}
	err := acquire(context.Background(), aulaglobal.New(site, nil), token, filepath.Join(root, "data"), ui, &fakeClipboard{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Token válido. Hola, Ana") || strings.Contains(out.String(), "abc123") {
		t.Fatal(out.String())
	}
	for n := 1; n <= 3; n++ {
		if !strings.Contains(out.String(), fmt.Sprintf("\n── Paso %d de 4", n)) {
			t.Fatal(out.String())
		}
	}
	if strings.Contains(out.String(), "\033") {
		t.Fatal("ANSI in redirected output")
	}
	t.Log("\n" + out.String())
	if _, err = os.Stat(filepath.Join(secrets, ".token-123")); !os.IsNotExist(err) {
		t.Fatal("temporary retained")
	}
	if b, _ := os.ReadFile(filepath.Join(secrets, "token.notes")); string(b) != "old" {
		t.Fatal("unrelated file changed")
	}
	var decline strings.Builder
	ui = termui.UI{In: strings.NewReader("\n"), Out: &decline, Password: func() ([]byte, error) { t.Fatal("password read after decline"); return nil, nil }}
	err = acquire(context.Background(), aulaglobal.New(site, nil), token, root, ui, &fakeClipboard{})
	if err != nil || !strings.Contains(decline.String(), "Obtenido el:") {
		t.Fatal(err, decline.String())
	}
}
func TestNormalize(t *testing.T) {
	for _, s := range []string{" abc123 ", `"MoodleSessionag=abc123"`, "'abc123'", "MoodleSessionag=\"abc123\""} {
		if normalizeCookie(s) != "abc123" {
			t.Fatal(s)
		}
	}
}

func TestThreeEmptyAttempts(t *testing.T) {
	var out strings.Builder
	calls := 0
	ui := termui.UI{In: strings.NewReader(""), Out: &out, Password: func() ([]byte, error) { calls++; return nil, nil }}
	root := t.TempDir()
	err := acquire(context.Background(), aulaglobal.New("http://127.0.0.1:1", nil), filepath.Join(root, "secrets", "token"), filepath.Join(root, "data"), ui, &fakeClipboard{})
	if err == nil || calls != 3 {
		t.Fatal(err, calls)
	}
}

func TestSetupStepFormatting(t *testing.T) {
	var out strings.Builder
	ui := termui.UI{In: strings.NewReader(""), Out: &out}
	for i, title := range []string{"Carpetas", "Tu cuenta de AulaGlobal", "Comprobación", "Conectar con tus asistentes"} {
		step(ui, i+1, title)
		fmt.Fprintln(ui.Out, "Una idea por línea.")
	}
	for i := 1; i <= 4; i++ {
		if !strings.Contains(out.String(), fmt.Sprintf("\n── Paso %d de 4", i)) {
			t.Fatal(out.String())
		}
	}
	if strings.Contains(out.String(), "\033") {
		t.Fatal("ANSI without TTY")
	}
	if strings.Count(out.String(), "──\n\n") != 4 {
		t.Fatal("missing separators")
	}
}

// stableTranscript hides temporary paths so goldens do not depend on the host.
func stableTranscript(out string, root string) string {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.ReplaceAll(out, root, "<tmp>")
	lines := strings.Split(out, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
		if strings.HasPrefix(lines[i], "  <tmp>") {
			lines[i] = strings.ReplaceAll(lines[i], string(filepath.Separator), "/")
		}
	}
	return strings.Join(lines, "\n")
}

// moodleFake accepts only the given tokens on site_info; "invalid" answers like
// Moodle does for an expired token.
func moodleFake(t *testing.T, valid string) *httptest.Server {
	t.Helper()
	var site string
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "launch.php") {
			hash := md5.Sum([]byte(site + r.URL.Query().Get("passport")))
			w.Header().Set("Location", "moodlemobile://token="+base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(hash[:])+":::"+valid)))
			w.WriteHeader(303)
			return
		}
		r.ParseForm()
		if r.Form.Get("wstoken") != valid {
			fmt.Fprint(w, `{"exception":"moodle_exception","errorcode":"invalidtoken","message":"Invalid token"}`)
			return
		}
		fmt.Fprint(w, `{"fullname":"Ana","userid":1}`)
	}))
	site = moodle.URL
	return moodle
}

func TestSetupExpiredToken(t *testing.T) {
	moodle := moodleFake(t, "new-token")
	defer moodle.Close()
	root := t.TempDir()
	secrets := filepath.Join(root, "secrets")
	os.Mkdir(secrets, 0700)
	token := filepath.Join(secrets, "token")
	os.WriteFile(token, []byte("old-token"), 0600)
	os.WriteFile(token+".meta.json", []byte(`{"obtained_at":"2026-01-02T03:04:05Z"}`), 0600)
	var out strings.Builder
	ui := termui.UI{In: strings.NewReader(""), Out: &out, Password: func() ([]byte, error) { return []byte("abc123"), nil }}
	err := acquire(context.Background(), aulaglobal.New(moodle.URL, nil), token, filepath.Join(root, "data"), ui, &fakeClipboard{})
	if err != nil {
		t.Fatal(err, out.String())
	}
	got := out.String()
	if strings.Contains(got, "¿Quieres reemplazarlo?") || strings.Contains(got, "Conservamos") || !strings.Contains(got, "Tu token ha caducado o ya no es válido. Vamos a renovarlo.") || !strings.Contains(got, "✓ Token válido. Hola, Ana") {
		t.Fatal(got)
	}
	if b, _ := os.ReadFile(token); strings.TrimSpace(string(b)) != "new-token" {
		t.Fatal("expired token not replaced")
	}
	testutil.Golden(t, "setup-expired-token", stableTranscript(got, root))
}

func TestSetupValidTokenKeepsQuestion(t *testing.T) {
	moodle := moodleFake(t, "good-token")
	defer moodle.Close()
	root := t.TempDir()
	token := filepath.Join(root, "secrets", "token")
	os.MkdirAll(filepath.Dir(token), 0700)
	os.WriteFile(token, []byte("good-token"), 0600)
	var out strings.Builder
	ui := termui.UI{In: strings.NewReader("\n"), Out: &out, Password: func() ([]byte, error) { t.Fatal("cookie requested"); return nil, nil }}
	err := acquire(context.Background(), aulaglobal.New(moodle.URL, nil), token, filepath.Join(root, "data"), ui, &fakeClipboard{})
	got := out.String()
	if err != nil || !strings.Contains(got, "¿Quieres reemplazarlo? [s/N]") || !strings.Contains(got, "✓ Conservamos tu token.") || !strings.Contains(got, "✓ Token válido. Hola, Ana") || strings.Contains(got, "caducado") {
		t.Fatal(err, got)
	}
}

func TestSetupUnverifiableToken(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "secrets", "token")
	os.MkdirAll(filepath.Dir(token), 0700)
	os.WriteFile(token, []byte("some-token"), 0600)
	os.WriteFile(token+".meta.json", []byte(`{"obtained_at":"2026-01-02T03:04:05Z"}`), 0600)
	var out strings.Builder
	ui := termui.UI{In: strings.NewReader("\n"), Out: &out, Password: func() ([]byte, error) { t.Fatal("cookie requested"); return nil, nil }}
	err := acquire(context.Background(), aulaglobal.New("http://127.0.0.1:1", nil), token, filepath.Join(root, "data"), ui, &fakeClipboard{})
	got := out.String()
	if err != nil || strings.Contains(got, "✓") {
		t.Fatal(err, got)
	}
	if strings.Contains(got, "caducado") || !strings.Contains(got, "¿Quieres reemplazarlo? [s/N]") {
		t.Fatal(got)
	}
	testutil.Golden(t, "setup-unverifiable-token", stableTranscript(got, root))
}
