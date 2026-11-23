package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"aulaglobal-mcp/internal/aulaglobal"
	"aulaglobal-mcp/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func solidPNG(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 3, 3))
	for x := 0; x < 3; x++ {
		for y := 0; y < 3; y++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// feedbackMoodle mimics the two web service calls behind get_assignment_feedback
// and serves the feedback files. Two different image.png files exist on purpose:
// one in the current feedback (comment image, itemid 900) and one in attempt 1
// (itemid 800), plus a third one in the file plugin of the current feedback.
type feedbackMoodle struct {
	mu          sync.Mutex
	status      string // submission status response
	assignments []string
	courseids   []string
	downloads   []string
}

var (
	currentImage  = solidPNG(color.RGBA{255, 0, 0, 255})
	attemptImage  = solidPNG(color.RGBA{0, 255, 0, 255})
	pluginImage   = solidPNG(color.RGBA{0, 0, 255, 255})
	feedbackFiles = map[string][]byte{
		"/webservice/pluginfile.php/10/assignfeedback_comments/feedback/900/image.png":      currentImage,
		"/webservice/pluginfile.php/10/assignfeedback_comments/feedback/800/image.png":      attemptImage,
		"/webservice/pluginfile.php/10/assignfeedback_file/feedback_files/900/image.png":    pluginImage,
		"/webservice/pluginfile.php/10/assignfeedback_file/feedback_files/900/report.pdf":   []byte("%PDF-1.4 report"),
		"/webservice/pluginfile.php/10/assignfeedback_editpdf/download/900/annotated.pdf":   []byte("%PDF-1.4 annotated"),
		"/webservice/pluginfile.php/10/assignfeedback_editpdf/combined/900/combined.pdf":    []byte("%PDF-1.4 combined"),
		"/webservice/pluginfile.php/10/assignfeedback_editpdf/pages/900/image_page0.png":    currentImage,
		"/webservice/pluginfile.php/10/assignfeedback_editpdf/tmp_x/900/image_page_tmp.png": currentImage,
	}
)

func fileJSON(host, path string, query string) string {
	name := path[strings.LastIndex(path, "/")+1:]
	mime := "application/octet-stream"
	switch {
	case strings.HasSuffix(name, ".png"):
		mime = "image/png"
	case strings.HasSuffix(name, ".pdf"):
		mime = "application/pdf"
	}
	return fmt.Sprintf(`{"filename":%q,"filepath":"/","filesize":%d,"fileurl":"http://%s%s%s","timemodified":1700000000,"mimetype":%q}`, name, len(feedbackFiles[path]), host, path, query, mime)
}

func (m *feedbackMoodle) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if strings.HasPrefix(r.URL.Path, "/webservice/pluginfile.php/") {
			if r.Form.Get("token") != "secret" {
				t.Errorf("download without token: %s", r.URL)
			}
			data, ok := feedbackFiles[r.URL.Path]
			m.mu.Lock()
			m.downloads = append(m.downloads, r.URL.Path)
			m.mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			if strings.HasSuffix(r.URL.Path, ".png") {
				w.Header().Set("Content-Type", "image/png")
			} else {
				w.Header().Set("Content-Type", "application/pdf")
			}
			w.Write(data)
			return
		}
		switch r.Form.Get("wsfunction") {
		case "mod_assign_get_assignments":
			m.mu.Lock()
			m.courseids = append(m.courseids, r.Form.Get("courseids[0]"))
			m.mu.Unlock()
			fmt.Fprint(w, `{"courses":[{"id":1,"fullname":"Álgebra 26/27 Algebra","assignments":[{"id":4,"cmid":44,"name":"Otra"},{"id":5,"cmid":55,"name":"Práctica 1"}]}]}`)
		case "mod_assign_get_submission_status":
			if r.Form.Get("assignid") != "5" {
				t.Errorf("assignid %q", r.Form.Get("assignid"))
			}
			fmt.Fprint(w, strings.ReplaceAll(m.status, "HOST", r.Host))
		case "core_course_get_courses_by_field":
			fmt.Fprint(w, `{"courses":[{"id":1,"fullname":"Álgebra 26/27 Algebra"}]}`)
		default:
			t.Errorf("unexpected WS %s", r.Form.Get("wsfunction"))
		}
	}
}

func commentsPlugin(t *testing.T, host, item, html string) string {
	path := "/webservice/pluginfile.php/10/assignfeedback_comments/feedback/" + item + "/image.png"
	return fmt.Sprintf(`{"type":"comments","name":"Feedback comments","fileareas":[{"area":"feedback","files":[%s]}],"editorfields":[{"name":"comments","description":"Feedback comments","text":%q,"format":1}]}`, fileJSON(host, path, "?forcedownload=1"), html)
}

func feedbackSession(t *testing.T, status string) (*mcp.ClientSession, *feedbackMoodle, string) {
	t.Helper()
	m := &feedbackMoodle{status: status}
	moodle := httptest.NewServer(m.handler(t))
	t.Cleanup(moodle.Close)
	api := aulaglobal.New(moodle.URL, aulaglobal.StaticToken{Value: "secret"})
	if err := api.SetMaxRPS(aulaglobal.MaxMaxRPS); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dataStore, err := store.Open(root, 1<<20, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dataStore.Root.Close() })
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := New(api, dataStore, nil).Connect(context.Background(), t1, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session, m, root
}

func TestFeedbackEndToEnd(t *testing.T) {
	status := fmt.Sprintf(`{"lastattempt":{},"feedback":{"grade":{"id":900,"grade":"75.65000"},"gradefordisplay":"<b>75,65</b> / 100,00","gradeddate":1767268800,"plugins":[%s,%s,%s]},
 "previousattempts":[{"attemptnumber":0,"grade":{"id":800,"grade":"-1.00000"},"feedbackplugins":[%s]}]}`,
		commentsPlugin(t, "HOST", "900", `<p>Good work</p><p>See <img src="http://HOST/webservice/pluginfile.php/10/assignfeedback_comments/feedback/900/image.png" alt="x"> and <img src="https://other.example/ext.png" alt="ext"></p><ul><li>a</li><li>b</li></ul>`),
		fmt.Sprintf(`{"type":"file","name":"Feedback files","fileareas":[{"area":"feedback_files","files":[%s,%s]}],"editorfields":[]}`,
			fileJSON("HOST", "/webservice/pluginfile.php/10/assignfeedback_file/feedback_files/900/report.pdf", ""),
			fileJSON("HOST", "/webservice/pluginfile.php/10/assignfeedback_file/feedback_files/900/image.png", "")),
		fmt.Sprintf(`{"type":"editpdf","name":"Annotate PDF","fileareas":[{"area":"download","files":[%s]},{"area":"combined","files":[%s]},{"area":"pages","files":[%s]},{"area":"tmp_x","files":[%s]}],"editorfields":[]}`,
			fileJSON("HOST", "/webservice/pluginfile.php/10/assignfeedback_editpdf/download/900/annotated.pdf", ""),
			fileJSON("HOST", "/webservice/pluginfile.php/10/assignfeedback_editpdf/combined/900/combined.pdf", ""),
			fileJSON("HOST", "/webservice/pluginfile.php/10/assignfeedback_editpdf/pages/900/image_page0.png", ""),
			fileJSON("HOST", "/webservice/pluginfile.php/10/assignfeedback_editpdf/tmp_x/900/image_page_tmp.png", "")),
		commentsPlugin(t, "HOST", "800", `<p>First try <img src="/webservice/pluginfile.php/10/assignfeedback_comments/feedback/800/image.png"></p>`))
	session, moodle, root := feedbackSession(t, status)

	res := call(t, session, "get_assignment_feedback", map[string]any{"assignment_id": 5})
	if res.IsError {
		t.Fatalf("%v", texts(res))
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out aulaglobal.FeedbackOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "token") || strings.Contains(string(raw), "http://") {
		t.Errorf("URLs or tokens leaked: %s", raw)
	}
	if out.AssignmentID != 5 || out.CMID != 55 || out.CourseID != 1 || out.CourseName != "Álgebra" || out.AssignmentName != "Práctica 1" || !out.Available || out.Note != "" {
		t.Fatalf("header: %+v", out)
	}
	if out.Grade != "75.65000" || out.GradeForDisplay != "75,65 / 100,00" || out.GradedDate.Unix != 1767268800 || out.GradedDate.Madrid == nil {
		t.Fatalf("grade: %+v", out)
	}
	if !strings.Contains(out.ContentNote, "third-party personal data") {
		t.Errorf("content note: %q", out.ContentNote)
	}
	if len(out.Comments) != 1 || out.Comments[0].Plugin != "comments" || out.Comments[0].Text != "Good work\n\nSee [image 1: image.png] and [image: ext]\n\n- a\n- b" {
		t.Fatalf("comments: %+v", out.Comments)
	}
	if len(out.PreviousAttempts) != 1 {
		t.Fatalf("attempts: %+v", out.PreviousAttempts)
	}
	prev := out.PreviousAttempts[0]
	if prev.AttemptNumber != 1 || prev.Grade != "" || len(prev.Comments) != 1 || prev.Comments[0].Text != "First try [image 2: image.png]" {
		t.Fatalf("previous attempt: %+v", prev)
	}
	// editpdf keeps only "download"; the junk areas never show up.
	var names []string
	handles := map[string]bool{}
	for _, f := range out.Files {
		names = append(names, f.Plugin+"/"+f.Area+"/"+f.Filename)
		handles[f.Handle] = true
		want := 0
		if f.Plugin == "comments" {
			want = 1
		}
		if f.ImageIndex != want {
			t.Errorf("image_index of %s/%s: %d", f.Plugin, f.Filename, f.ImageIndex)
		}
	}
	if got := strings.Join(names, " "); got != "comments/feedback/image.png file/feedback_files/report.pdf file/feedback_files/image.png editpdf/download/annotated.pdf" {
		t.Fatalf("files: %s", got)
	}
	if len(prev.Files) != 1 || prev.Files[0].ImageIndex != 2 || handles[prev.Files[0].Handle] {
		t.Fatalf("attempt files: %+v", prev.Files)
	}
	handles[prev.Files[0].Handle] = true
	if len(handles) != 5 {
		t.Fatalf("handles are not unique: %d", len(handles))
	}
	if moodle.courseids[0] != "" {
		t.Errorf("courseids sent without course_id: %q", moodle.courseids)
	}

	// Every handle works with download_files; same-named images do not collide.
	all := []string{}
	for h := range handles {
		all = append(all, h)
	}
	dl := call(t, session, "download_files", map[string]any{"handles": all})
	if dl.IsError {
		t.Fatalf("%v", texts(dl))
	}
	var results struct{ Results []store.Result }
	raw, _ = json.Marshal(dl.StructuredContent)
	json.Unmarshal(raw, &results)
	paths := map[string]bool{}
	for _, r := range results.Results {
		if r.Status != "downloaded" {
			t.Errorf("%+v", r)
		}
		paths[r.Path] = true
	}
	for _, want := range []string{
		"Álgebra/Entregas/Práctica 1/Feedback/image.png",
		"Álgebra/Entregas/Práctica 1/Feedback/image (2).png",
		"Álgebra/Entregas/Práctica 1/Feedback/report.pdf",
		"Álgebra/Entregas/Práctica 1/Feedback/annotated.pdf",
		"Álgebra/Entregas/Práctica 1/Feedback/Intento 1/image.png",
	} {
		if !paths[want] {
			t.Errorf("missing %s in %v", want, paths)
		}
	}
	for path, want := range map[string][]byte{
		"Álgebra/Entregas/Práctica 1/Feedback/Intento 1/image.png": attemptImage,
	} {
		if got, err := os.ReadFile(filepath.Join(root, path)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s content: %v", path, err)
		}
	}
	// The comment image (itemid 900) and the file-plugin image are different files.
	a, _ := os.ReadFile(filepath.Join(root, "Álgebra/Entregas/Práctica 1/Feedback/image.png"))
	b, _ := os.ReadFile(filepath.Join(root, "Álgebra/Entregas/Práctica 1/Feedback/image (2).png"))
	if bytes.Equal(a, b) || !(bytes.Equal(a, currentImage) || bytes.Equal(b, currentImage)) {
		t.Error("same-named files overwrote each other")
	}
	for _, d := range moodle.downloads {
		if strings.Contains(d, "/combined/") || strings.Contains(d, "/pages/") || strings.Contains(d, "tmp_") {
			t.Errorf("junk area downloaded: %s", d)
		}
	}

	// view_image returns the bytes intact and no structured content.
	view := call(t, session, "view_image", map[string]any{"path": "Álgebra/Entregas/Práctica 1/Feedback/Intento 1/image.png"})
	if view.IsError || view.StructuredContent != nil || len(view.Content) != 2 {
		t.Fatalf("view_image: %+v", view)
	}
	img, ok := view.Content[1].(*mcp.ImageContent)
	if !ok || img.MIMEType != "image/png" || !bytes.Equal(img.Data, attemptImage) {
		t.Fatalf("image content: %+v", view.Content[1])
	}
	if info := texts(view)[0]; !strings.Contains(info, "image/png") || !strings.Contains(info, fmt.Sprint(len(attemptImage))) || !strings.Contains(info, "Untrusted") {
		t.Errorf("info: %q", info)
	}
}

func TestFeedbackNotAvailableAndErrors(t *testing.T) {
	session, moodle, _ := feedbackSession(t, `{"lastattempt":{},"warnings":[]}`)
	res := call(t, session, "get_assignment_feedback", map[string]any{"assignment_id": 5, "course_id": 1})
	if res.IsError {
		t.Fatalf("%v", texts(res))
	}
	structured, _ := res.StructuredContent.(map[string]any)
	if structured["available"] != false || structured["note"] == "" || structured["grade"] != nil || structured["course_name"] != "Álgebra" {
		t.Fatalf("%v", structured)
	}
	if moodle.courseids[0] != "1" {
		t.Errorf("course_id not sent: %q", moodle.courseids)
	}
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"assignment_id": 99}, "assignment not found"},
		{map[string]any{"assignment_id": 0}, "positive"},
		{map[string]any{"assignment_id": 5, "course_id": -1}, "positive"},
	} {
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_assignment_feedback", Arguments: c.args})
		got := fmt.Sprint(err)
		if err == nil {
			got = strings.Join(texts(res), " ")
			if !res.IsError {
				t.Errorf("%v: no error", c.args)
			}
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%v: %q, want %q", c.args, got, c.want)
		}
	}
}

func TestToolSchemas(t *testing.T) {
	session := connect(t, nil)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		if tool.InputSchema == nil {
			t.Errorf("%s: no input schema", tool.Name)
		}
		// view_image returns an image, so a structured result would only get in the way.
		if (tool.OutputSchema == nil) != (tool.Name == "view_image") {
			t.Errorf("%s: output schema %v", tool.Name, tool.OutputSchema)
		}
		switch tool.Name {
		case "get_assignment_feedback":
			for _, want := range []string{"list_assignments", "download_files", "view_image", "feedback_viewed", "untrusted", "third-party"} {
				if !strings.Contains(tool.Description, want) {
					t.Errorf("get_assignment_feedback description lacks %q", want)
				}
			}
		case "view_image":
			if !strings.Contains(tool.Description, "download_files") {
				t.Errorf("view_image description: %s", tool.Description)
			}
		}
	}
	sort.Strings(names)
	if got := strings.Join(names, " "); got != "auth_status download_files get_assignment_feedback list_assignments list_course_files list_courses read_file search_files view_image" {
		t.Errorf("tools: %s", got)
	}
}

func TestViewImageRefusals(t *testing.T) {
	session, _, root := feedbackSession(t, `{}`)
	os.WriteFile(filepath.Join(root, "note.png"), []byte("not an image"), 0600)
	for path, want := range map[string]string{"note.png": "not a supported image", "../x.png": "relative", "missing.png": "no such file"} {
		res := call(t, session, "view_image", map[string]any{"path": path})
		if !res.IsError || !strings.Contains(strings.Join(texts(res), " "), want) {
			t.Errorf("%s: %+v", path, res)
		}
	}
}
