package assistants_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/assistants"
)

func TestConfigMerging(t *testing.T) {
	entry := map[string]any{"command": `C:\Users\Ana\aulaglobal-mcp.exe`, "args": []string{"--transport", "stdio"}}
	old := []byte(`{"other":{"large":1234567890123456789},"mcpServers":{"keep":{"command":"keep"}}}`)
	b, _, e := assistants.McpServersConfig(old, entry, true)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "1234567890123456789") || !strings.Contains(string(b), "\"keep\"") {
		t.Fatal(string(b))
	}
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, old, 0600)
	if e = assistants.AtomicConfig(path, b); e != nil {
		t.Fatal(e)
	}
	bak, _ := os.ReadFile(path + ".bak")
	if string(bak) != string(old) {
		t.Fatal("backup")
	}
	b2, _ := os.ReadFile(path)
	if string(b2) != string(b) {
		t.Fatal("write")
	}
	for _, old := range []string{"# keep\nmodel = 'x'\n[mcp_servers.other]\ncommand='keep'\n", "model='x'\n[mcp_servers.aulaglobal]\ncommand='old'\n[mcp_servers.aulaglobal.env]\nX='x'\n[mcp_servers.other]\ncommand='keep'\n", "model='x'\n[mcp_servers.\"aulaglobal\"]\ncommand='old'\n"} {
		b, exists, e := assistants.CodexConfig([]byte(old), entry, false)
		if e != nil {
			t.Fatal(e)
		}
		if exists {
			if b != nil {
				t.Fatal("must confirm")
			}
			b, _, e = assistants.CodexConfig([]byte(old), entry, true)
		}
		if e != nil || !strings.Contains(string(b), "model") || strings.Contains(string(b), "command='old'") {
			t.Fatal(e, string(b))
		}
		if strings.Contains(old, "# keep") && !strings.Contains(string(b), "# keep") {
			t.Fatal("comment lost")
		}
	}
	if _, _, e = assistants.McpServersConfig([]byte("{bad"), entry, true); e == nil {
		t.Fatal("invalid json accepted")
	}
	if _, _, e = assistants.CodexConfig([]byte("[bad"), entry, true); e == nil {
		t.Fatal("invalid toml accepted")
	}
	var root map[string]any
	if e = json.Unmarshal(b, &root); e != nil {
		t.Fatal(e)
	}
}
