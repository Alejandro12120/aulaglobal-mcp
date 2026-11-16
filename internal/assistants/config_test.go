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
	merged, _, err := assistants.McpServersConfig(old, entry, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(merged), "1234567890123456789") || !strings.Contains(string(merged), "\"keep\"") {
		t.Fatal(string(merged))
	}
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, old, 0600)
	if err = assistants.AtomicConfig(path, merged); err != nil {
		t.Fatal(err)
	}
	bak, _ := os.ReadFile(path + ".bak")
	if string(bak) != string(old) {
		t.Fatal("backup")
	}
	b2, _ := os.ReadFile(path)
	if string(b2) != string(merged) {
		t.Fatal("write")
	}
	for _, old := range []string{"# keep\nmodel = 'x'\n[mcp_servers.other]\ncommand='keep'\n", "model='x'\n[mcp_servers.aulaglobal]\ncommand='old'\n[mcp_servers.aulaglobal.env]\nX='x'\n[mcp_servers.other]\ncommand='keep'\n", "model='x'\n[mcp_servers.\"aulaglobal\"]\ncommand='old'\n"} {
		merged, exists, err := assistants.CodexConfig([]byte(old), entry, false)
		if err != nil {
			t.Fatal(err)
		}
		if exists {
			if merged != nil {
				t.Fatal("must confirm")
			}
			merged, _, err = assistants.CodexConfig([]byte(old), entry, true)
		}
		if err != nil || !strings.Contains(string(merged), "model") || strings.Contains(string(merged), "command='old'") {
			t.Fatal(err, string(merged))
		}
		if strings.Contains(old, "# keep") && !strings.Contains(string(merged), "# keep") {
			t.Fatal("comment lost")
		}
	}
	if _, _, err = assistants.McpServersConfig([]byte("{bad"), entry, true); err == nil {
		t.Fatal("invalid json accepted")
	}
	if _, _, err = assistants.CodexConfig([]byte("[bad"), entry, true); err == nil {
		t.Fatal("invalid toml accepted")
	}
	var root map[string]any
	if err = json.Unmarshal(merged, &root); err != nil {
		t.Fatal(err)
	}
}
