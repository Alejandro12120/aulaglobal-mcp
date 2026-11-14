package aulaglobal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAssignments(t *testing.T) {
	now := time.Now().Unix()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Method != "POST" {
			t.Error("not POST")
		}
		switch r.Form.Get("wsfunction") {
		case "core_course_get_enrolled_courses_by_timeline_classification":
			fmt.Fprint(w, `{"courses":[{"id":1,"fullname":"Álgebra 26/27 Algebra"}]}`)
		case "mod_assign_get_assignments":
			if r.Form.Get("courseids[0]") != "1" {
				t.Error("array form encoding missing")
			}
			if v := r.Form.Get("courseids[1]"); v != "" && v != "2" {
				t.Error("second course array encoding")
			}
			fmt.Fprintf(w, `{"courses":[{"id":1,"fullname":"Álgebra 26/27 Algebra","assignments":[
  {"id":1,"cmid":11,"name":"later","intro":"intro","duedate":%d,"introattachments":[{"filename":"guide.pdf","fileurl":"http://%s/pluginfile.php/1?token=secret"}]},
  {"id":2,"cmid":12,"name":"earlier","duedate":%d},
  {"id":3,"cmid":13,"name":"overdue","duedate":%d,"cutoffdate":%d},
  {"id":4,"cmid":14,"name":"no due"},
  {"id":5,"cmid":15,"name":"closed","cutoffdate":1}
  ]}]}`, now+3600, r.Host, now+1800, now-100, now-10)
		case "mod_assign_get_submission_status":
			if r.Form.Get("assignid") == "1" {
				fmt.Fprint(w, `{"errorcode":"invalidparameter","exception":"x"}`)
				return
			}
			fmt.Fprint(w, `{"lastattempt":{"submission":{"status":"submitted","timemodified":100},"gradingstatus":"graded"},"feedback":{"grade":{"grade":"9.0"}}}`)
		default:
			t.Errorf("unexpected WS %s", r.Form.Get("wsfunction"))
		}
	}))
	defer ts.Close()
	c := New(ts.URL, StaticToken{Value: "secret"})
	out, e := c.Assignments(context.Background(), AssignmentInput{IncludeSubmissionStatus: true, Language: "en"}, func(s string) string { return s })
	if e != nil || len(out.Assignments) != 3 {
		t.Fatalf("%+v %v", out, e)
	}
	a := out.Assignments
	if a[0].ID != 2 || a[1].ID != 1 || a[2].ID != 4 || a[0].CourseName != "Algebra" {
		t.Fatalf("%+v", a)
	}
	if a[0].Submission.Status != "submitted" || a[0].Submission.Grade != "9.0" || a[1].Submission.Error == "" {
		t.Fatalf("%+v", a)
	}
	if strings.Contains(a[1].Attachments[0].URL, "secret") || a[1].Attachments[0].Handle == "" {
		t.Fatal("attachment secret or missing handle")
	}
	for _, tc := range []struct {
		status string
		n      int
	}{{"all", 5}, {"overdue", 1}} {
		o, e := c.Assignments(context.Background(), AssignmentInput{CourseIDs: []int64{1, 2}, Status: tc.status}, func(s string) string { return s })
		if e != nil || len(o.Assignments) != tc.n {
			t.Fatalf("%s %+v %v", tc.status, o, e)
		}
	}
	for _, tc := range []struct {
		n int64
		s string
	}{{1767268800, "+01:00"}, {1782907200, "+02:00"}} {
		if v := stamp(tc.n); v.Madrid == nil || !strings.HasSuffix(*v.Madrid, tc.s) {
			t.Fatalf("%+v", v)
		}
	}
}
func TestStableRevisionHandles(t *testing.T) {
	revision := "1"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("wsfunction") == "core_course_get_courses_by_field" {
			fmt.Fprint(w, `{"courses":[{"id":1,"fullname":"Curso"}]}`)
			return
		}
		fmt.Fprintf(w, `[{"name":"Tema","modules":[{"id":2,"contents":[{"type":"file","filename":"a.pdf","filepath":"/folder/","fileurl":"http://%s/pluginfile.php/%s/a.pdf"}]}]}]`, r.Host, revision)
	}))
	defer ts.Close()
	c := New(ts.URL, StaticToken{Value: "secret"})
	a, e := c.Files(context.Background(), 1, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	revision = "2"
	b, e := c.Files(context.Background(), 1, nil, nil)
	if e != nil || a[0].Modules[0].Files[0].Handle != b[0].Modules[0].Files[0].Handle {
		t.Fatal("revision changed handle")
	}
}
func TestSecretReplaceMetadata(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	p := filepath.Join(dir, "token")
	if e := SaveSecret(p, "one", false); e != nil {
		t.Fatal(e)
	}
	if e := SaveSecret(p, "two", false); e == nil {
		t.Fatal("overwrite without force")
	}
	if e := SaveSecret(p, "two", true); e != nil {
		t.Fatal(e)
	}
	st, _ := os.Stat(p)
	b, _ := os.ReadFile(p)
	if (runtime.GOOS != "windows" && st.Mode().Perm() != 0600) || string(b) != "two\n" {
		t.Fatal("incorrect secret")
	}
	meta, _ := json.Marshal(map[string]string{"obtained_at": time.Now().Add(-48 * time.Hour).Format(time.RFC3339)})
	if e := SaveSecret(p+".meta.json", string(meta), true); e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"fullname":"User"}`) }))
	defer ts.Close()
	a, e := New(ts.URL, StaticToken{File: p}).Auth(context.Background())
	if e != nil || a.TokenObtainedAt == nil || a.TokenAgeDays == nil || *a.TokenAgeDays != 2 || a.ExpiresAt != nil {
		t.Fatalf("%+v %v", a, e)
	}
}

func TestIntroRuneLimitAndCanonicalNames(t *testing.T) {
	for _, text := range []string{strings.Repeat("a", 2500), strings.Repeat("界", 1500), strings.Repeat("界", 2500)} {
		t.Run(fmt.Sprint(len(text)), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				switch r.Form.Get("wsfunction") {
				case "mod_assign_get_assignments":
					fmt.Fprintf(w, `{"courses":[{"id":1,"fullname":"Álgebra 26/27 Algebra","assignments":[{"id":1,"intro":%q,"introattachments":[{"filename":"a.pdf","fileurl":"http://example/a.pdf"}]}]}]}`, text)
				case "core_course_get_contents":
					fmt.Fprint(w, `[]`)
				default:
					t.Error("unnecessary course name lookup")
					fmt.Fprint(w, `{"courses":[]}`)
				}
			}))
			defer ts.Close()
			c := New(ts.URL, StaticToken{Value: "secret"})
			out, e := c.Assignments(context.Background(), AssignmentInput{CourseIDs: []int64{1}, Status: "all", Language: "en"}, func(s string) string { return s })
			if e != nil {
				t.Fatal(e)
			}
			expected := text
			if len([]rune(text)) > 2000 {
				expected = string([]rune(text)[:2000]) + " [truncated]"
			}
			a := out.Assignments[0]
			if a.Intro != expected {
				t.Errorf("intro length %d, want %d", len([]rune(a.Intro)), len([]rune(expected)))
			}
			if a.CourseName != "Algebra" || a.Attachments[0].CourseName != "Álgebra" {
				t.Errorf("names: %+v", a)
			}
			c.Files(context.Background(), 1, nil, nil)
		})
	}
}
