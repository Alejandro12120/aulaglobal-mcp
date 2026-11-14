package aulaglobal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const Site = "https://aulaglobal.uc3m.es"

type TokenProvider interface {
	Get(context.Context) (string, error)
}
type Invalidator interface{ Invalidate(context.Context) error }
type StaticToken struct{ Value, File string }

func (p StaticToken) Get(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s := p.Value
	if p.File != "" {
		b, e := os.ReadFile(p.File)
		if e != nil {
			return "", errors.New("cannot read token file")
		}
		s = string(b)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("token missing — configure AULAGLOBAL_TOKEN or AULAGLOBAL_TOKEN_FILE")
	}
	return s, nil
}

var secretQuery = regexp.MustCompile("(?i)(token|wstoken|privatetoken|MoodleSessionag)=([^&\\s]+)")

func Redact(s string, secrets ...string) string {
	for _, v := range secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
			s = strings.ReplaceAll(s, url.QueryEscape(v), "[REDACTED]")
		}
	}
	return secretQuery.ReplaceAllString(s, "$1=[REDACTED]")
}

type APIError struct{ Code string }

func (e *APIError) Error() string {
	if e.Code == "invalidtoken" || e.Code == "accessexception" || e.Code == "invalidtimedtoken" {
		return "token invalid or expired — obtain a new one (or check service permissions)"
	}
	return "Moodle web service error: " + e.Code
}

type Client struct {
	Base   string
	HTTP   *http.Client
	Tokens TokenProvider
}

func New(base string, p TokenProvider) *Client {
	u, _ := url.Parse(base)
	return &Client{Base: strings.TrimRight(base, "/"), Tokens: p, HTTP: &http.Client{
		Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second},
		Timeout:   2 * time.Minute,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 || u == nil || r.URL.Host != u.Host || r.URL.Scheme != u.Scheme {
				return errors.New("redirect refused")
			}
			return nil
		},
	}}
}
func (c *Client) WS(ctx context.Context, fn string, args url.Values, out any) error {
	token, e := c.Tokens.Get(ctx)
	if e != nil {
		return e
	}
	if args == nil {
		args = url.Values{}
	}
	args.Set("wstoken", token)
	args.Set("wsfunction", fn)
	args.Set("moodlewsrestformat", "json")
	req, e := http.NewRequestWithContext(ctx, "POST", c.Base+"/webservice/rest/server.php", strings.NewReader(args.Encode()))
	if e != nil {
		return errors.New("invalid site configuration")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return errors.New(Redact(e.Error(), token))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Moodle HTTP status %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if e != nil {
		return errors.New("reading Moodle response failed")
	}
	if len(b) > 32<<20 {
		return errors.New("Moodle response too large")
	}
	var problem struct{ Exception, Errorcode string }
	_ = json.Unmarshal(b, &problem)
	if problem.Exception != "" || problem.Errorcode != "" {
		code := problem.Errorcode
		if !regexp.MustCompile("^[a-zA-Z0-9_]+$").MatchString(code) {
			code = "unknown"
		}
		return &APIError{Code: code}
	}
	if json.Unmarshal(b, out) != nil {
		return errors.New("invalid Moodle JSON response")
	}
	return nil
}

type Auth struct {
	Fullname     string `json:"fullname"`
	UserID       int64  `json:"userid"`
	Sitename     string `json:"sitename"`
	Release      string `json:"release"`
	ExpiresAt    *int64 `json:"expires_at"`
	ExpirySource string `json:"expiry_source"`
}

func (c *Client) Auth(ctx context.Context) (Auth, error) {
	var a Auth
	e := c.WS(ctx, "core_webservice_get_site_info", nil, &a)
	a.ExpirySource = "unknown: get_site_info and mobile launch do not expose this token's validuntil; site duration policy is not an expiry timestamp"
	return a, e
}

type Course struct {
	ID        int64  `json:"id"`
	Fullname  string `json:"fullname"`
	Shortname string `json:"shortname"`
	Display   string `json:"fullnamedisplay,omitempty"`
	Name      string `json:"localized_name"`
}

var year = regexp.MustCompile(`\s+2\d(?:\d\d)?/\d{2,4}(?:-[12][CS]|-S[12])?`)

func CleanName(s, lang string) string {
	loc := year.FindStringIndex(s)
	if loc == nil {
		for _, sep := range []string{"-1C", "-2C", "-1S", "-2S", "Bachelor", "Student", "Convenio-Bilateral s"} {
			if idx := strings.Index(s, sep); idx > 0 {
				end := idx
				if strings.HasPrefix(sep, "-") {
					end += len(sep)
				}
				if lang == "en" {
					part := strings.TrimSpace(s[end:])
					if part != "" {
						return part
					}
				}
				return strings.TrimSpace(s[:idx])
			}
		}
		return strings.TrimSpace(s)
	}
	es := strings.TrimSpace(s[:loc[0]])
	en := strings.TrimSpace(s[loc[1]:])
	if next := year.FindStringIndex(en); next != nil {
		en = strings.TrimSpace(en[:next[0]])
	}
	if lang == "en" && en != "" {
		return en
	}
	return es
}
func (c *Client) Courses(ctx context.Context, class, lang string) ([]Course, error) {
	if class == "" {
		class = "inprogress"
	}
	switch class {
	case "all", "inprogress", "past", "future":
	default:
		return nil, errors.New("classification must be all/inprogress/past/future")
	}
	if lang != "" && lang != "es" && lang != "en" {
		return nil, errors.New("language must be es/en")
	}
	var res struct {
		Courses []Course `json:"courses"`
		Next    int      `json:"nextoffset"`
	}
	all := []Course{}
	offset := 0
	for {
		e := c.WS(ctx, "core_course_get_enrolled_courses_by_timeline_classification", url.Values{"classification": {class}, "limit": {"100"}, "offset": {strconv.Itoa(offset)}}, &res)
		if e != nil {
			var api *APIError
			if !errors.As(e, &api) || (api.Code != "invalidparameter" && api.Code != "servicenotavailable" && api.Code != "wsfunctionnotavailable" && api.Code != "accessexception") {
				return nil, e
			}
			a, err := c.Auth(ctx)
			if err != nil {
				return nil, err
			}
			if err = c.WS(ctx, "core_enrol_get_users_courses", url.Values{"userid": {strconv.FormatInt(a.UserID, 10)}}, &all); err != nil {
				return nil, err
			}
			break
		}
		all = append(all, res.Courses...)
		if res.Next <= offset || len(res.Courses) == 0 {
			break
		}
		offset = res.Next
		if len(all) > 10000 {
			return nil, errors.New("too many courses")
		}
	}
	for i := range all {
		s := all[i].Display
		if s == "" {
			s = all[i].Fullname
		}
		all[i].Name = CleanName(s, lang)
	}
	return all, nil
}

type File struct {
	Type     string `json:"type"`
	Filename string `json:"filename"`
	URL      string `json:"-"`
	RawURL   string `json:"fileurl,omitempty"`
	Size     *int64 `json:"filesize"`
	Mimetype string `json:"mimetype"`
	Modified int64  `json:"timemodified"`
	Handle   string `json:"handle"`
	Section  string `json:"section"`
	Module   string `json:"module"`
	CourseID int64  `json:"course_id"`
	ModuleID int64  `json:"module_id"`
}
type Module struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Files []File `json:"contents"`
}
type Section struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Modules []Module `json:"modules"`
}

func Accept(name string, include, exclude []string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	has := func(list []string) bool {
		for _, s := range list {
			if strings.TrimPrefix(strings.ToLower(s), ".") == ext {
				return true
			}
		}
		return false
	}
	return (len(include) == 0 || has(include)) && !has(exclude)
}
func (c *Client) Files(ctx context.Context, id int64, include, exclude []string) ([]Section, error) {
	if id <= 0 {
		return nil, errors.New("course_id must be positive")
	}
	var ss []Section
	if e := c.WS(ctx, "core_course_get_contents", url.Values{"courseid": {strconv.FormatInt(id, 10)}}, &ss); e != nil {
		return nil, e
	}
	for si := range ss {
		for mi := range ss[si].Modules {
			m := &ss[si].Modules[mi]
			out := []File{}
			for _, f := range m.Files {
				if f.Type != "file" || !Accept(f.Filename, include, exclude) {
					continue
				}
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
				sum := sha256.Sum256([]byte(fmt.Sprintf("%d/%d/%s", id, m.ID, f.URL)))
				f.Handle = hex.EncodeToString(sum[:16])
				f.CourseID = id
				f.ModuleID = m.ID
				f.Section = ss[si].Name
				f.Module = m.Name
				out = append(out, f)
			}
			m.Files = out
		}
	}
	return ss, nil
}
func (c *Client) Download(ctx context.Context, f File) (*http.Response, error) {
	token, e := c.Tokens.Get(ctx)
	if e != nil {
		return nil, e
	}
	u, e := url.Parse(f.URL)
	base, _ := url.Parse(c.Base)
	if e != nil || u.Host != base.Host || u.Scheme != base.Scheme || u.User != nil {
		return nil, errors.New("file URL outside AulaGlobal refused")
	}
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	for attempt := 0; attempt < 3; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		resp, err := c.HTTP.Do(req)
		transient := false
		if err != nil {
			var ne net.Error
			transient = errors.As(err, &ne) && ne.Timeout()
		} else {
			transient = resp.StatusCode >= 500
		}
		if transient && attempt < 2 {
			if resp != nil {
				resp.Body.Close()
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * 100 * time.Millisecond):
			}
			continue
		}
		if err != nil {
			return nil, errors.New(Redact(err.Error(), token))
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("download HTTP status %d", resp.StatusCode)
		}
		return resp, nil
	}
	return nil, errors.New("download failed")
}
