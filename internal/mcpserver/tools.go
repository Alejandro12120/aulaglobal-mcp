package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"aulaglobal-mcp/internal/aulaglobal"
	"aulaglobal-mcp/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Each tool is an input type, an output type and a handler method. The
// jsonschema tags become the tool's input schema.

type Empty struct{}

// AuthOutput is the auth_status result. It is a separate type from
// aulaglobal.Auth on purpose: the account holder's name and user ID are
// decoded from Moodle for local use (the setup greeting, the enrolment
// fallback) and must never reach the assistant.
type AuthOutput struct {
	TokenValid      bool    `json:"token_valid" jsonschema:"True when Moodle accepted the token; an invalid token is reported as an error"`
	Sitename        string  `json:"sitename"`
	Release         string  `json:"release"`
	ExpiresAt       *int64  `json:"expires_at"`
	TokenObtainedAt *string `json:"token_obtained_at,omitempty"`
	TokenAgeDays    *int    `json:"token_age_days,omitempty"`
	ExpirySource    string  `json:"expiry_source"`
	UpdateAvailable string  `json:"update_available,omitempty" jsonschema:"Newer aulaglobal-mcp version available, when known"`
}

func (s *Service) authStatus(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, AuthOutput, error) {
	auth, err := s.API.Auth(ctx)
	out := AuthOutput{TokenValid: err == nil, Sitename: auth.Sitename, Release: auth.Release, ExpiresAt: auth.ExpiresAt, TokenObtainedAt: auth.TokenObtainedAt, TokenAgeDays: auth.TokenAgeDays, ExpirySource: auth.ExpirySource}
	if s.updates != nil {
		out.UpdateAvailable = s.updates.Available()
	}
	return nil, out, err
}

type CoursesInput struct {
	Classification string `json:"classification,omitempty" jsonschema:"all/inprogress/past/future; default inprogress; fallback on older Moodle returns all enrollments"`
	Language       string `json:"language,omitempty" jsonschema:"es or en; default es"`
}
type CoursesOutput struct {
	Courses []aulaglobal.Course `json:"courses"`
}

func (s *Service) listCourses(ctx context.Context, _ *mcp.CallToolRequest, in CoursesInput) (*mcp.CallToolResult, CoursesOutput, error) {
	courses, err := s.API.Courses(ctx, in.Classification, in.Language)
	return nil, CoursesOutput{courses}, err
}

type FilesInput struct {
	CourseID int64    `json:"course_id" jsonschema:"Positive Moodle course ID"`
	Include  []string `json:"include,omitempty" jsonschema:"Allowed extensions e.g. pdf; empty means all"`
	Exclude  []string `json:"exclude,omitempty" jsonschema:"Excluded extensions; always applied"`
}
type FilesOutput struct {
	Sections []aulaglobal.Section `json:"sections"`
}

func (s *Service) listCourseFiles(ctx context.Context, _ *mcp.CallToolRequest, in FilesInput) (*mcp.CallToolResult, FilesOutput, error) {
	sections, err := s.catalog(ctx, in.CourseID, in.Include, in.Exclude)
	return nil, FilesOutput{sections}, err
}

type DownloadInput struct {
	Handles  []string `json:"handles,omitempty" jsonschema:"Handles from list_course_files in this server process"`
	CourseID int64    `json:"course_id,omitempty" jsonschema:"Download whole course instead of handles"`
}
type DownloadOutput struct {
	Results []store.Result `json:"results"`
}

func (s *Service) downloadFiles(ctx context.Context, _ *mcp.CallToolRequest, in DownloadInput) (*mcp.CallToolResult, DownloadOutput, error) {
	if (len(in.Handles) == 0) == (in.CourseID == 0) || in.CourseID < 0 || len(in.Handles) > 10000 {
		return nil, DownloadOutput{}, errors.New("provide handles (max 10000) OR positive course_id")
	}
	var files []aulaglobal.File
	var err error
	if in.CourseID > 0 {
		files, err = s.courseFiles(ctx, in.CourseID)
	} else {
		files, err = s.filesByHandle(in.Handles)
	}
	if err != nil {
		return nil, DownloadOutput{}, err
	}
	results, err := s.Store.Download(ctx, s.API, withoutDuplicates(files))
	if err != nil {
		return nil, DownloadOutput{}, err
	}
	return nil, DownloadOutput{results}, nil
}

// courseFiles lists every file of a course.
func (s *Service) courseFiles(ctx context.Context, courseID int64) ([]aulaglobal.File, error) {
	sections, err := s.catalog(ctx, courseID, nil, nil)
	if err != nil {
		return nil, err
	}
	files := []aulaglobal.File{}
	for _, sec := range sections {
		for _, module := range sec.Modules {
			files = append(files, module.Files...)
		}
	}
	return files, nil
}

// filesByHandle looks up files from an earlier listing.
func (s *Service) filesByHandle(handles []string) ([]aulaglobal.File, error) {
	files := []aulaglobal.File{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, handle := range handles {
		file, ok := s.files[handle]
		if !ok {
			return nil, errors.New("unknown handle; call list_course_files first")
		}
		files = append(files, file)
	}
	return files, nil
}

// withoutDuplicates drops repeated handles and keeps the first of each.
func withoutDuplicates(files []aulaglobal.File) []aulaglobal.File {
	seen := map[string]bool{}
	unique := files[:0]
	for _, file := range files {
		if !seen[file.Handle] {
			seen[file.Handle] = true
			unique = append(unique, file)
		}
	}
	return unique
}

type ReadInput struct {
	Path   string `json:"path" jsonschema:"Path relative to download root from download_files"`
	Offset int    `json:"offset,omitempty" jsonschema:"UTF-8 byte offset; default 0"`
	Length int    `json:"length,omitempty" jsonschema:"Max text bytes 1..65536; default 16384"`
}

func (s *Service) readFile(ctx context.Context, _ *mcp.CallToolRequest, in ReadInput) (*mcp.CallToolResult, store.ReadResult, error) {
	if in.Length == 0 {
		in.Length = 16384
	}
	result, err := s.Store.Read(ctx, in.Path, in.Offset, in.Length)
	return nil, result, err
}

type SearchInput struct {
	PathPrefix string `json:"path_prefix,omitempty" jsonschema:"Relative course or directory prefix"`
	Query      string `json:"query"`
	Regex      bool   `json:"regex,omitempty"`
	Limit      int    `json:"limit,omitempty" jsonschema:"1..100 results; default 20"`
}
type SearchOutput struct {
	Matches []store.Match `json:"matches"`
}

func (s *Service) searchFiles(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
	if in.Limit == 0 {
		in.Limit = 20
	}
	matches, err := s.Store.Search(ctx, in.Query, in.Regex, in.Limit, in.PathPrefix)
	return nil, SearchOutput{matches}, err
}

func (s *Service) listAssignments(ctx context.Context, _ *mcp.CallToolRequest, in aulaglobal.AssignmentInput) (*mcp.CallToolResult, aulaglobal.AssignmentsOutput, error) {
	out, err := s.API.Assignments(ctx, in, store.PlainHTML)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, assignment := range out.Assignments {
		for _, file := range assignment.Attachments {
			if len(s.files) >= maxHandles {
				return nil, out, errors.New("handle cache full")
			}
			s.files[file.Handle] = file
		}
	}
	return nil, out, err
}

// catalog lists a course's files and remembers their handles for download_files.
func (s *Service) catalog(ctx context.Context, id int64, include, exclude []string) ([]aulaglobal.Section, error) {
	sections, err := s.API.Files(ctx, id, include, exclude)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sec := range sections {
		for _, module := range sec.Modules {
			for _, file := range module.Files {
				if len(s.files) >= maxHandles {
					return nil, errors.New("handle cache full; restart server")
				}
				s.files[file.Handle] = file
			}
		}
	}
	return sections, nil
}

func (s *Service) getAssignmentFeedback(ctx context.Context, _ *mcp.CallToolRequest, in aulaglobal.FeedbackInput) (*mcp.CallToolResult, aulaglobal.FeedbackOutput, error) {
	out, err := s.API.Feedback(ctx, in, store.HTMLText)
	if err != nil {
		return nil, out, err
	}
	// Handles are remembered the same way list_assignments does it, so
	// download_files accepts them.
	s.mu.Lock()
	defer s.mu.Unlock()
	remember := func(files []aulaglobal.FeedbackFile) error {
		for _, file := range files {
			if len(s.files) >= maxHandles {
				return errors.New("handle cache full")
			}
			s.files[file.Handle] = file.File
		}
		return nil
	}
	if err := remember(out.Files); err != nil {
		return nil, out, err
	}
	for _, attempt := range out.PreviousAttempts {
		if err := remember(attempt.Files); err != nil {
			return nil, out, err
		}
	}
	return nil, out, nil
}

type ImageInput struct {
	Path string `json:"path" jsonschema:"Path relative to download root from download_files"`
}

// viewImage returns the image itself. It has no typed output on purpose: a
// structured result would carry no image, and some clients show only that.
func (s *Service) viewImage(ctx context.Context, _ *mcp.CallToolRequest, in ImageInput) (*mcp.CallToolResult, any, error) {
	data, mime, err := s.Store.Image(ctx, in.Path)
	if err != nil {
		return nil, nil, err
	}
	info := fmt.Sprintf("path: %s\nmime: %s\nbytes: %d\nUntrusted course content", filepath.ToSlash(in.Path), mime, len(data))
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: info}, &mcp.ImageContent{Data: data, MIMEType: mime}}}, nil, nil
}
