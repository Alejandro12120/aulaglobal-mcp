package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/aulaglobal"
	"aulaglobal-mcp/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAssignmentStatement(t *testing.T) {
	long := strings.Repeat("界", 2100)
	intro := "<p>Read     <b>this</b>.</p><ul><li>A&nbsp; &amp; B</li><li>" + long + "</li></ul>"
	full := "Read this.\n\n- A & B\n- " + long
	activity := "<p>Show    your work<br>Submit it.</p><script>ignore()</script>"
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.URL.Path {
		case "/images/external.png":
			t.Error("assignment text fetched an external image")
		case "/pluginfile.php/intro/guide.txt", "/pluginfile.php/activity/guide.txt":
			if r.Form.Get("token") != "secret" || r.Form.Get("wstoken") != "" {
				t.Error("attachment download did not use the configured token")
			}
			fmt.Fprint(w, "example")
		default:
			if r.Form.Get("wsfunction") != "mod_assign_get_assignments" {
				t.Errorf("unexpected Moodle call: %q", r.Form.Get("wsfunction"))
			}
			courseID := r.Form.Get("courseids[0]")
			if courseID != "" && courseID != "7" {
				fmt.Fprint(w, `{"courses":[]}`)
				return
			}
			image := fmt.Sprintf(`<img src="http://%s/images/external.png">`, r.Host)
			fmt.Fprintf(w, `{"courses":[{"id":7,"fullname":"Curso","assignments":[
				{"id":3,"cmid":30,"name":"Other","intro":%q},
				{"id":5,"cmid":55,"name":"Practice","intro":%q,"activity":%q,"duedate":1767268800,
				"introattachments":[{"filename":"guide.txt","filepath":"/","fileurl":"http://%s/pluginfile.php/intro/guide.txt?token=omitme&wstoken=omitme"}],
				"activityattachments":[{"filename":"guide.txt","filepath":"/","fileurl":"http://%s/pluginfile.php/activity/guide.txt?token=omitme&wstoken=omitme"}]},
				{"id":8,"cmid":88,"name":"Text","intro":"a < b and x <tag>","introformat":2}
			]}]}`, "<p>"+strings.Repeat(" ", 2500)+"Short</p>", intro, activity+image, r.Host, r.Host)
		}
	}))
	t.Cleanup(moodle.Close)
	api := aulaglobal.New(moodle.URL, aulaglobal.StaticToken{Value: "secret"})
	if err := api.SetMaxRPS(aulaglobal.MaxMaxRPS); err != nil {
		t.Fatal(err)
	}
	dataStore, err := store.Open(t.TempDir(), 1<<20, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dataStore.Root.Close() })
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := New(api, dataStore, nil).Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	decode := func(res *mcp.CallToolResult, out any) {
		t.Helper()
		if res.IsError {
			t.Fatalf("tool error: %v", texts(res))
		}
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "omitme") || strings.Contains(string(raw), "external.png") {
			t.Fatal("attachment token or external image URL appeared in tool output")
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}

	var detail aulaglobal.Assignment
	decode(call(t, session, "get_assignment", map[string]any{"assignment_id": 5, "course_id": 7}), &detail)
	if detail.ID != 5 || detail.CMID != 55 || detail.CourseID != 7 || detail.CourseName != "Curso" || detail.Due.Unix != 1767268800 || detail.Due.Madrid == nil || detail.IntroNote != "Untrusted course content" {
		t.Fatal("incorrect assignment metadata")
	}
	if detail.Intro != full || detail.Activity != "Show your work\nSubmit it." {
		t.Errorf("detail text differs from fixture: intro has %d runes, activity %q", len([]rune(detail.Intro)), detail.Activity)
	}
	if len(detail.Attachments) != 1 || len(detail.ActivityAttachments) != 1 {
		t.Fatal("missing assignment attachments")
	}
	introHandle, activityHandle := detail.Attachments[0].Handle, detail.ActivityAttachments[0].Handle
	if introHandle == "" || activityHandle == "" || introHandle == activityHandle {
		t.Fatal("missing or colliding attachment handles")
	}
	// Download before listing to prove the detail tool registered both handles.
	download := call(t, session, "download_files", map[string]any{"handles": []string{introHandle, activityHandle}})
	if download.IsError {
		t.Fatalf("download: %v", texts(download))
	}
	raw, err := json.Marshal(download.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"guide.txt", "Actividad/guide.txt"} {
		body, err := dataStore.Root.ReadFile("Curso/Entregas/Practice/" + path)
		if err != nil || string(body) != "example" {
			t.Fatalf("download to %s: %s (%v)", path, raw, err)
		}
	}

	var list aulaglobal.AssignmentsOutput
	decode(call(t, session, "list_assignments", map[string]any{"course_ids": []int{7}, "status": "all"}), &list)
	if len(list.Assignments) != 3 {
		t.Fatal("expected three assignments")
	}
	preview := list.Assignments[0]
	if preview.ID != 5 || preview.Activity != "" || len(preview.ActivityAttachments) != 0 ||
		preview.Intro != string([]rune(full)[:2000])+" [truncated; call get_assignment for the full statement]" ||
		len(preview.Attachments) != 1 || preview.Attachments[0].Handle != introHandle {
		t.Fatal("unexpected list preview")
	}
	if list.Assignments[1].Intro != "Short" {
		t.Errorf("whitespace was not collapsed before truncation: %q", list.Assignments[1].Intro)
	}

	var plain aulaglobal.Assignment
	decode(call(t, session, "get_assignment", map[string]any{"assignment_id": 8}), &plain)
	if plain.ID != 8 || plain.Intro != "a < b and x <tag>" || plain.Activity != "" {
		t.Errorf("incorrect plain-text assignment: %+v", plain)
	}
	for _, check := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"assignment_id": 0}, "positive assignment_id"},
		{map[string]any{"assignment_id": 5, "course_id": -1}, "course_id must be positive"},
		{map[string]any{"assignment_id": 55}, "assignment not found"},
		{map[string]any{"assignment_id": 5, "course_id": 9}, "assignment not found"},
	} {
		res := call(t, session, "get_assignment", check.args)
		if !res.IsError || !strings.Contains(strings.Join(texts(res), " "), check.want) {
			t.Errorf("%v: want %q, got %v", check.args, check.want, texts(res))
		}
	}
}

func TestAssignmentHandleLimit(t *testing.T) {
	s := &Service{files: make(map[string]aulaglobal.File, maxHandles)}
	for i := 0; i < maxHandles; i++ {
		s.files[strconv.Itoa(i)] = aulaglobal.File{}
	}
	if err := s.rememberAssignmentFiles([]aulaglobal.File{{Handle: "0"}}); err != nil {
		t.Fatalf("existing handle: %v", err)
	}
	if err := s.rememberAssignmentFiles([]aulaglobal.File{{Handle: "new"}}); err == nil || len(s.files) != maxHandles {
		t.Fatal("assignment attachments exceeded the handle cache limit")
	}
}
