package aulaglobal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAssignments(t *testing.T) {
	now := time.Now().Unix()
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			if r.Form.Get("assignid") == "4" { // Moodle stores -1 when there is no grade
				fmt.Fprint(w, `{"lastattempt":{"submission":{"status":"new"},"gradingstatus":"notgraded"},"feedback":{"grade":{"grade":"-1.00000"}}}`)
				return
			}
			fmt.Fprint(w, `{"lastattempt":{"submission":{"status":"submitted","timemodified":100},"gradingstatus":"graded"},"feedback":{"grade":{"grade":"9.0"}}}`)
		default:
			t.Errorf("unexpected WS %s", r.Form.Get("wsfunction"))
		}
	}))
	defer moodle.Close()
	client := New(moodle.URL, StaticToken{Value: "secret"})
	out, err := client.Assignments(context.Background(), AssignmentInput{IncludeSubmissionStatus: true, Language: "en"}, func(s string) string { return s })
	if err != nil || len(out.Assignments) != 3 {
		t.Fatalf("%+v %v", out, err)
	}
	a := out.Assignments
	if a[0].ID != 2 || a[1].ID != 1 || a[2].ID != 4 || a[0].CourseName != "Algebra" {
		t.Fatalf("%+v", a)
	}
	if a[0].Submission.Status != "submitted" || a[0].Submission.Grade != "9.0" || a[1].Submission.Error == "" {
		t.Fatalf("%+v", a)
	}
	if a[2].Submission.Grade != "" || a[2].Submission.Status != "new" {
		t.Fatalf("ungraded -1 must be omitted: %+v", a[2].Submission)
	}
	if strings.Contains(a[1].Attachments[0].URL, "secret") || a[1].Attachments[0].Handle == "" {
		t.Fatal("attachment secret or missing handle")
	}
	for _, tc := range []struct {
		status string
		n      int
	}{{"all", 5}, {"overdue", 1}} {
		o, err := client.Assignments(context.Background(), AssignmentInput{CourseIDs: []int64{1, 2}, Status: tc.status}, func(s string) string { return s })
		if err != nil || len(o.Assignments) != tc.n {
			t.Fatalf("%s %+v %v", tc.status, o, err)
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
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("wsfunction") == "core_course_get_courses_by_field" {
			fmt.Fprint(w, `{"courses":[{"id":1,"fullname":"Curso"}]}`)
			return
		}
		fmt.Fprintf(w, `[{"name":"Tema","modules":[{"id":2,"contents":[{"type":"file","filename":"a.pdf","filepath":"/folder/","fileurl":"http://%s/pluginfile.php/%s/a.pdf"}]}]}]`, r.Host, revision)
	}))
	defer moodle.Close()
	client := New(moodle.URL, StaticToken{Value: "secret"})
	a, err := client.Files(context.Background(), 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	revision = "2"
	b, err := client.Files(context.Background(), 1, nil, nil)
	if err != nil || a[0].Modules[0].Files[0].Handle != b[0].Modules[0].Files[0].Handle {
		t.Fatal("revision changed handle")
	}
}
func TestSecretReplaceMetadata(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	p := filepath.Join(dir, "token")
	if err := SaveSecret(p, "one", false); err != nil {
		t.Fatal(err)
	}
	if err := SaveSecret(p, "two", false); err == nil {
		t.Fatal("overwrite without force")
	}
	if err := SaveSecret(p, "two", true); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	b, _ := os.ReadFile(p)
	if (runtime.GOOS != "windows" && st.Mode().Perm() != 0600) || string(b) != "two\n" {
		t.Fatal("incorrect secret")
	}
	meta, _ := json.Marshal(map[string]string{"obtained_at": time.Now().Add(-48 * time.Hour).Format(time.RFC3339)})
	if err := SaveSecret(p+".meta.json", string(meta), true); err != nil {
		t.Fatal(err)
	}
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"fullname":"User"}`) }))
	defer moodle.Close()
	a, err := New(moodle.URL, StaticToken{File: p}).Auth(context.Background())
	if err != nil || a.TokenObtainedAt == nil || a.TokenAgeDays == nil || *a.TokenAgeDays != 2 || a.ExpiresAt != nil {
		t.Fatalf("%+v %v", a, err)
	}
}

func TestIntroRuneLimitAndCanonicalNames(t *testing.T) {
	for _, text := range []string{strings.Repeat("a", 2500), strings.Repeat("界", 1500), strings.Repeat("界", 2500)} {
		t.Run(fmt.Sprint(len(text)), func(t *testing.T) {
			moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			defer moodle.Close()
			client := New(moodle.URL, StaticToken{Value: "secret"})
			out, err := client.Assignments(context.Background(), AssignmentInput{CourseIDs: []int64{1}, Status: "all", Language: "en"}, func(s string) string { return s })
			if err != nil {
				t.Fatal(err)
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
			client.Files(context.Background(), 1, nil, nil)
		})
	}
}

func TestAssignmentsAbortOnDeadToken(t *testing.T) {
	now := time.Now().Unix()
	for _, code := range []string{"invalidtoken", "accessexception"} {
		t.Run(code, func(t *testing.T) {
			var statusCalls atomic.Int32
			moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				switch r.Form.Get("wsfunction") {
				case "mod_assign_get_assignments":
					asg := []string{}
					for i := 1; i <= 30; i++ {
						asg = append(asg, fmt.Sprintf(`{"id":%d,"cmid":%d,"name":"a%d","duedate":%d}`, i, i, i, now+3600))
					}
					fmt.Fprintf(w, `{"courses":[{"id":1,"fullname":"X","assignments":[%s]}]}`, strings.Join(asg, ","))
				case "mod_assign_get_submission_status":
					statusCalls.Add(1)
					fmt.Fprintf(w, `{"exception":"x","errorcode":%q}`, code)
				case "core_webservice_get_site_info":
					fmt.Fprint(w, `{"exception":"x","errorcode":"invalidtoken"}`)
				}
			}))
			defer moodle.Close()
			client := New(moodle.URL, StaticToken{Value: "secret"})
			out, err := client.Assignments(context.Background(), AssignmentInput{CourseIDs: []int64{1}, IncludeSubmissionStatus: true}, func(s string) string { return s })
			if !errors.Is(err, ErrTokenExpired) || len(out.Assignments) != 0 {
				t.Fatalf("want single token error, got %v with %d assignments", err, len(out.Assignments))
			}
			if n := statusCalls.Load(); n > 8 {
				t.Fatalf("not short-circuited: %d status calls", n)
			}
		})
	}
}
