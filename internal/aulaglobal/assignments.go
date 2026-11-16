package aulaglobal

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
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
type Timestamp struct {
	Unix   int64   `json:"unix"`
	Madrid *string `json:"rfc3339"`
}

func stamp(n int64) Timestamp {
	t := Timestamp{Unix: n}
	if n > 0 {
		loc, _ := time.LoadLocation("Europe/Madrid")
		s := time.Unix(n, 0).In(loc).Format(time.RFC3339)
		t.Madrid = &s
	}
	return t
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
	IntroNote           string      `json:"intro_note"`
	Allows              Timestamp   `json:"allowsubmissionsfromdate"`
	Due                 Timestamp   `json:"duedate"`
	Cutoff              Timestamp   `json:"cutoffdate"`
	GradingDue          Timestamp   `json:"gradingduedate"`
	SubmissionsPossible bool        `json:"submissions_possible"`
	Attachments         []File      `json:"introattachments"`
	Submission          *Submission `json:"submission,omitempty"`
}
type AssignmentsOutput struct {
	Assignments []Assignment `json:"assignments"`
}
type remoteAssignment struct {
	ID            int64  `json:"id"`
	CMID          int64  `json:"cmid"`
	Name          string `json:"name"`
	Intro         string `json:"intro"`
	Allows        int64  `json:"allowsubmissionsfromdate"`
	Due           int64  `json:"duedate"`
	Cutoff        int64  `json:"cutoffdate"`
	GradingDue    int64  `json:"gradingduedate"`
	NoSubmissions int    `json:"nosubmissions"`
	Attachments   []File `json:"introattachments"`
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
		courses, e := c.Courses(ctx, "inprogress", in.Language)
		if e != nil {
			return out, e
		}
		for _, v := range courses {
			in.CourseIDs = append(in.CourseIDs, v.ID)
			names[v.ID] = v.Name
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
	var res struct {
		Courses []struct {
			ID          int64              `json:"id"`
			Fullname    string             `json:"fullname"`
			Assignments []remoteAssignment `json:"assignments"`
		} `json:"courses"`
	}
	if e := c.WS(ctx, "mod_assign_get_assignments", args, &res); e != nil {
		return out, e
	}
	now := time.Now().Unix()
	for _, course := range res.Courses {
		c.rememberCourse(course.ID, course.Fullname)
		localName := c.CourseName(ctx, course.ID, "es")
		name := names[course.ID]
		if name == "" {
			name = CleanName(course.Fullname, in.Language)
		}
		if name == "" {
			name = c.CourseName(ctx, course.ID, in.Language)
		}
		for _, a := range course.Assignments {
			possible := a.NoSubmissions == 0 && (a.Allows == 0 || a.Allows <= now) && (a.Cutoff == 0 || a.Cutoff > now)
			if in.Status == "upcoming" && !((a.Due > now) || (a.Due == 0 && possible)) {
				continue
			}
			if in.Status == "overdue" && !(a.Due > 0 && a.Due <= now) {
				continue
			}
			v := Assignment{ID: a.ID, CMID: a.CMID, CourseID: course.ID, CourseName: name, Name: a.Name, Intro: truncateIntro(plain(a.Intro)), IntroNote: "Untrusted course content", Allows: stamp(a.Allows), Due: stamp(a.Due), Cutoff: stamp(a.Cutoff), GradingDue: stamp(a.GradingDue), SubmissionsPossible: possible}
			v.Attachments = introAttachments(a, course.ID, localName)
			out.Assignments = append(out.Assignments, v)
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

// truncateIntro keeps the first 2000 characters of an assignment intro.
func truncateIntro(intro string) string {
	if runes := []rune(intro); len(runes) > 2000 {
		return string(runes[:2000]) + " [truncated]"
	}
	return intro
}

// introAttachments turns the files attached to an assignment into downloadable
// files stored under <course>/Entregas/<assignment>/. The token is removed from
// each URL, so it never reaches the assistant.
func introAttachments(a remoteAssignment, courseID int64, courseName string) []File {
	files := []File{}
	for _, f := range a.Attachments {
		u, e := url.Parse(f.RawURL)
		if e != nil {
			continue
		}
		q := u.Query()
		q.Del("token")
		q.Del("wstoken")
		u.RawQuery = q.Encode()
		f.URL = u.String()
		f.RawURL = ""
		f.CourseID = courseID
		f.CourseName = courseName
		f.ModuleID = a.CMID
		f.Section = "Entregas"
		f.LocalSubdir = "/" + a.Name + "/"
		f.Module = a.Name
		f.Handle = StableHandle(courseID, a.CMID, f.Filepath, f.Filename)
		files = append(files, f)
	}
	return files
}

// sortByDue orders assignments by due date, with the ones without a date last.
func sortByDue(list []Assignment) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i].Due.Unix, list[j].Due.Unix
		if a == 0 {
			return false
		}
		return b == 0 || a < b
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
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				select {
				case <-stop:
					continue
				default:
				}
				a := &list[i]
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
				e := c.WS(ctx, "mod_assign_get_submission_status", url.Values{"assignid": {strconv.FormatInt(a.ID, 10)}}, &res)
				a.Submission = &Submission{Status: res.LastAttempt.Submission.Status, Modified: res.LastAttempt.Submission.Modified, GradingStatus: res.LastAttempt.GradingStatus, Grade: res.Feedback.Grade.Grade}
				if errors.Is(e, ErrTokenExpired) || errors.Is(e, ErrRateLimited) {
					once.Do(func() { dead = e; close(stop) })
					continue
				}
				if e != nil {
					a.Submission.Error = e.Error()
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
