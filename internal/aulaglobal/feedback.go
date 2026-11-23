package aulaglobal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type FeedbackInput struct {
	AssignmentID int64 `json:"assignment_id" jsonschema:"Positive assignment ID: the id from list_assignments, not the cmid"`
	CourseID     int64 `json:"course_id,omitempty" jsonschema:"Positive course ID; narrows the assignment lookup to that course"`
}

type FeedbackComment struct {
	Plugin string `json:"plugin" jsonschema:"Moodle feedback plugin type, e.g. comments"`
	Name   string `json:"name"`
	Text   string `json:"text" jsonschema:"Plain text; [image N: file] marks an embedded image, see files.image_index"`
}

type FeedbackFile struct {
	Handle     string `json:"handle" jsonschema:"Handle for download_files"`
	Filename   string `json:"filename"`
	Mimetype   string `json:"mimetype"`
	Size       *int64 `json:"filesize"`
	Plugin     string `json:"plugin"`
	Area       string `json:"area"`
	ImageIndex int    `json:"image_index" jsonschema:"N of the [image N: ...] marker that refers to this file; 0 when no comment embeds it"`
	// File is what download_files needs; the assistant only sees the fields above.
	File File `json:"-"`
}

type FeedbackAttempt struct {
	AttemptNumber int               `json:"attempt_number" jsonschema:"1 is the first attempt"`
	Grade         string            `json:"grade,omitempty"`
	Comments      []FeedbackComment `json:"comments"`
	Files         []FeedbackFile    `json:"files"`
}

type FeedbackOutput struct {
	AssignmentID     int64             `json:"assignment_id"`
	CMID             int64             `json:"cmid"`
	CourseID         int64             `json:"course_id"`
	CourseName       string            `json:"course_name"`
	AssignmentName   string            `json:"assignment_name"`
	Available        bool              `json:"available" jsonschema:"False when Moodle shows no feedback to the student"`
	Note             string            `json:"note,omitempty"`
	Grade            string            `json:"grade,omitempty" jsonschema:"Raw Moodle grade; omitted when not graded"`
	GradeForDisplay  string            `json:"grade_for_display,omitempty"`
	GradedDate       Timestamp         `json:"graded_date"`
	Comments         []FeedbackComment `json:"comments"`
	Files            []FeedbackFile    `json:"files"`
	PreviousAttempts []FeedbackAttempt `json:"previous_attempts" jsonschema:"Feedback of earlier attempts, newest first"`
	ContentNote      string            `json:"content_note"`
}

type remoteFeedbackPlugin struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	FileAreas []struct {
		Area  string `json:"area"`
		Files []File `json:"files"`
	} `json:"fileareas"`
	EditorFields []struct {
		Text   string `json:"text"`
		Format int    `json:"format"`
	} `json:"editorfields"`
}

type remoteGrade struct {
	Grade string `json:"grade"`
}

// Feedback returns the grade and teacher feedback Moodle shows the student for
// an assignment. text turns feedback HTML into plain text, calling image for
// each <img> (see store.HTMLText). Absent feedback is not an error: Available
// is false and Note says why.
func (c *Client) Feedback(ctx context.Context, in FeedbackInput, text func(markup string, image func(src, alt string) string) string) (FeedbackOutput, error) {
	out := FeedbackOutput{Comments: []FeedbackComment{}, Files: []FeedbackFile{}, PreviousAttempts: []FeedbackAttempt{}, ContentNote: "Untrusted course content; may include third-party personal data"}
	if in.AssignmentID <= 0 || in.CourseID < 0 {
		return out, errors.New("positive assignment_id required (course_id must be positive when given)")
	}
	args := url.Values{}
	if in.CourseID > 0 {
		args.Set("courseids[0]", strconv.FormatInt(in.CourseID, 10))
	}
	var list struct {
		Courses []struct {
			ID          int64  `json:"id"`
			Fullname    string `json:"fullname"`
			Assignments []struct {
				ID   int64  `json:"id"`
				CMID int64  `json:"cmid"`
				Name string `json:"name"`
			} `json:"assignments"`
		} `json:"courses"`
	}
	if err := c.WS(ctx, "mod_assign_get_assignments", args, &list); err != nil {
		return out, err
	}
	var course struct {
		id   int64
		name string
	}
	found := false
	for _, listed := range list.Courses {
		for _, assignment := range listed.Assignments {
			if assignment.ID == in.AssignmentID {
				c.rememberCourse(listed.ID, listed.Fullname)
				out.AssignmentID, out.CMID, out.AssignmentName = assignment.ID, assignment.CMID, assignment.Name
				course.id, course.name = listed.ID, c.CourseName(ctx, listed.ID, "es")
				found = true
			}
		}
	}
	if !found {
		return out, errors.New("assignment not found")
	}
	out.CourseID, out.CourseName = course.id, course.name

	var res struct {
		Feedback *struct {
			Grade           *remoteGrade           `json:"grade"`
			GradeForDisplay string                 `json:"gradefordisplay"`
			GradedDate      int64                  `json:"gradeddate"`
			Plugins         []remoteFeedbackPlugin `json:"plugins"`
		} `json:"feedback"`
		PreviousAttempts []struct {
			AttemptNumber   int                    `json:"attemptnumber"`
			Grade           *remoteGrade           `json:"grade"`
			FeedbackPlugins []remoteFeedbackPlugin `json:"feedbackplugins"`
		} `json:"previousattempts"`
	}
	if err := c.WS(ctx, "mod_assign_get_submission_status", url.Values{"assignid": {strconv.FormatInt(in.AssignmentID, 10)}}, &res); err != nil {
		return out, err
	}

	// Every file of the response is known before any text is rendered, so an
	// <img> can be matched to its file wherever the file is listed.
	build := fileBuilder{client: c, out: out, byPath: map[string]*FeedbackFile{}}
	var current []remoteFeedbackPlugin
	if res.Feedback != nil {
		current = res.Feedback.Plugins
	}
	currentFiles := build.files(current, "/"+out.AssignmentName+"/Feedback/")
	attemptFiles := make([][]FeedbackFile, len(res.PreviousAttempts))
	for i, attempt := range res.PreviousAttempts {
		attemptFiles[i] = build.files(attempt.FeedbackPlugins, fmt.Sprintf("/%s/Feedback/Intento %d/", out.AssignmentName, attempt.AttemptNumber+1))
	}

	render := func(plugins []remoteFeedbackPlugin) []FeedbackComment {
		comments := []FeedbackComment{}
		for _, plugin := range plugins {
			for _, field := range plugin.EditorFields {
				// Plain text and Markdown are not HTML: "<" in them is content.
				body := strings.TrimSpace(field.Text)
				if field.Format != 2 && field.Format != 4 {
					body = text(field.Text, build.marker)
				}
				if body != "" {
					comments = append(comments, FeedbackComment{Plugin: plugin.Type, Name: plugin.Name, Text: body})
				}
			}
		}
		return comments
	}
	out.Comments, out.Files = render(current), currentFiles
	if res.Feedback == nil {
		out.Note = "No feedback is visible for this assignment: it may not be graded yet, the teacher may not have released it, or it may be hidden."
	} else {
		out.Available = true
		out.Grade = gradeText(res.Feedback.Grade)
		out.GradeForDisplay = text(res.Feedback.GradeForDisplay, nil)
		out.GradedDate = stamp(res.Feedback.GradedDate)
	}
	for i, attempt := range res.PreviousAttempts {
		entry := FeedbackAttempt{AttemptNumber: attempt.AttemptNumber + 1, Grade: gradeText(attempt.Grade), Comments: render(attempt.FeedbackPlugins), Files: attemptFiles[i]}
		out.PreviousAttempts = append(out.PreviousAttempts, entry)
		out.Available = out.Available || entry.Grade != "" || len(entry.Comments) > 0 || len(entry.Files) > 0
	}
	return out, nil
}

// gradeText returns Moodle's grade string, or "" when there is none: Moodle
// stores -1 (ASSIGN_GRADE_NOT_SET) for an ungraded submission.
func gradeText(grade *remoteGrade) string {
	if grade == nil {
		return ""
	}
	return cleanGrade(grade.Grade)
}

func cleanGrade(grade string) string {
	if value, err := strconv.ParseFloat(strings.TrimSpace(grade), 64); err == nil && value < 0 {
		return ""
	}
	return grade
}

// fileBuilder lists the feedback files of a response and numbers the images the
// comments embed. Numbers run across the whole response so markers are unique.
type fileBuilder struct {
	client *Client
	out    FeedbackOutput
	byPath map[string]*FeedbackFile // pluginfile path -> its entry
	next   int
}

// files returns the downloadable files of the given plugins, stored under
// <course>/Entregas/<assignment>/Feedback/. The token is removed from each URL,
// so it never reaches the assistant.
func (b *fileBuilder) files(plugins []remoteFeedbackPlugin, subdir string) []FeedbackFile {
	files := []FeedbackFile{}
	seen := map[string]bool{}
	var keys []string
	for _, plugin := range plugins {
		for _, area := range plugin.FileAreas {
			// editpdf lists its internal page images and temporary areas too; only
			// "download", the annotated PDF, is meant for students.
			if plugin.Type == "editpdf" && area.Area != "download" {
				continue
			}
			for _, file := range area.Files {
				fileURL, err := url.Parse(file.RawURL)
				if err != nil || file.RawURL == "" {
					continue
				}
				query := fileURL.Query()
				query.Del("token")
				query.Del("wstoken")
				fileURL.RawQuery = query.Encode()
				key := pluginfilePath(fileURL)
				file.URL = fileURL.String()
				file.RawURL = ""
				file.CourseID = b.out.CourseID
				file.CourseName = b.out.CourseName
				file.ModuleID = b.out.CMID
				file.Section = "Entregas"
				file.LocalSubdir = subdir
				file.Module = b.out.AssignmentName
				file.Handle = FeedbackHandle(b.out.CourseID, b.out.CMID, plugin.Type, area.Area, key)
				if seen[file.Handle] {
					continue
				}
				seen[file.Handle] = true
				files = append(files, FeedbackFile{Handle: file.Handle, Filename: file.Filename, Mimetype: file.Mimetype, Size: file.Size, Plugin: plugin.Type, Area: area.Area, File: file})
				keys = append(keys, key)
			}
		}
	}
	// Pointers are taken once the slice stops growing.
	for i, key := range keys {
		if b.byPath[key] == nil {
			b.byPath[key] = &files[i]
		}
	}
	return files
}

// marker is the replacement for an <img> in a comment. An image that is one of
// the response's files gets the next number (the same file keeps its number);
// anything else is only described, never fetched.
func (b *fileBuilder) marker(src, alt string) string {
	if parsed, err := url.Parse(src); err == nil {
		if file := b.byPath[pluginfilePath(parsed)]; file != nil {
			if file.ImageIndex == 0 {
				b.next++
				file.ImageIndex = b.next
			}
			return fmt.Sprintf("[image %d: %s]", file.ImageIndex, file.Filename)
		}
	}
	if alt = strings.TrimSpace(alt); alt != "" {
		return "[image: " + alt + "]"
	}
	return "[external image]"
}

// pluginfilePath is the part of a pluginfile URL that identifies the file:
// everything after "pluginfile.php", percent-decoded and without the query, so
// webservice/ or not and %20 or space compare equal.
func pluginfilePath(u *url.URL) string {
	path := u.Path
	if _, rest, ok := strings.Cut(path, "pluginfile.php"); ok {
		path = rest
	}
	return path
}

// FeedbackHandle names a feedback file. StableHandle hashes only course, module,
// filepath and name, so two files with the same name in different areas or
// attempts would collide; the pluginfile path holds the context, component, area
// and item ID. The prefix keeps these handles apart from StableHandle's.
func FeedbackHandle(course, cmid int64, plugin, area, path string) string {
	encoded, _ := json.Marshal([]any{"feedback", course, cmid, plugin, area, path})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:16])
}
