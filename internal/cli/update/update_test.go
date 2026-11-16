package update

import (
	"aulaglobal-mcp/internal/selfupdate"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type updateEnv struct {
	srv       *httptest.Server
	downloads atomic.Int32
}

func newUpdateEnv(t *testing.T, tag string) *updateEnv {
	t.Helper()
	e := &updateEnv{}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":"checksums.txt"}]}`, tag)
			return
		}
		e.downloads.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *updateEnv) run(current, stdin string, container bool, args ...string) (string, error) {
	u := selfupdate.New(current)
	u.APIBase, u.DownloadBase, u.HTTP = e.srv.URL, e.srv.URL, e.srv.Client()
	u.GOOS, u.GOARCH = "linux", "amd64"
	var out bytes.Buffer
	err := runUpdate(context.Background(), args, strings.NewReader(stdin), &out, u, container)
	return out.String(), err
}

func TestUpdateHelp(t *testing.T) {
	e := newUpdateEnv(t, "v0.2.0")
	for _, a := range []string{"--help", "-h"} {
		out, err := e.run("0.1.0", "", false, a)
		if err != nil || !strings.Contains(out, "Uso: aulaglobal-mcp update") {
			t.Fatalf("%s: %v %q", a, err, out)
		}
	}
}

func TestUpdateUnknownFlagAndArgs(t *testing.T) {
	e := newUpdateEnv(t, "v0.2.0")
	if _, err := e.run("0.1.0", "", false, "--bogus"); err == nil {
		t.Fatal("unknown flag accepted")
	}
	if _, err := e.run("0.1.0", "", false, "now"); err == nil {
		t.Fatal("positional argument accepted")
	}
}

func TestUpdateCheck(t *testing.T) {
	out, err := func() (string, error) { e := newUpdateEnv(t, "v0.2.0"); return e.run("0.1.0", "", false, "--check") }()
	if err != nil || !strings.Contains(out, "v0.1.0 → v0.2.0") || !strings.Contains(out, "!") {
		t.Fatalf("%v %q", err, out)
	}
	e := newUpdateEnv(t, "v0.1.0")
	if out, err = e.run("0.1.0", "", false, "--check"); err != nil || !strings.Contains(out, "✓ Ya tienes la última versión") {
		t.Fatalf("%v %q", err, out)
	}
	e = newUpdateEnv(t, "v0.1.0")
	if out, err = e.run("0.3.0", "", false); err != nil || !strings.Contains(out, "más reciente") || e.downloads.Load() != 0 {
		t.Fatalf("downgrade: %v %q", err, out)
	}
}

func TestUpdateDevVersion(t *testing.T) {
	for _, v := range []string{"dev", "0.0.0-SNAPSHOT-abc"} {
		e := newUpdateEnv(t, "v0.2.0")
		out, err := e.run(v, "s\n", false)
		if err == nil || !strings.Contains(err.Error(), "--force") || e.downloads.Load() != 0 {
			t.Fatalf("%s: %v %q", v, err, out)
		}
		out, err = e.run(v, "", false, "--check")
		if err != nil || !strings.Contains(out, "v0.2.0") {
			t.Fatalf("%s check: %v %q", v, err, out)
		}
	}
}

func TestUpdateContainerRefused(t *testing.T) {
	e := newUpdateEnv(t, "v0.2.0")
	_, err := e.run("0.1.0", "s\n", true, "--yes")
	if err == nil || !strings.Contains(err.Error(), "docker pull") || e.downloads.Load() != 0 {
		t.Fatalf("%v", err)
	}
}

func TestUpdateConfirmation(t *testing.T) {
	for stdin, wantInstall := range map[string]bool{"s\n": true, "\n": true, "SÍ\r\n": true, "n\n": false, "no\n": false, "x\n": false, "": false} {
		e := newUpdateEnv(t, "v0.2.0")
		out, err := e.run("0.1.0", stdin, false)
		if wantInstall {
			// The fake release lists no archive, so the install attempt fails: proof it was attempted.
			if err == nil || !strings.Contains(err.Error(), "no incluye") {
				t.Errorf("%q: expected install attempt, got %v %q", stdin, err, out)
			}
		} else if err != nil || !strings.Contains(out, "cancelada") {
			t.Errorf("%q: expected cancel, got %v %q", stdin, err, out)
		}
	}
	e := newUpdateEnv(t, "v0.2.0")
	out, err := e.run("0.1.0", "", false, "--yes")
	if err == nil || strings.Contains(out, "[S/n]") {
		t.Fatalf("--yes prompted or succeeded: %v %q", err, out)
	}
}
