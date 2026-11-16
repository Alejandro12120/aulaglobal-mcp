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

type fakeRelease struct {
	srv       *httptest.Server
	downloads atomic.Int32
}

func newFakeRelease(t *testing.T, tag string) *fakeRelease {
	t.Helper()
	release := &fakeRelease{}
	release.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":"checksums.txt"}]}`, tag)
			return
		}
		release.downloads.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(release.srv.Close)
	return release
}

func (release *fakeRelease) run(current, stdin string, container bool, args ...string) (string, error) {
	updater := selfupdate.New(current)
	updater.APIBase, updater.DownloadBase, updater.HTTP = release.srv.URL, release.srv.URL, release.srv.Client()
	updater.GOOS, updater.GOARCH = "linux", "amd64"
	var out bytes.Buffer
	err := runUpdate(context.Background(), args, strings.NewReader(stdin), &out, updater, container)
	return out.String(), err
}

func TestUpdateHelp(t *testing.T) {
	release := newFakeRelease(t, "v0.2.0")
	for _, arg := range []string{"--help", "-h"} {
		out, err := release.run("0.1.0", "", false, arg)
		if err != nil || !strings.Contains(out, "Uso: aulaglobal-mcp update") {
			t.Fatalf("%s: %v %q", arg, err, out)
		}
	}
}

func TestUpdateUnknownFlagAndArgs(t *testing.T) {
	release := newFakeRelease(t, "v0.2.0")
	if _, err := release.run("0.1.0", "", false, "--bogus"); err == nil {
		t.Fatal("unknown flag accepted")
	}
	if _, err := release.run("0.1.0", "", false, "now"); err == nil {
		t.Fatal("positional argument accepted")
	}
}

func TestUpdateCheck(t *testing.T) {
	out, err := func() (string, error) {
		release := newFakeRelease(t, "v0.2.0")
		return release.run("0.1.0", "", false, "--check")
	}()
	if err != nil || !strings.Contains(out, "v0.1.0 → v0.2.0") || !strings.Contains(out, "!") {
		t.Fatalf("%v %q", err, out)
	}
	release := newFakeRelease(t, "v0.1.0")
	if out, err = release.run("0.1.0", "", false, "--check"); err != nil || !strings.Contains(out, "✓ Ya tienes la última versión") {
		t.Fatalf("%v %q", err, out)
	}
	release = newFakeRelease(t, "v0.1.0")
	if out, err = release.run("0.3.0", "", false); err != nil || !strings.Contains(out, "más reciente") || release.downloads.Load() != 0 {
		t.Fatalf("downgrade: %v %q", err, out)
	}
}

func TestUpdateDevVersion(t *testing.T) {
	for _, v := range []string{"dev", "0.0.0-SNAPSHOT-abc"} {
		release := newFakeRelease(t, "v0.2.0")
		out, err := release.run(v, "s\n", false)
		if err == nil || !strings.Contains(err.Error(), "--force") || release.downloads.Load() != 0 {
			t.Fatalf("%s: %v %q", v, err, out)
		}
		out, err = release.run(v, "", false, "--check")
		if err != nil || !strings.Contains(out, "v0.2.0") {
			t.Fatalf("%s check: %v %q", v, err, out)
		}
	}
}

func TestUpdateContainerRefused(t *testing.T) {
	release := newFakeRelease(t, "v0.2.0")
	_, err := release.run("0.1.0", "s\n", true, "--yes")
	if err == nil || !strings.Contains(err.Error(), "docker pull") || release.downloads.Load() != 0 {
		t.Fatalf("%v", err)
	}
}

func TestUpdateConfirmation(t *testing.T) {
	for stdin, wantInstall := range map[string]bool{"s\n": true, "\n": true, "SÍ\r\n": true, "n\n": false, "no\n": false, "x\n": false, "": false} {
		release := newFakeRelease(t, "v0.2.0")
		out, err := release.run("0.1.0", stdin, false)
		if wantInstall {
			// The fake release lists no archive, so the install attempt fails: proof it was attempted.
			if err == nil || !strings.Contains(err.Error(), "no incluye") {
				t.Errorf("%q: expected install attempt, got %v %q", stdin, err, out)
			}
		} else if err != nil || !strings.Contains(out, "cancelada") {
			t.Errorf("%q: expected cancel, got %v %q", stdin, err, out)
		}
	}
	release := newFakeRelease(t, "v0.2.0")
	out, err := release.run("0.1.0", "", false, "--yes")
	if err == nil || strings.Contains(out, "[S/n]") {
		t.Fatalf("--yes prompted or succeeded: %v %q", err, out)
	}
}
