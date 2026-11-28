package aulaglobal

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

type AssignmentInput struct {
	CourseIDs               []int64 `json:"course_ids,omitempty"`
	Status                  string  `json:"status,omitempty" jsonschema:"upcoming (default), overdue, all"`
	IncludeSubmissionStatus bool    `json:"include_submission_status,omitempty"`
	Language                string  `json:"language,omitempty" jsonschema:"es (default) or en"`
}
type AssignmentDetailInput struct {
	AssignmentID int64 `json:"assignment_id" jsonschema:"Positive assignment id from list_assignments (not cmid)"`
	CourseID     int64 `json:"course_id,omitempty" jsonschema:"Positive course ID to narrow the lookup"`
}
type Timestamp struct {
	Unix   int64   `json:"unix"`
	Madrid *string `json:"rfc3339"`
}

func stamp(unix int64) Timestamp {
	timestamp := Timestamp{Unix: unix}
	if unix > 0 {
		madrid, _ := time.LoadLocation("Europe/Madrid")
		formatted := time.Unix(unix, 0).In(madrid).Format(time.RFC3339)
		timestamp.Madrid = &formatted
	}
	return timestamp
}

type Submission struct {
	Status        string `json:"status"`
	GradingStatus string `json:"grading_status"`
	Modified      int64  `json:"timemodified"`
	Grade         string `json:"grade,omitempty"`
	Error         string `json:"error,omitempty"`
}
type Assignment struct {
	ID                  int64       `json:"id"`
	CMID                int64       `json:"cmid"`
	CourseID            int64       `json:"course_id"`
	CourseName          string      `json:"course_name"`
	Name                string      `json:"name"`
	Intro               string      `json:"intro"`
	Activity            string      `json:"activity,omitempty"`
	IntroNote           string      `json:"intro_note"`
	Allows              Timestamp   `json:"allowsubmissionsfromdate"`
	Due                 Timestamp   `json:"duedate"`
	Cutoff              Timestamp   `json:"cutoffdate"`
	GradingDue          Timestamp   `json:"gradingduedate"`
	SubmissionsPossible bool        `json:"submissions_possible"`
	Attachments         []File      `json:"introattachments"`
	ActivityAttachments []File      `json:"activityattachments,omitempty"`
	Submission          *Submission `json:"submission,omitempty"`
}
type AssignmentsOutput struct {
	Assignments []Assignment `json:"assignments"`
}
type remoteAssignment struct {
	ID                  int64  `json:"id"`
	CMID                int64  `json:"cmid"`
	Name                string `json:"name"`
	Intro               string `json:"intro"`
	IntroFormat         int    `json:"introformat"`
	Activity            string `json:"activity"`
	ActivityFormat      int    `json:"activityformat"`
	Allows              int64  `json:"allowsubmissionsfromdate"`
	Due                 int64  `json:"duedate"`
	Cutoff              int64  `json:"cutoffdate"`
	GradingDue          int64  `json:"gradingduedate"`
	NoSubmissions       int    `json:"nosubmissions"`
	Attachments         []File `json:"introattachments"`
	ActivityAttachments []File `json:"activityattachments"`
}

// Assignments lists the assignments of the given courses, or of the courses in
// progress when none are given. plain turns the HTML intro into text.
func (c *Client) Assignments(ctx context.Context, in AssignmentInput, plain func(string) string) (AssignmentsOutput, error) {
	out := AssignmentsOutput{Assignments: []Assignment{}}
	if in.Status == "" {
		in.Status = "upcoming"
	}
	if in.Status != "upcoming" && in.Status != "overdue" && in.Status != "all" {
		return out, fmt.Errorf("status must be upcoming/overdue/all")
	}
	if in.Language != "" && in.Language != "es" && in.Language != "en" {
		return out, fmt.Errorf("language must be es/en")
	}
	names := map[int64]string{}
	if len(in.CourseIDs) == 0 {
		courses, err := c.Courses(ctx, "inprogress", in.Language)
		if err != nil {
			return out, err
		}
		for _, course := range courses {
			in.CourseIDs = append(in.CourseIDs, course.ID)
			names[course.ID] = course.Name
		}
	}
	if len(in.CourseIDs) > 1000 {
		return out, fmt.Errorf("course limit: 1000")
	}
	if len(in.CourseIDs) == 0 {
		return out, nil
	}
	args := url.Values{}
	for i, id := range in.CourseIDs {
		if id <= 0 {
			return out, fmt.Errorf("positive course IDs required")
		}
		args.Set(fmt.Sprintf("courseids[%d]", i), strconv.FormatInt(id, 10))
	}
	courses, err := c.assignmentCourses(ctx, args)
	if err != nil {
		return out, err
	}
	now := time.Now().Unix()
	for _, course := range courses {
		c.rememberCourse(course.ID, course.Fullname)
		localName := c.CourseName(ctx, course.ID, "es")
		name := names[course.ID]
		if name == "" {
			name = CleanName(course.Fullname, in.Language)
		}
		if name == "" {
			name = c.CourseName(ctx, course.ID, in.Language)
		}
		for _, remote := range course.Assignments {
			possible := remote.submissionsPossible(now)
			if in.Status == "upcoming" && !((remote.Due > now) || (remote.Due == 0 && possible)) {
				continue
			}
			if in.Status == "overdue" && !(remote.Due > 0 && remote.Due <= now) {
				continue
			}
			assignment := remote.summary(course.ID, name, localName, possible)
			assignment.Intro = truncateIntro(assignmentText(remote.Intro, remote.IntroFormat, plain))
			out.Assignments = append(out.Assignments, assignment)
		}
	}
	sortByDue(out.Assignments)
	if in.IncludeSubmissionStatus {
		if dead := c.addSubmissionStatus(ctx, out.Assignments); dead != nil {
			return AssignmentsOutput{Assignments: []Assignment{}}, dead
		}
	}
	return out, nil
}

type assignmentCourse struct {
	ID          int64              `json:"id"`
	Fullname    string             `json:"fullname"`
	Assignments []remoteAssignment `json:"assignments"`
}

func (c *Client) assignmentCourses(ctx context.Context, args url.Values) ([]assignmentCourse, error) {
	var res struct {
		Courses []assignmentCourse `json:"courses"`
	}
	err := c.WS(ctx, "mod_assign_get_assignments", args, &res)
	return res.Courses, err
}

// AssignmentDetail returns the full statement Moodle exposes for one assignment.
func (c *Client) AssignmentDetail(ctx context.Context, in AssignmentDetailInput, plain func(string) string) (Assignment, error) {
	if in.AssignmentID <= 0 || in.CourseID < 0 {
		return Assignment{}, errors.New("positive assignment_id required (course_id must be positive when given)")
	}
	args := url.Values{}
	if in.CourseID > 0 {
		args.Set("courseids[0]", strconv.FormatInt(in.CourseID, 10))
	}
	courses, err := c.assignmentCourses(ctx, args)
	if err != nil {
		return Assignment{}, err
	}
	for _, course := range courses {
		if in.CourseID > 0 && course.ID != in.CourseID {
			continue
		}
		for _, remote := range course.Assignments {
			if remote.ID != in.AssignmentID {
				continue
			}
			c.rememberCourse(course.ID, course.Fullname)
			name := c.CourseName(ctx, course.ID, "es")
			out := remote.summary(course.ID, name, name, remote.submissionsPossible(time.Now().Unix()))
			out.Intro = assignmentText(remote.Intro, remote.IntroFormat, plain)
			out.Activity = assignmentText(remote.Activity, remote.ActivityFormat, plain)
			out.ActivityAttachments = assignmentAttachments(remote, remote.ActivityAttachments, course.ID, name, "Actividad/")
			return out, nil
		}
	}
	return Assignment{}, errors.New("assignment not found")
}

func (a remoteAssignment) submissionsPossible(now int64) bool {
	return a.NoSubmissions == 0 && (a.Allows == 0 || a.Allows <= now) && (a.Cutoff == 0 || a.Cutoff > now)
}

func (a remoteAssignment) summary(courseID int64, name, localName string, possible bool) Assignment {
	return Assignment{
		ID: a.ID, CMID: a.CMID, CourseID: courseID, CourseName: name, Name: a.Name,
		IntroNote: "Untrusted course content",
		Allows:    stamp(a.Allows), Due: stamp(a.Due), Cutoff: stamp(a.Cutoff), GradingDue: stamp(a.GradingDue),
		SubmissionsPossible: possible,
		Attachments:         assignmentAttachments(a, a.Attachments, courseID, localName, ""),
	}
}

func assignmentText(markup string, format int, plain func(string) string) string {
	// Moodle's plain text and Markdown can contain literal angle brackets.
	if format == 2 || format == 4 {
		return strings.TrimSpace(markup)
	}
	return plain(markup)
}

// truncateIntro keeps the first 2000 characters of an assignment intro.
func truncateIntro(intro string) string {
	if runes := []rune(intro); len(runes) > 2000 {
		return string(runes[:2000]) + " [truncated; call get_assignment for the full statement]"
	}
	return intro
}

// assignmentAttachments makes files downloadable under the assignment folder
// and strips tokens from their URLs before passing them to the assistant.
func assignmentAttachments(assignment remoteAssignment, attachments []File, courseID int64, courseName, subfolder string) []File {
	files := []File{}
	for _, file := range attachments {
		fileURL, err := url.Parse(file.RawURL)
		if err != nil {
			continue
		}
		query := fileURL.Query()
		query.Del("token")
		query.Del("wstoken")
		fileURL.RawQuery = query.Encode()
		file.URL = fileURL.String()
		file.RawURL = ""
		file.CourseID = courseID
		file.CourseName = courseName
		file.ModuleID = assignment.CMID
		file.Section = "Entregas"
		file.LocalSubdir = "/" + assignment.Name + "/" + subfolder
		file.Module = assignment.Name
		file.Handle = StableHandle(courseID, assignment.CMID, subfolder+file.Filepath, file.Filename)
		files = append(files, file)
	}
	return files
}

// sortByDue orders assignments by due date, with the ones without a date last.
func sortByDue(list []Assignment) {
	sort.SliceStable(list, func(i, j int) bool {
		first, second := list[i].Due.Unix, list[j].Due.Unix
		if first == 0 {
			return false
		}
		return second == 0 || first < second
	})
}

// addSubmissionStatus asks Moodle for the submission status of each assignment,
// with four workers and at most 100 calls. A dead token (or AulaGlobal throttling
// us after retries) fails every call the same way, so it stops the whole batch
// and is returned once. Any other error is recorded on its assignment.
func (c *Client) addSubmissionStatus(ctx context.Context, list []Assignment) error {
	jobs := make(chan int)
	stop := make(chan struct{})
	var once sync.Once
	var dead error
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				select {
				case <-stop:
					continue
				default:
				}
				assignment := &list[i]
				var res struct {
					LastAttempt struct {
						Submission struct {
							Status   string `json:"status"`
							Modified int64  `json:"timemodified"`
						} `json:"submission"`
						GradingStatus string `json:"gradingstatus"`
					} `json:"lastattempt"`
					Feedback struct {
						Grade struct {
							Grade string `json:"grade"`
						} `json:"grade"`
					} `json:"feedback"`
				}
				err := c.WS(ctx, "mod_assign_get_submission_status", url.Values{"assignid": {strconv.FormatInt(assignment.ID, 10)}}, &res)
				assignment.Submission = &Submission{Status: res.LastAttempt.Submission.Status, Modified: res.LastAttempt.Submission.Modified, GradingStatus: res.LastAttempt.GradingStatus, Grade: cleanGrade(res.Feedback.Grade.Grade)}
				if errors.Is(err, ErrTokenExpired) || errors.Is(err, ErrRateLimited) {
					once.Do(func() { dead = err; close(stop) })
					continue
				}
				if err != nil {
					assignment.Submission.Error = err.Error()
				}
			}
		}()
	}
	for i := range list {
		if i >= 100 {
			list[i].Submission = &Submission{Error: "submission status call limit: 100"}
			continue
		}
		select {
		case jobs <- i:
		case <-stop:
		}
	}
	close(jobs)
	wg.Wait()
	return dead
}
