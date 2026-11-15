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
	"sync"
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
		return "", errors.New("No hay ningún token de AulaGlobal configurado. Ejecuta `aulaglobal-mcp setup` en una terminal para conectar tu cuenta (o define AULAGLOBAL_TOKEN / AULAGLOBAL_TOKEN_FILE).")
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

// ErrTokenExpired marks a token that Moodle no longer accepts. Test with errors.Is.
var ErrTokenExpired = errors.New("AulaGlobal token expired or invalid")

const siteInfoFn = "core_webservice_get_site_info"

// APIError is a Moodle web service error. Expired is set only when the token is
// known to be dead; TokenOK only when a follow-up site_info call proved the token
// valid (so an access error is a permission problem, not an expiry).
type APIError struct {
	Code    string
	Expired bool
	TokenOK bool
}

func (e *APIError) Is(target error) bool { return target == ErrTokenExpired && e.Expired }
func (e *APIError) Error() string {
	switch {
	case e.Expired:
		return "La sesión de AulaGlobal ha caducado o el token ya no es válido (Moodle caduca los tokens periódicamente). " +
			"Para renovarla, inicia sesión en " + Site + " en el navegador y ejecuta `aulaglobal-mcp setup` en una terminal; " +
			"detectará el token caducado y te guiará para sustituirlo. No hace falta reiniciar el asistente. " +
			"Los apuntes ya descargados siguen disponibles con read_file y search_files."
	case e.TokenOK:
		return "El token de AulaGlobal es válido, pero Moodle ha denegado el acceso a esta función o servicio (errorcode: " + e.Code + "). " +
			"No es un problema de caducidad: no hace falta renovar el token."
	}
	return "Moodle web service error: " + e.Code
}

var errorCode = regexp.MustCompile("^[a-zA-Z0-9_]+$")

// ClassifyAPIError turns a Moodle errorcode into an APIError, deciding whether the
// token is dead. invalidtoken/invalidtimedtoken are conclusive; accessexception is
// also raised for plain permission problems, so it is confirmed with site_info
// (fn is the failing function, "" outside web service calls such as pluginfile).
func (c *Client) ClassifyAPIError(ctx context.Context, fn, code string) *APIError {
	if !errorCode.MatchString(code) {
		code = "unknown"
	}
	e := &APIError{Code: code}
	switch code {
	case "invalidtoken", "invalidtimedtoken":
		e.Expired = true
	case "accessexception":
		if fn == siteInfoFn {
			e.Expired = true
			break
		}
		switch c.probeToken(ctx) {
		case probeDead:
			e.Expired = true
		case probeValid:
			e.TokenOK = true
		}
	}
	return e
}

type probeResult int

const (
	probeUnknown probeResult = iota
	probeValid
	probeDead
)

// probeToken calls site_info. A valid verdict is cached briefly so a batch of
// permission errors does not double the request count.
func (c *Client) probeToken(ctx context.Context) probeResult {
	c.probeMu.Lock()
	ok := time.Now().Before(c.validUntil)
	c.probeMu.Unlock()
	if ok {
		return probeValid
	}
	e := c.ws(ctx, siteInfoFn, nil, &struct{}{})
	if e == nil {
		c.probeMu.Lock()
		c.validUntil = time.Now().Add(30 * time.Second)
		c.probeMu.Unlock()
		return probeValid
	}
	var api *APIError
	if errors.As(e, &api) && (api.Code == "invalidtoken" || api.Code == "invalidtimedtoken" || api.Code == "accessexception") {
		return probeDead
	}
	return probeUnknown
}

type Client struct {
	Base         string
	HTTP         *http.Client
	Tokens       TokenProvider
	StallTimeout time.Duration
	courseMu     sync.Mutex
	courseNames  map[int64]string
	probeMu      sync.Mutex
	validUntil   time.Time
}

func New(base string, p TokenProvider) *Client {
	u, _ := url.Parse(base)
	return &Client{Base: strings.TrimRight(base, "/"), Tokens: p, StallTimeout: 60 * time.Second, HTTP: &http.Client{
		Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second},

		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 || u == nil || r.URL.Host != u.Host || r.URL.Scheme != u.Scheme {
				return errors.New("redirect refused")
			}
			return nil
		},
	}}
}
func (c *Client) WS(ctx context.Context, fn string, args url.Values, out any) error {
	e := c.ws(ctx, fn, args, out)
	var api *APIError
	if errors.As(e, &api) {
		return c.ClassifyAPIError(ctx, fn, api.Code)
	}
	return e
}

// ws performs one web service call; Moodle errors come back unclassified.
func (c *Client) ws(ctx context.Context, fn string, args url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
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
		return &APIError{Code: problem.Errorcode}
	}
	if json.Unmarshal(b, out) != nil {
		return errors.New("invalid Moodle JSON response")
	}
	return nil
}

type Auth struct {
	Fullname        string  `json:"fullname"`
	UserID          int64   `json:"userid"`
	Sitename        string  `json:"sitename"`
	Release         string  `json:"release"`
	ExpiresAt       *int64  `json:"expires_at"`
	TokenObtainedAt *string `json:"token_obtained_at,omitempty"`
	TokenAgeDays    *int    `json:"token_age_days,omitempty"`
	ExpirySource    string  `json:"expiry_source"`
}

func (c *Client) Auth(ctx context.Context) (Auth, error) {
	var a Auth
	e := c.WS(ctx, siteInfoFn, nil, &a)
	a.ExpirySource = "unknown: get_site_info and mobile launch do not expose this token's validuntil; site duration policy is not an expiry timestamp"
	if p, ok := c.Tokens.(StaticToken); ok && p.File != "" {
		var meta struct {
			ObtainedAt time.Time `json:"obtained_at"`
		}
		if b, err := os.ReadFile(p.File + ".meta.json"); err == nil && json.Unmarshal(b, &meta) == nil && !meta.ObtainedAt.IsZero() {
			t := meta.ObtainedAt.Format(time.RFC3339)
			age := max(0, int(time.Since(meta.ObtainedAt).Hours()/24))
			a.TokenObtainedAt = &t
			a.TokenAgeDays = &age
		}
	}
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

// CleanName picks the Spanish or English part of a bilingual UC3M course name and
// strips the academic year. Adapted from extractCourseNameByLanguage in AGDownloader
// (MIT, Copyright (c) 2024 Eduardo Alarcón; see THIRD_PARTY_NOTICES.md).
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
			if errors.Is(e, ErrTokenExpired) || !errors.As(e, &api) || (api.Code != "invalidparameter" && api.Code != "servicenotavailable" && api.Code != "wsfunctionnotavailable" && api.Code != "accessexception") {
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
		c.rememberCourse(all[i].ID, s)
		all[i].Name = CleanName(s, lang)
	}
	return all, nil
}

type File struct {
	LocalSubdir string `json:"-"`
	CourseName  string `json:"-"`
	Filepath    string `json:"filepath"`
	Type        string `json:"type"`
	Filename    string `json:"filename"`
	URL         string `json:"-"`
	RawURL      string `json:"fileurl,omitempty"`
	Size        *int64 `json:"filesize"`
	Mimetype    string `json:"mimetype"`
	Modified    int64  `json:"timemodified"`
	Handle      string `json:"handle"`
	Section     string `json:"section"`
	Module      string `json:"module"`
	CourseID    int64  `json:"course_id"`
	ModuleID    int64  `json:"module_id"`
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
	name := c.CourseName(ctx, id, "es")
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
				f.CourseName = name
				f.Handle = StableHandle(id, m.ID, f.Filepath, f.Filename)
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
		idle := c.StallTimeout
		if idle <= 0 {
			idle = 60 * time.Second
		}
		resp.Body = newStallBody(resp.Body, idle)
		return resp, nil
	}
	return nil, errors.New("download failed")
}

func StableHandle(course, module int64, path, name string) string {
	b, _ := json.Marshal([]any{course, module, path, name})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// Cache the bilingual source so both display languages and local Spanish names agree.
func (c *Client) rememberCourse(id int64, name string) {
	if name == "" {
		return
	}
	c.courseMu.Lock()
	defer c.courseMu.Unlock()
	if c.courseNames == nil {
		c.courseNames = make(map[int64]string)
	}
	c.courseNames[id] = name
}
func (c *Client) CourseName(ctx context.Context, id int64, lang string) string {
	c.courseMu.Lock()
	name := c.courseNames[id]
	c.courseMu.Unlock()
	if name != "" {
		return CleanName(name, lang)
	}
	var res struct {
		Courses []Course `json:"courses"`
	}
	if c.WS(ctx, "core_course_get_courses_by_field", url.Values{"field": {"id"}, "value": {strconv.FormatInt(id, 10)}}, &res) == nil && len(res.Courses) > 0 {
		c.rememberCourse(id, res.Courses[0].Fullname)
		return CleanName(res.Courses[0].Fullname, lang)
	}
	return fmt.Sprintf("course-%d", id)
}
