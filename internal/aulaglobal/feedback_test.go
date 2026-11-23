package aulaglobal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var testImg = regexp.MustCompile(`<img src="([^"]*)"[^>]*>`)

// testText is a stand-in for store.HTMLText, which this package cannot import.
func testText(markup string, image func(src, alt string) string) string {
	return testImg.ReplaceAllStringFunc(markup, func(tag string) string {
		if image == nil {
			return ""
		}
		return image(testImg.FindStringSubmatch(tag)[1], "")
	})
}

func feedbackClient(t *testing.T, status string) *Client {
	t.Helper()
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.Form.Get("wsfunction") {
		case "mod_assign_get_assignments":
			fmt.Fprint(w, `{"courses":[{"id":1,"fullname":"Álgebra 26/27 Algebra","assignments":[{"id":5,"cmid":55,"name":"Tarea"}]}]}`)
		case "mod_assign_get_submission_status":
			fmt.Fprint(w, strings.ReplaceAll(status, "HOST", r.Host))
		default:
			t.Errorf("unexpected WS %s", r.Form.Get("wsfunction"))
		}
	}))
	t.Cleanup(moodle.Close)
	return New(moodle.URL, StaticToken{Value: "secret"})
}

func TestFeedbackMatchesEncodedImagesAndKeepsHandlesApart(t *testing.T) {
	file := func(item string) string {
		return fmt.Sprintf(`{"filename":"my pic.png","filepath":"/","filesize":3,"fileurl":"http://HOST/webservice/pluginfile.php/10/assignfeedback_comments/feedback/%s/my%%20pic.png?forcedownload=1&token=leak","mimetype":"image/png"}`, item)
	}
	// The same file name in the current feedback and in attempt 2, with the
	// <img> written with a literal space, without "webservice/", and relative.
	status := fmt.Sprintf(`{"feedback":{"grade":{"grade":"0.00000"},"plugins":[{"type":"comments","name":"C","fileareas":[{"area":"feedback","files":[%s]}],"editorfields":[{"text":"<img src=\"http://HOST/pluginfile.php/10/assignfeedback_comments/feedback/9/my pic.png\"> <img src=\"/webservice/pluginfile.php/10/assignfeedback_comments/feedback/9/my%%20pic.png?x=1\">","format":1}]}]},
"previousattempts":[{"attemptnumber":1,"grade":{"grade":"5.00000"},"feedbackplugins":[{"type":"comments","name":"C","fileareas":[{"area":"feedback","files":[%s,%s]}],"editorfields":[{"text":"<img src=\"/webservice/pluginfile.php/10/assignfeedback_comments/feedback/4/my%%20pic.png\">","format":1}]}]}]}`, file("9"), file("4"), file("4"))
	out, err := feedbackClient(t, status).Feedback(context.Background(), FeedbackInput{AssignmentID: 5}, testText)
	if err != nil {
		t.Fatal(err)
	}
	if out.Grade != "0.00000" || len(out.Comments) != 1 || out.Comments[0].Text != "[image 1: my pic.png] [image 1: my pic.png]" {
		t.Fatalf("%+v", out)
	}
	prev := out.PreviousAttempts[0]
	if prev.AttemptNumber != 2 || prev.Grade != "5.00000" || prev.Comments[0].Text != "[image 2: my pic.png]" || len(prev.Files) != 1 {
		t.Fatalf("%+v", prev)
	}
	if out.Files[0].Handle == prev.Files[0].Handle || out.Files[0].ImageIndex != 1 || prev.Files[0].ImageIndex != 2 {
		t.Fatalf("handles or indexes: %+v %+v", out.Files, prev.Files)
	}
	got := out.Files[0].File
	if strings.Contains(got.URL, "token") || got.Section != "Entregas" || got.LocalSubdir != "/Tarea/Feedback/" || prev.Files[0].File.LocalSubdir != "/Tarea/Feedback/Intento 2/" || got.CourseName != "Álgebra" {
		t.Fatalf("file: %+v", got)
	}
	if h := StableHandle(1, 55, "/", "my pic.png"); h == out.Files[0].Handle || h == prev.Files[0].Handle {
		t.Error("feedback handle equals a StableHandle")
	}
}

func TestFeedbackOnlyInPreviousAttemptAndMissingAssignment(t *testing.T) {
	status := `{"previousattempts":[{"attemptnumber":0,"grade":null,"feedbackplugins":[{"type":"comments","name":"C","editorfields":[{"text":"old","format":2}]}]}]}`
	client := feedbackClient(t, status)
	out, err := client.Feedback(context.Background(), FeedbackInput{AssignmentID: 5, CourseID: 1}, testText)
	if err != nil || !out.Available || out.Note == "" || out.Grade != "" || len(out.PreviousAttempts) != 1 || out.PreviousAttempts[0].Comments[0].Text != "old" {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := client.Feedback(context.Background(), FeedbackInput{AssignmentID: 6}, testText); err == nil || err.Error() != "assignment not found" {
		t.Fatalf("%v", err)
	}
	for _, bad := range []FeedbackInput{{}, {AssignmentID: -1}, {AssignmentID: 1, CourseID: -2}} {
		if _, err := client.Feedback(context.Background(), bad, testText); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestCleanGrade(t *testing.T) {
	for in, want := range map[string]string{"-1.00000": "", "-1": "", "0.00000": "0.00000", "75.65000": "75.65000", "": "", "A": "A", "-": "-"} {
		if got := cleanGrade(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
