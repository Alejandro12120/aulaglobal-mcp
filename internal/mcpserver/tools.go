package mcpserver

import (
	"context"
	"errors"

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
	a, e := s.API.Auth(ctx)
	out := AuthOutput{TokenValid: e == nil, Sitename: a.Sitename, Release: a.Release, ExpiresAt: a.ExpiresAt, TokenObtainedAt: a.TokenObtainedAt, TokenAgeDays: a.TokenAgeDays, ExpirySource: a.ExpirySource}
	if s.updates != nil {
		out.UpdateAvailable = s.updates.Available()
	}
	return nil, out, e
}

type CoursesInput struct {
	Classification string `json:"classification,omitempty" jsonschema:"all/inprogress/past/future; default inprogress; fallback on older Moodle returns all enrollments"`
	Language       string `json:"language,omitempty" jsonschema:"es or en; default es"`
}
type CoursesOutput struct {
	Courses []aulaglobal.Course `json:"courses"`
}

func (s *Service) listCourses(ctx context.Context, _ *mcp.CallToolRequest, in CoursesInput) (*mcp.CallToolResult, CoursesOutput, error) {
	a, e := s.API.Courses(ctx, in.Classification, in.Language)
	return nil, CoursesOutput{a}, e
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
	ss, e := s.catalog(ctx, in.CourseID, in.Include, in.Exclude)
	return nil, FilesOutput{ss}, e
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
	var e error
	if in.CourseID > 0 {
		files, e = s.courseFiles(ctx, in.CourseID)
	} else {
		files, e = s.filesByHandle(in.Handles)
	}
	if e != nil {
		return nil, DownloadOutput{}, e
	}
	results, e := s.Store.Download(ctx, s.API, withoutDuplicates(files))
	if e != nil {
		return nil, DownloadOutput{}, e
	}
	return nil, DownloadOutput{results}, nil
}

// courseFiles lists every file of a course.
func (s *Service) courseFiles(ctx context.Context, courseID int64) ([]aulaglobal.File, error) {
	ss, e := s.catalog(ctx, courseID, nil, nil)
	if e != nil {
		return nil, e
	}
	files := []aulaglobal.File{}
	for _, sec := range ss {
		for _, m := range sec.Modules {
			files = append(files, m.Files...)
		}
	}
	return files, nil
}

// filesByHandle looks up files from an earlier listing.
func (s *Service) filesByHandle(handles []string) ([]aulaglobal.File, error) {
	files := []aulaglobal.File{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range handles {
		f, ok := s.files[h]
		if !ok {
			return nil, errors.New("unknown handle; call list_course_files first")
		}
		files = append(files, f)
	}
	return files, nil
}

// withoutDuplicates drops repeated handles and keeps the first of each.
func withoutDuplicates(files []aulaglobal.File) []aulaglobal.File {
	seen := map[string]bool{}
	unique := files[:0]
	for _, f := range files {
		if !seen[f.Handle] {
			seen[f.Handle] = true
			unique = append(unique, f)
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
	a, e := s.Store.Read(ctx, in.Path, in.Offset, in.Length)
	return nil, a, e
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
	a, e := s.Store.Search(ctx, in.Query, in.Regex, in.Limit, in.PathPrefix)
	return nil, SearchOutput{a}, e
}

func (s *Service) listAssignments(ctx context.Context, _ *mcp.CallToolRequest, in aulaglobal.AssignmentInput) (*mcp.CallToolResult, aulaglobal.AssignmentsOutput, error) {
	out, e := s.API.Assignments(ctx, in, store.PlainHTML)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range out.Assignments {
		for _, f := range a.Attachments {
			if len(s.files) >= maxHandles {
				return nil, out, errors.New("handle cache full")
			}
			s.files[f.Handle] = f
		}
	}
	return nil, out, e
}

// catalog lists a course's files and remembers their handles for download_files.
func (s *Service) catalog(ctx context.Context, id int64, include, exclude []string) ([]aulaglobal.Section, error) {
	ss, e := s.API.Files(ctx, id, include, exclude)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sec := range ss {
		for _, m := range sec.Modules {
			for _, f := range m.Files {
				if len(s.files) >= maxHandles {
					return nil, errors.New("handle cache full; restart server")
				}
				s.files[f.Handle] = f
			}
		}
	}
	return ss, nil
}
