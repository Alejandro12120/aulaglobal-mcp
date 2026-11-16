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
	name      string
	assistant assistants.Assistant
	path      func(assistants.Env) string
	before    string // %EXE% is replaced by the command
	after     string
	wantEnv   map[string]string
}

func removeCases(t *testing.T) []removeCase {
	return []removeCase{
		{
			name: "claude-desktop keeps order, indentation and big numbers", assistant: assistantstest.ByID(t, "claude-desktop"),
			path:    func(env assistants.Env) string { return userpaths.Claude(env.GOOS, env.Home, env.ConfigDir) },
			before:  "{\n  \"zeta\": 1234567890123456789,\n  \"mcpServers\": {\n    \"keep\": {\n      \"command\": \"keep\"\n    },\n    \"aulaglobal\": {\n      \"command\": \"%EXE%\",\n      \"args\": [\"--transport\", \"stdio\"],\n      \"env\": {\"AULAGLOBAL_DOWNLOAD_DIR\": \"/notes\"}\n    },\n    \"last\": {\"command\": \"last\"}\n  },\n  \"alpha\": true\n}\n",
			after:   "{\n  \"zeta\": 1234567890123456789,\n  \"mcpServers\": {\n    \"keep\": {\n      \"command\": \"keep\"\n    },\n    \"last\": {\"command\": \"last\"}\n  },\n  \"alpha\": true\n}\n",
			wantEnv: map[string]string{"AULAGLOBAL_DOWNLOAD_DIR": "/notes"},
		},
		{
			name: "claude-desktop only server", assistant: assistantstest.ByID(t, "claude-desktop"),
			path:   func(env assistants.Env) string { return userpaths.Claude(env.GOOS, env.Home, env.ConfigDir) },
			before: "{\n  \"mcpServers\": {\n    \"aulaglobal\": {\"command\": \"%EXE%\"}\n  }\n}\n",
			after:  "{\n  \"mcpServers\": {}\n}\n",
		},
		{
			name: "antigravity last member", assistant: assistantstest.ByID(t, "antigravity"),
			path:   func(env assistants.Env) string { return userpaths.Antigravity(env.Home) },
			before: "{\"mcpServers\":{\"keep\":{\"command\":\"k\"},\"aulaglobal\":{\"command\":\"%EXE%\",\"disabled\":false}}}",
			after:  "{\"mcpServers\":{\"keep\":{\"command\":\"k\"}}}",
		},
		{
			name: "antigravity first member with tabs", assistant: assistantstest.ByID(t, "antigravity"),
			path:   func(env assistants.Env) string { return userpaths.Antigravity(env.Home) },
			before: "{\n\t\"mcpServers\": {\n\t\t\"aulaglobal\": {\"command\": \"%EXE%\"},\n\t\t\"keep\": {\"command\": \"k\"}\n\t}\n}",
			after:  "{\n\t\"mcpServers\": {\n\t\t\"keep\": {\"command\": \"k\"}\n\t}\n}",
		},
		{
			name: "codex table with sub-table and comments", assistant: assistantstest.ByID(t, "codex"),
			path:    func(env assistants.Env) string { return filepath.Join(env.Home, ".codex", "config.toml") },
			before:  "# my settings\nmodel = \"x\"\n\n[mcp_servers.other]\ncommand = \"keep\" # note\n\n[mcp_servers.aulaglobal]\ncommand = '%EXE%'\nargs = ['--transport', 'stdio']\n\n[mcp_servers.aulaglobal.env]\nAULAGLOBAL_TOKEN_FILE = '/tok'\n\n[projects.\"/p\"]\ntrust = \"trusted\"\n",
			after:   "# my settings\nmodel = \"x\"\n\n[mcp_servers.other]\ncommand = \"keep\" # note\n\n[projects.\"/p\"]\ntrust = \"trusted\"\n",
			wantEnv: map[string]string{"AULAGLOBAL_TOKEN_FILE": "/tok"},
		},
		{
			name: "codex block appended by setup is removed with its blank line", assistant: assistantstest.ByID(t, "codex"),
			path:   func(env assistants.Env) string { return filepath.Join(env.Home, ".codex", "config.toml") },
			before: "model = \"x\"\n\n[mcp_servers.aulaglobal]\ncommand = '%EXE%'\n",
			after:  "model = \"x\"\n",
		},
		{
			name: "opencode V2", assistant: assistantstest.ByID(t, "opencode"),
			path: func(env assistants.Env) string {
				return filepath.Join(userpaths.OpenCode(env.Home, ""), "opencode.json")
			},
			before:  "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {\n    \"servers\": {\n      \"aulaglobal\": {\n        \"type\": \"local\",\n        \"command\": [\"%EXE%\", \"--transport\", \"stdio\"],\n        \"environment\": {\"AULAGLOBAL_DOWNLOAD_DIR\": \"/n\"}\n      },\n      \"keep\": {\"type\": \"local\", \"command\": [\"k\"]}\n    }\n  }\n}\n",
			after:   "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {\n    \"servers\": {\n      \"keep\": {\"type\": \"local\", \"command\": [\"k\"]}\n    }\n  }\n}\n",
			wantEnv: map[string]string{"AULAGLOBAL_DOWNLOAD_DIR": "/n"},
		},
		{
			name: "opencode V1", assistant: assistantstest.ByID(t, "opencode"),
			path: func(env assistants.Env) string {
				return filepath.Join(userpaths.OpenCode(env.Home, ""), "opencode.json")
			},
			before: "{\n  \"mcp\": {\n    \"keep\": {\"type\": \"local\", \"command\": [\"k\"], \"enabled\": true},\n    \"aulaglobal\": {\"type\": \"local\", \"command\": [\"%EXE%\", \"--transport\", \"stdio\"], \"enabled\": true}\n  },\n  \"theme\": \"dark\"\n}\n",
			after:  "{\n  \"mcp\": {\n    \"keep\": {\"type\": \"local\", \"command\": [\"k\"], \"enabled\": true}\n  },\n  \"theme\": \"dark\"\n}\n",
		},
	}
}

func TestRemoveEveryFileClientKeepsTheRestByteForByte(t *testing.T) {
	for _, tc := range removeCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			env := assistantstest.FileEnv(t)
			path := tc.path(env)
			exe := filepath.Join(env.Home, "bin", "aulaglobal-mcp")
			literal := exe // TOML literal strings take the path as is; JSON needs backslashes escaped
			if tc.assistant.ID() != "codex" {
				literal = strings.ReplaceAll(exe, `\`, `\\`)
			}
			testutil.WriteFile(t, path, strings.ReplaceAll(tc.before, "%EXE%", literal))
			if !tc.assistant.Detect(env) {
				t.Fatal("not detected")
			}
			link := tc.assistant.Find(env)
			if link.State != assistants.LinkPresent || link.Location != path {
				t.Fatalf("%+v", link)
			}
			if filepath.Clean(link.Command) != filepath.Clean(exe) {
				t.Fatalf("command %q", link.Command)
			}
			for name, v := range tc.wantEnv {
				if link.Env[name] != v {
					t.Fatalf("env %v", link.Env)
				}
			}
			original := testutil.ReadFile(t, path)
			if err := tc.assistant.Remove(env); err != nil {
				t.Fatal(err)
			}
			want := tc.after
			if got := testutil.ReadFile(t, path); got != want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, want)
			}
			if testutil.ReadFile(t, path+".bak") != original {
				t.Fatal("backup differs from the original")
			}
			if link := tc.assistant.Find(env); link.State != assistants.LinkNone {
				t.Fatalf("still present: %+v", link)
			}
			// Idempotent: nothing left to remove, nothing rewritten.
			if err := tc.assistant.Remove(env); err != nil {
				t.Fatal(err)
			}
			if testutil.ReadFile(t, path) != want {
				t.Fatal("second removal changed the file")
			}
		})
	}
}

func TestRemoveClientStates(t *testing.T) {
	for _, assistant := range assistants.All() {
		if assistant.ID() == "claude-code" {
			continue
		}
		t.Run(assistant.ID(), func(t *testing.T) {
			env := assistantstest.FileEnv(t)
			if link := assistant.Find(env); link.State != assistants.LinkNotInstalled {
				t.Fatalf("%+v", link)
			}
			var dir string
			switch assistant.ID() {
			case "claude-desktop":
				dir = filepath.Dir(userpaths.Claude(env.GOOS, env.Home, env.ConfigDir))
			case "codex":
				dir = filepath.Join(env.Home, ".codex")
			case "opencode":
				dir = userpaths.OpenCode(env.Home, "")
			case "antigravity":
				dir = filepath.Dir(userpaths.Antigravity(env.Home))
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if link := assistant.Find(env); link.State != assistants.LinkNone {
				t.Fatalf("no file: %+v", link)
			}
			if err := assistant.Remove(env); err != nil {
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
	matches, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestMalformedFilesAreLeftUntouched(t *testing.T) {
	bad := map[string]string{
		"claude-desktop": "{ not json",
		"antigravity":    `{"mcpServers":{"aulaglobal":{"command":"x"},"aulaglobal":{"command":"y"}}}`,
		"codex":          "[mcp_servers.aulaglobal\ncommand = ",
		"opencode":       "{\n  // comment\n  \"mcp\": {\"servers\": {\"aulaglobal\": {\"type\":\"local\",\"command\":[\"x\"]}}}\n}\n",
	}
	for _, tc := range removeCases(t) {
		content, ok := bad[tc.assistant.ID()]
		if !ok {
			continue
		}
		delete(bad, tc.assistant.ID()) // once per assistant
		t.Run(tc.assistant.ID(), func(t *testing.T) {
			env := assistantstest.FileEnv(t)
			path := tc.path(env)
			testutil.WriteFile(t, path, content)
			link := tc.assistant.Find(env)
			if link.State != assistants.LinkError {
				t.Fatalf("%+v", link)
			}
			if err := tc.assistant.Remove(env); err == nil {
				t.Fatal("removal of an unreadable file succeeded")
			}
			if testutil.ReadFile(t, path) != content {
				t.Fatal("file changed")
			}
			if _, err := os.Stat(path + ".bak"); err == nil {
				t.Fatal("backup of an untouched file")
			}
			if instructions := tc.assistant.ManualRemove(env); !strings.Contains(instructions, path) || !strings.Contains(instructions, "aulaglobal") {
				t.Fatal(instructions)
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
	env := assistantstest.FileEnv(t)
	assistant := assistantstest.ByID(t, "codex")
	dir := filepath.Join(env.Home, ".codex")
	real := filepath.Join(env.Home, "dotfiles", "config.toml")
	testutil.WriteFile(t, real, "[mcp_servers.aulaglobal]\ncommand = \"/x/aulaglobal-mcp\"\n")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	testutil.SymlinkOrSkip(t, real, filepath.Join(dir, "config.toml"))
	if err := assistant.Remove(env); err == nil {
		t.Fatal("a symlinked config was rewritten")
	}
	if !strings.Contains(testutil.ReadFile(t, real), "aulaglobal") {
		t.Fatal("target changed")
	}
}
