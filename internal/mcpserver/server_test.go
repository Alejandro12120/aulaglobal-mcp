package mcpserver

import (
	"aulaglobal-mcp/internal/aulaglobal"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSecurity(t *testing.T) {
	handler := SecureHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), "secret", false, []string{"localhost:8080"}, []string{"http://localhost:8080"})
	for _, tc := range []struct {
		host, origin, auth string
		want               int
	}{
		{"localhost:8080", "", "Bearer secret", 204}, {"localhost:8080", "", "", 401}, {"evil.test", "", "Bearer secret", 403}, {"localhost:8080", "https://evil.test", "Bearer secret", 403}, {"localhost:8080", "http://localhost:8080", "Bearer secret", 204},
	} {
		r := httptest.NewRequest("POST", "http://"+tc.host+"/mcp", strings.NewReader("{}"))
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v: %d", tc, w.Code)
		}
	}
}

type fakeUpdates struct {
	mu        sync.Mutex
	latest    string
	announced bool
}

func (f *fakeUpdates) Available() string { f.mu.Lock(); defer f.mu.Unlock(); return f.latest }
func (f *fakeUpdates) TakeNotice() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.latest == "" || f.announced {
		return ""
	}
	f.announced = true
	return "NOTICE " + f.latest
}

func connect(t *testing.T, updates Updates) *mcp.ClientSession {
	t.Helper()
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"fullname":"Ana","userid":7,"sitename":"AulaGlobal","release":"4.1"}`))
	}))
	t.Cleanup(moodle.Close)
	api := aulaglobal.New(moodle.URL, aulaglobal.StaticToken{Value: "secret"})
	var notifier Updates = updates
	if updates == (*fakeUpdates)(nil) {
		notifier = nil
	}
	server := New(api, nil, notifier)
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func texts(res *mcp.CallToolResult) []string {
	var out []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			out = append(out, tc.Text)
		}
	}
	return out
}

func TestUpdateNoticeAppendedOnceAndAuthField(t *testing.T) {
	session := connect(t, &fakeUpdates{latest: "v0.9.0"})
	// A failing call must not consume the notice.
	if res := call(t, session, "download_files", map[string]any{}); !res.IsError || strings.Contains(strings.Join(texts(res), ""), "NOTICE") {
		t.Fatalf("error result: %+v", res)
	}
	first := call(t, session, "auth_status", nil)
	if first.IsError {
		t.Fatalf("%+v", first)
	}
	got := texts(first)
	if len(got) != 2 || got[1] != "NOTICE v0.9.0" {
		t.Fatalf("first result contents: %q", got)
	}
	structured, _ := first.StructuredContent.(map[string]any)
	if structured["update_available"] != "v0.9.0" || structured["sitename"] != "AulaGlobal" || structured["token_valid"] != true {
		t.Fatalf("structured: %v", structured)
	}
	second := call(t, session, "auth_status", nil)
	if len(texts(second)) != 1 {
		t.Fatalf("notice repeated: %q", texts(second))
	}
	if structured, _ := second.StructuredContent.(map[string]any); structured["update_available"] != "v0.9.0" {
		t.Fatalf("auth_status lost update_available: %v", structured)
	}
}

func TestNoUpdateNoNoticeAndNoField(t *testing.T) {
	for name, updates := range map[string]*fakeUpdates{"up to date": {}, "disabled": nil} {
		session := connect(t, updates)
		res := call(t, session, "auth_status", nil)
		if len(texts(res)) != 1 {
			t.Errorf("%s: %q", name, texts(res))
		}
		structured, _ := res.StructuredContent.(map[string]any)
		if _, present := structured["update_available"]; present || structured["sitename"] != "AulaGlobal" {
			t.Errorf("%s: %v", name, structured)
		}
	}
}

func TestNoticeAppearsWhenCheckFinishesLater(t *testing.T) {
	updates := &fakeUpdates{}
	session := connect(t, updates)
	if len(texts(call(t, session, "auth_status", nil))) != 1 {
		t.Fatal("notice before the check")
	}
	updates.mu.Lock()
	updates.latest = "v1.0.0"
	updates.mu.Unlock()
	if got := texts(call(t, session, "auth_status", nil)); len(got) != 2 {
		t.Fatalf("%q", got)
	}
}

// auth_status must not hand the assistant who the user is: neither in the
// structured result nor in the text copy, nor in the published output schema.
func TestAuthStatusExposesNoPersonalData(t *testing.T) {
	session := connect(t, nil)
	res := call(t, session, "auth_status", nil)
	if res.IsError {
		t.Fatalf("%+v", res)
	}
	structured, _ := res.StructuredContent.(map[string]any)
	for _, k := range []string{"fullname", "userid", "username", "firstname", "lastname", "userpictureurl"} {
		if _, present := structured[k]; present {
			t.Errorf("auth_status returned %q", k)
		}
	}
	for _, want := range []string{"token_valid", "sitename", "release", "expires_at", "expiry_source"} {
		if _, present := structured[want]; !present {
			t.Errorf("auth_status lacks %q: %v", want, structured)
		}
	}
	if structured["release"] != "4.1" {
		t.Errorf("release: %v", structured["release"])
	}
	all := strings.Join(texts(res), "\n")
	if strings.Contains(all, "Ana") || strings.Contains(all, "userid") || strings.Contains(all, "fullname") {
		t.Errorf("personal data in text content: %s", all)
	}
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		schema, _ := json.Marshal(tool.OutputSchema)
		// (list_courses legitimately has a course "fullname"; no tool has a user ID.)
		if strings.Contains(string(schema), "userid") || (tool.Name == "auth_status" && strings.Contains(string(schema), "fullname")) {
			t.Errorf("%s output schema mentions personal fields: %s", tool.Name, schema)
		}
		if tool.Name == "auth_status" && !strings.Contains(tool.Description, "Does not return the account holder's name or user ID") {
			t.Errorf("description does not state the data minimisation: %s", tool.Description)
		}
	}
}
