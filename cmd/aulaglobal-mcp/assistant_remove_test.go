package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/userpaths"
)

type removeCase struct {
	name    string
	a       assistant
	path    func(clientEnv) string
	before  string // %EXE% is replaced by the command
	after   string
	wantEnv map[string]string
}

func removeCases() []removeCase {
	return []removeCase{
		{
			name: "claude-desktop keeps order, indentation and big numbers", a: desktopAssistant(),
			path:    func(e clientEnv) string { return userpaths.Claude(e.GOOS, e.Home, e.Config) },
			before:  "{\n  \"zeta\": 1234567890123456789,\n  \"mcpServers\": {\n    \"keep\": {\n      \"command\": \"keep\"\n    },\n    \"aulaglobal\": {\n      \"command\": \"%EXE%\",\n      \"args\": [\"--transport\", \"stdio\"],\n      \"env\": {\"AULAGLOBAL_DOWNLOAD_DIR\": \"/notes\"}\n    },\n    \"last\": {\"command\": \"last\"}\n  },\n  \"alpha\": true\n}\n",
			after:   "{\n  \"zeta\": 1234567890123456789,\n  \"mcpServers\": {\n    \"keep\": {\n      \"command\": \"keep\"\n    },\n    \"last\": {\"command\": \"last\"}\n  },\n  \"alpha\": true\n}\n",
			wantEnv: map[string]string{"AULAGLOBAL_DOWNLOAD_DIR": "/notes"},
		},
		{
			name: "claude-desktop only server", a: desktopAssistant(),
			path:   func(e clientEnv) string { return userpaths.Claude(e.GOOS, e.Home, e.Config) },
			before: "{\n  \"mcpServers\": {\n    \"aulaglobal\": {\"command\": \"%EXE%\"}\n  }\n}\n",
			after:  "{\n  \"mcpServers\": {}\n}\n",
		},
		{
			name: "antigravity last member", a: antigravityAssistant(),
			path:   func(e clientEnv) string { return userpaths.Antigravity(e.Home) },
			before: "{\"mcpServers\":{\"keep\":{\"command\":\"k\"},\"aulaglobal\":{\"command\":\"%EXE%\",\"disabled\":false}}}",
			after:  "{\"mcpServers\":{\"keep\":{\"command\":\"k\"}}}",
		},
		{
			name: "antigravity first member with tabs", a: antigravityAssistant(),
			path:   func(e clientEnv) string { return userpaths.Antigravity(e.Home) },
			before: "{\n\t\"mcpServers\": {\n\t\t\"aulaglobal\": {\"command\": \"%EXE%\"},\n\t\t\"keep\": {\"command\": \"k\"}\n\t}\n}",
			after:  "{\n\t\"mcpServers\": {\n\t\t\"keep\": {\"command\": \"k\"}\n\t}\n}",
		},
		{
			name: "codex table with sub-table and comments", a: codexAssistant(),
			path:    func(e clientEnv) string { return filepath.Join(e.Home, ".codex", "config.toml") },
			before:  "# my settings\nmodel = \"x\"\n\n[mcp_servers.other]\ncommand = \"keep\" # note\n\n[mcp_servers.aulaglobal]\ncommand = '%EXE%'\nargs = ['--transport', 'stdio']\n\n[mcp_servers.aulaglobal.env]\nAULAGLOBAL_TOKEN_FILE = '/tok'\n\n[projects.\"/p\"]\ntrust = \"trusted\"\n",
			after:   "# my settings\nmodel = \"x\"\n\n[mcp_servers.other]\ncommand = \"keep\" # note\n\n[projects.\"/p\"]\ntrust = \"trusted\"\n",
			wantEnv: map[string]string{"AULAGLOBAL_TOKEN_FILE": "/tok"},
		},
		{
			name: "codex block appended by setup is removed with its blank line", a: codexAssistant(),
			path:   func(e clientEnv) string { return filepath.Join(e.Home, ".codex", "config.toml") },
			before: "model = \"x\"\n\n[mcp_servers.aulaglobal]\ncommand = '%EXE%'\n",
			after:  "model = \"x\"\n",
		},
		{
			name: "opencode V2", a: openCodeAssistant{},
			path:    func(e clientEnv) string { return filepath.Join(userpaths.OpenCode(e.Home, ""), "opencode.json") },
			before:  "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {\n    \"servers\": {\n      \"aulaglobal\": {\n        \"type\": \"local\",\n        \"command\": [\"%EXE%\", \"--transport\", \"stdio\"],\n        \"environment\": {\"AULAGLOBAL_DOWNLOAD_DIR\": \"/n\"}\n      },\n      \"keep\": {\"type\": \"local\", \"command\": [\"k\"]}\n    }\n  }\n}\n",
			after:   "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {\n    \"servers\": {\n      \"keep\": {\"type\": \"local\", \"command\": [\"k\"]}\n    }\n  }\n}\n",
			wantEnv: map[string]string{"AULAGLOBAL_DOWNLOAD_DIR": "/n"},
		},
		{
			name: "opencode V1", a: openCodeAssistant{},
			path:   func(e clientEnv) string { return filepath.Join(userpaths.OpenCode(e.Home, ""), "opencode.json") },
			before: "{\n  \"mcp\": {\n    \"keep\": {\"type\": \"local\", \"command\": [\"k\"], \"enabled\": true},\n    \"aulaglobal\": {\"type\": \"local\", \"command\": [\"%EXE%\", \"--transport\", \"stdio\"], \"enabled\": true}\n  },\n  \"theme\": \"dark\"\n}\n",
			after:  "{\n  \"mcp\": {\n    \"keep\": {\"type\": \"local\", \"command\": [\"k\"], \"enabled\": true}\n  },\n  \"theme\": \"dark\"\n}\n",
		},
	}
}

func TestRemoveEveryFileClientKeepsTheRestByteForByte(t *testing.T) {
	for _, tc := range removeCases() {
		t.Run(tc.name, func(t *testing.T) {
			e := fileClientEnv(t)
			p := tc.path(e)
			exe := filepath.Join(e.Home, "bin", "aulaglobal-mcp")
			literal := exe // TOML literal strings take the path as is; JSON needs backslashes escaped
			if tc.a.ID() != "codex" {
				literal = strings.ReplaceAll(exe, `\`, `\\`)
			}
			writeFile(t, p, strings.ReplaceAll(tc.before, "%EXE%", literal))
			if !tc.a.Detect(e) {
				t.Fatal("not detected")
			}
			l := tc.a.Find(e)
			if l.state != linkPresent || l.location != p {
				t.Fatalf("%+v", l)
			}
			if !pointsToProgram(l.command, exe, runtime.GOOS == "windows") {
				t.Fatalf("command %q", l.command)
			}
			for k, v := range tc.wantEnv {
				if l.env[k] != v {
					t.Fatalf("env %v", l.env)
				}
			}
			original := readFile(t, p)
			if err := tc.a.Remove(e); err != nil {
				t.Fatal(err)
			}
			want := tc.after
			if got := readFile(t, p); got != want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, want)
			}
			if readFile(t, p+".bak") != original {
				t.Fatal("backup differs from the original")
			}
			if l := tc.a.Find(e); l.state != linkNone {
				t.Fatalf("still present: %+v", l)
			}
			// Idempotent: nothing left to remove, nothing rewritten.
			if err := tc.a.Remove(e); err != nil {
				t.Fatal(err)
			}
			if readFile(t, p) != want {
				t.Fatal("second removal changed the file")
			}
		})
	}
}

func TestRemoveClientStates(t *testing.T) {
	for _, a := range assistants {
		if a.ID() == "claude-code" {
			continue
		}
		t.Run(a.ID(), func(t *testing.T) {
			e := fileClientEnv(t)
			if l := a.Find(e); l.state != linkNotInstalled {
				t.Fatalf("%+v", l)
			}
			var dir string
			switch a.ID() {
			case "claude-desktop":
				dir = filepath.Dir(userpaths.Claude(e.GOOS, e.Home, e.Config))
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
			if l := a.Find(e); l.state != linkNone {
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
	for _, tc := range removeCases() {
		content, ok := bad[tc.a.ID()]
		if !ok {
			continue
		}
		delete(bad, tc.a.ID()) // once per assistant
		t.Run(tc.a.ID(), func(t *testing.T) {
			e := fileClientEnv(t)
			p := tc.path(e)
			writeFile(t, p, content)
			l := tc.a.Find(e)
			if l.state != linkError {
				t.Fatalf("%+v", l)
			}
			if err := tc.a.Remove(e); err == nil {
				t.Fatal("removal of an unreadable file succeeded")
			}
			if readFile(t, p) != content {
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
			if out, _, err := codexRemove([]byte(content)); err == nil {
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
		got, found, err := jsonRemoveServer([]byte(tc.in), []string{"mcpServers"})
		if err != nil || !found || string(got) != tc.want {
			t.Errorf("%s -> %s (%v %v)", tc.in, got, found, err)
		}
	}
	for _, in := range []string{`{}`, `{"mcpServers":{"b":1}}`, `{"other":{"aulaglobal":1}}`} {
		if out, found, err := jsonRemoveServer([]byte(in), []string{"mcpServers"}); found || err != nil || out != nil {
			t.Errorf("%s: found=%v err=%v", in, found, err)
		}
	}
	if _, _, err := jsonRemoveServer([]byte(`{"mcpServers":[1]}`), []string{"mcpServers"}); err == nil {
		t.Error("array accepted as servers")
	}
}

func TestSymlinkedConfigIsNotReplaced(t *testing.T) {
	e := fileClientEnv(t)
	a := codexAssistant()
	dir := filepath.Join(e.Home, ".codex")
	real := filepath.Join(e.Home, "dotfiles", "config.toml")
	writeFile(t, real, "[mcp_servers.aulaglobal]\ncommand = \"/x/aulaglobal-mcp\"\n")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, real, filepath.Join(dir, "config.toml"))
	if err := a.Remove(e); err == nil {
		t.Fatal("a symlinked config was rewritten")
	}
	if !strings.Contains(readFile(t, real), "aulaglobal") {
		t.Fatal("target changed")
	}
}

// ---- Claude Code -------------------------------------------------------------

// symlinkOrSkip creates link -> target, skipping the test where the platform
// cannot create symlinks (Windows without the privilege) or does not report them
// as symlinks (Wine), since the code under test relies on Lstat telling them apart.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Skip("this platform does not report symlinks to Lstat")
	}
}
