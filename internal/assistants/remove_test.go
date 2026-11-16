package assistants_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/assistants/assistantstest"
	"aulaglobal-mcp/internal/testutil"
	"aulaglobal-mcp/internal/userpaths"
)

type removeCase struct {
	name    string
	a       assistants.Assistant
	path    func(assistants.Env) string
	before  string // %EXE% is replaced by the command
	after   string
	wantEnv map[string]string
}

func removeCases(t *testing.T) []removeCase {
	return []removeCase{
		{
			name: "claude-desktop keeps order, indentation and big numbers", a: assistantstest.ByID(t, "claude-desktop"),
			path:    func(e assistants.Env) string { return userpaths.Claude(e.GOOS, e.Home, e.ConfigDir) },
			before:  "{\n  \"zeta\": 1234567890123456789,\n  \"mcpServers\": {\n    \"keep\": {\n      \"command\": \"keep\"\n    },\n    \"aulaglobal\": {\n      \"command\": \"%EXE%\",\n      \"args\": [\"--transport\", \"stdio\"],\n      \"env\": {\"AULAGLOBAL_DOWNLOAD_DIR\": \"/notes\"}\n    },\n    \"last\": {\"command\": \"last\"}\n  },\n  \"alpha\": true\n}\n",
			after:   "{\n  \"zeta\": 1234567890123456789,\n  \"mcpServers\": {\n    \"keep\": {\n      \"command\": \"keep\"\n    },\n    \"last\": {\"command\": \"last\"}\n  },\n  \"alpha\": true\n}\n",
			wantEnv: map[string]string{"AULAGLOBAL_DOWNLOAD_DIR": "/notes"},
		},
		{
			name: "claude-desktop only server", a: assistantstest.ByID(t, "claude-desktop"),
			path:   func(e assistants.Env) string { return userpaths.Claude(e.GOOS, e.Home, e.ConfigDir) },
			before: "{\n  \"mcpServers\": {\n    \"aulaglobal\": {\"command\": \"%EXE%\"}\n  }\n}\n",
			after:  "{\n  \"mcpServers\": {}\n}\n",
		},
		{
			name: "antigravity last member", a: assistantstest.ByID(t, "antigravity"),
			path:   func(e assistants.Env) string { return userpaths.Antigravity(e.Home) },
			before: "{\"mcpServers\":{\"keep\":{\"command\":\"k\"},\"aulaglobal\":{\"command\":\"%EXE%\",\"disabled\":false}}}",
			after:  "{\"mcpServers\":{\"keep\":{\"command\":\"k\"}}}",
		},
		{
			name: "antigravity first member with tabs", a: assistantstest.ByID(t, "antigravity"),
			path:   func(e assistants.Env) string { return userpaths.Antigravity(e.Home) },
			before: "{\n\t\"mcpServers\": {\n\t\t\"aulaglobal\": {\"command\": \"%EXE%\"},\n\t\t\"keep\": {\"command\": \"k\"}\n\t}\n}",
			after:  "{\n\t\"mcpServers\": {\n\t\t\"keep\": {\"command\": \"k\"}\n\t}\n}",
		},
		{
			name: "codex table with sub-table and comments", a: assistantstest.ByID(t, "codex"),
			path:    func(e assistants.Env) string { return filepath.Join(e.Home, ".codex", "config.toml") },
			before:  "# my settings\nmodel = \"x\"\n\n[mcp_servers.other]\ncommand = \"keep\" # note\n\n[mcp_servers.aulaglobal]\ncommand = '%EXE%'\nargs = ['--transport', 'stdio']\n\n[mcp_servers.aulaglobal.env]\nAULAGLOBAL_TOKEN_FILE = '/tok'\n\n[projects.\"/p\"]\ntrust = \"trusted\"\n",
			after:   "# my settings\nmodel = \"x\"\n\n[mcp_servers.other]\ncommand = \"keep\" # note\n\n[projects.\"/p\"]\ntrust = \"trusted\"\n",
			wantEnv: map[string]string{"AULAGLOBAL_TOKEN_FILE": "/tok"},
		},
		{
			name: "codex block appended by setup is removed with its blank line", a: assistantstest.ByID(t, "codex"),
			path:   func(e assistants.Env) string { return filepath.Join(e.Home, ".codex", "config.toml") },
			before: "model = \"x\"\n\n[mcp_servers.aulaglobal]\ncommand = '%EXE%'\n",
			after:  "model = \"x\"\n",
		},
		{
			name: "opencode V2", a: assistantstest.ByID(t, "opencode"),
			path:    func(e assistants.Env) string { return filepath.Join(userpaths.OpenCode(e.Home, ""), "opencode.json") },
			before:  "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {\n    \"servers\": {\n      \"aulaglobal\": {\n        \"type\": \"local\",\n        \"command\": [\"%EXE%\", \"--transport\", \"stdio\"],\n        \"environment\": {\"AULAGLOBAL_DOWNLOAD_DIR\": \"/n\"}\n      },\n      \"keep\": {\"type\": \"local\", \"command\": [\"k\"]}\n    }\n  }\n}\n",
			after:   "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {\n    \"servers\": {\n      \"keep\": {\"type\": \"local\", \"command\": [\"k\"]}\n    }\n  }\n}\n",
			wantEnv: map[string]string{"AULAGLOBAL_DOWNLOAD_DIR": "/n"},
		},
		{
			name: "opencode V1", a: assistantstest.ByID(t, "opencode"),
			path:   func(e assistants.Env) string { return filepath.Join(userpaths.OpenCode(e.Home, ""), "opencode.json") },
			before: "{\n  \"mcp\": {\n    \"keep\": {\"type\": \"local\", \"command\": [\"k\"], \"enabled\": true},\n    \"aulaglobal\": {\"type\": \"local\", \"command\": [\"%EXE%\", \"--transport\", \"stdio\"], \"enabled\": true}\n  },\n  \"theme\": \"dark\"\n}\n",
			after:  "{\n  \"mcp\": {\n    \"keep\": {\"type\": \"local\", \"command\": [\"k\"], \"enabled\": true}\n  },\n  \"theme\": \"dark\"\n}\n",
		},
	}
}

func TestRemoveEveryFileClientKeepsTheRestByteForByte(t *testing.T) {
	for _, tc := range removeCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			e := assistantstest.FileEnv(t)
			p := tc.path(e)
			exe := filepath.Join(e.Home, "bin", "aulaglobal-mcp")
			literal := exe // TOML literal strings take the path as is; JSON needs backslashes escaped
			if tc.a.ID() != "codex" {
				literal = strings.ReplaceAll(exe, `\`, `\\`)
			}
			testutil.WriteFile(t, p, strings.ReplaceAll(tc.before, "%EXE%", literal))
			if !tc.a.Detect(e) {
				t.Fatal("not detected")
			}
			l := tc.a.Find(e)
			if l.State != assistants.LinkPresent || l.Location != p {
				t.Fatalf("%+v", l)
			}
			if filepath.Clean(l.Command) != filepath.Clean(exe) {
				t.Fatalf("command %q", l.Command)
			}
			for k, v := range tc.wantEnv {
				if l.Env[k] != v {
					t.Fatalf("env %v", l.Env)
				}
			}
			original := testutil.ReadFile(t, p)
			if err := tc.a.Remove(e); err != nil {
				t.Fatal(err)
			}
			want := tc.after
			if got := testutil.ReadFile(t, p); got != want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, want)
			}
			if testutil.ReadFile(t, p+".bak") != original {
				t.Fatal("backup differs from the original")
			}
			if l := tc.a.Find(e); l.State != assistants.LinkNone {
				t.Fatalf("still present: %+v", l)
			}
			// Idempotent: nothing left to remove, nothing rewritten.
			if err := tc.a.Remove(e); err != nil {
				t.Fatal(err)
			}
			if testutil.ReadFile(t, p) != want {
				t.Fatal("second removal changed the file")
			}
		})
	}
}

func TestRemoveClientStates(t *testing.T) {
	for _, a := range assistants.All() {
		if a.ID() == "claude-code" {
			continue
		}
		t.Run(a.ID(), func(t *testing.T) {
			e := assistantstest.FileEnv(t)
			if l := a.Find(e); l.State != assistants.LinkNotInstalled {
				t.Fatalf("%+v", l)
			}
			var dir string
			switch a.ID() {
			case "claude-desktop":
				dir = filepath.Dir(userpaths.Claude(e.GOOS, e.Home, e.ConfigDir))
			case "codex":
				dir = filepath.Join(e.Home, ".codex")
			case "opencode":
				dir = userpaths.OpenCode(e.Home, "")
			case "antigravity":
				dir = filepath.Dir(userpaths.Antigravity(e.Home))
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if l := a.Find(e); l.State != assistants.LinkNone {
				t.Fatalf("no file: %+v", l)
			}
			if err := a.Remove(e); err != nil {
				t.Fatal(err)
			}
			if len(mustGlob(t, dir)) != 0 {
				t.Fatal("removal created files")
			}
		})
	}
}

func mustGlob(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMalformedFilesAreLeftUntouched(t *testing.T) {
	bad := map[string]string{
		"claude-desktop": "{ not json",
		"antigravity":    `{"mcpServers":{"aulaglobal":{"command":"x"},"aulaglobal":{"command":"y"}}}`,
		"codex":          "[mcp_servers.aulaglobal\ncommand = ",
		"opencode":       "{\n  // comment\n  \"mcp\": {\"servers\": {\"aulaglobal\": {\"type\":\"local\",\"command\":[\"x\"]}}}\n}\n",
	}
	for _, tc := range removeCases(t) {
		content, ok := bad[tc.a.ID()]
		if !ok {
			continue
		}
		delete(bad, tc.a.ID()) // once per assistant
		t.Run(tc.a.ID(), func(t *testing.T) {
			e := assistantstest.FileEnv(t)
			p := tc.path(e)
			testutil.WriteFile(t, p, content)
			l := tc.a.Find(e)
			if l.State != assistants.LinkError {
				t.Fatalf("%+v", l)
			}
			if err := tc.a.Remove(e); err == nil {
				t.Fatal("removal of an unreadable file succeeded")
			}
			if testutil.ReadFile(t, p) != content {
				t.Fatal("file changed")
			}
			if _, err := os.Stat(p + ".bak"); err == nil {
				t.Fatal("backup of an untouched file")
			}
			if m := tc.a.ManualRemove(e); !strings.Contains(m, p) || !strings.Contains(m, "aulaglobal") {
				t.Fatal(m)
			}
		})
	}
}

func TestCodexInlineAndMultilineLayoutsAreRefused(t *testing.T) {
	for name, content := range map[string]string{
		"inline table": "[mcp_servers]\naulaglobal = { command = \"x\" }\n",
		"dotted keys":  "[mcp_servers]\naulaglobal.command = \"x\"\n",
		"multiline":    "note = \"\"\"\nhello\n\"\"\"\n[mcp_servers.aulaglobal]\ncommand = \"x\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if out, _, err := assistants.CodexRemove([]byte(content)); err == nil {
				t.Fatalf("removed unsafely: %q", out)
			}
		})
	}
}

func TestJSONMemberRemovalShapes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"mcpServers":{"aulaglobal":{"a":1}}}`, `{"mcpServers":{}}`},
		{`{"mcpServers":{"aulaglobal":{"a":1},"b":2}}`, `{"mcpServers":{"b":2}}`},
		{`{"mcpServers":{"b":2,"aulaglobal":{"a":1}}}`, `{"mcpServers":{"b":2}}`},
		{`{"mcpServers":{"b":2,"aulaglobal":{"a":1},"c":[1,2]}}`, `{"mcpServers":{"b":2,"c":[1,2]}}`},
		{`{"x":{"aulaglobal":1},"mcpServers":{"aulaglobal":null,"b":2}}`, `{"x":{"aulaglobal":1},"mcpServers":{"b":2}}`},
	} {
		got, found, err := assistants.JSONRemoveServer([]byte(tc.in), []string{"mcpServers"})
		if err != nil || !found || string(got) != tc.want {
			t.Errorf("%s -> %s (%v %v)", tc.in, got, found, err)
		}
	}
	for _, in := range []string{`{}`, `{"mcpServers":{"b":1}}`, `{"other":{"aulaglobal":1}}`} {
		if out, found, err := assistants.JSONRemoveServer([]byte(in), []string{"mcpServers"}); found || err != nil || out != nil {
			t.Errorf("%s: found=%v err=%v", in, found, err)
		}
	}
	if _, _, err := assistants.JSONRemoveServer([]byte(`{"mcpServers":[1]}`), []string{"mcpServers"}); err == nil {
		t.Error("array accepted as servers")
	}
}

func TestSymlinkedConfigIsNotReplaced(t *testing.T) {
	e := assistantstest.FileEnv(t)
	a := assistantstest.ByID(t, "codex")
	dir := filepath.Join(e.Home, ".codex")
	real := filepath.Join(e.Home, "dotfiles", "config.toml")
	testutil.WriteFile(t, real, "[mcp_servers.aulaglobal]\ncommand = \"/x/aulaglobal-mcp\"\n")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	testutil.SymlinkOrSkip(t, real, filepath.Join(dir, "config.toml"))
	if err := a.Remove(e); err == nil {
		t.Fatal("a symlinked config was rewritten")
	}
	if !strings.Contains(testutil.ReadFile(t, real), "aulaglobal") {
		t.Fatal("target changed")
	}
}
