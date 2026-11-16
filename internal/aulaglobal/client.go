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

func (static StaticToken) Get(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	token := static.Value
	if static.File != "" {
		content, err := os.ReadFile(static.File)
		if err != nil {
			return "", errors.New("cannot read token file")
		}
		token = string(content)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("No hay ningún token de AulaGlobal configurado. Ejecuta `aulaglobal-mcp setup` en una terminal para conectar tu cuenta (o define AULAGLOBAL_TOKEN / AULAGLOBAL_TOKEN_FILE).")
	}
	return token, nil
}

var secretQuery = regexp.MustCompile("(?i)(token|wstoken|privatetoken|MoodleSessionag)=([^&\\s]+)")

func Redact(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
			text = strings.ReplaceAll(text, url.QueryEscape(secret), "[REDACTED]")
		}
	}
	return secretQuery.ReplaceAllString(text, "$1=[REDACTED]")
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
func (c *Client) ClassifyAPIError(ctx context.Context, function, code string) *APIError {
	if !errorCode.MatchString(code) {
		code = "unknown"
	}
	apiErr := &APIError{Code: code}
	switch code {
	case "invalidtoken", "invalidtimedtoken":
		apiErr.Expired = true
	case "accessexception":
		if function == siteInfoFn {
			apiErr.Expired = true
			break
		}
		switch c.probeToken(ctx) {
		case probeDead:
			apiErr.Expired = true
		case probeValid:
			apiErr.TokenOK = true
		}
	}
	return apiErr
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
	err := c.ws(ctx, siteInfoFn, nil, &struct{}{})
	if err == nil {
		c.probeMu.Lock()
		c.validUntil = time.Now().Add(30 * time.Second)
		c.probeMu.Unlock()
		return probeValid
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.Code == "invalidtoken" || apiErr.Code == "invalidtimedtoken" || apiErr.Code == "accessexception") {
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
	politeOnce   sync.Once
	pol          *polite
}

func New(base string, tokens TokenProvider) *Client {
	siteURL, _ := url.Parse(base)
	return &Client{Base: strings.TrimRight(base, "/"), Tokens: tokens, StallTimeout: 60 * time.Second, HTTP: &http.Client{
		Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second},

		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 || siteURL == nil || request.URL.Host != siteURL.Host || request.URL.Scheme != siteURL.Scheme {
				return errors.New("redirect refused")
			}
			return nil
		},
	}}
}
func (c *Client) WS(ctx context.Context, function string, args url.Values, out any) error {
	err := c.ws(ctx, function, args, out)
	var api *APIError
	if errors.As(err, &api) {
		return c.ClassifyAPIError(ctx, function, api.Code)
	}
	return err
}

// ws performs one web service call; Moodle errors come back unclassified.
func (c *Client) ws(ctx context.Context, function string, args url.Values, out any) error {
	token, err := c.Tokens.Get(ctx)
	if err != nil {
		return err
	}
	body, err := c.wsBody(ctx, token, function, args)
	if err != nil {
		return err
	}
	if json.Unmarshal(body, out) != nil {
		return errors.New("invalid Moodle JSON response")
	}
	return nil
}

// wsBody returns the raw JSON of a successful call. Read-only functions are
// deduplicated while in flight (listings are also cached for cacheTTL); only
// successful responses are ever kept, so a dead token is noticed immediately.
func (c *Client) wsBody(ctx context.Context, token, function string, args url.Values) ([]byte, error) {
	if !readOnlyFns[function] {
		return c.wsFetch(ctx, token, function, args, false)
	}
	gate := c.polite()
	key := cacheKey(token, function, args)
	if cacheable[function] {
		if body, ok := gate.cached(key); ok {
			return body, nil
		}
	}
	return gate.shared(ctx, key, func(ctx context.Context) ([]byte, error) {
		body, err := c.wsFetch(ctx, token, function, args, true)
		if err == nil && cacheable[function] && json.Valid(body) {
			gate.store(key, body)
		}
		return body, err
	})
}

// wsFetch performs the HTTP exchange. retry allows repeating after 429/503 (only
// for read-only functions). Each attempt has its own 60 s budget; time spent
// waiting for the rate limiter or a server-requested pause does not count.
func (c *Client) wsFetch(ctx context.Context, token, function string, args url.Values, retry bool) ([]byte, error) {
	form := url.Values{}
	for name, values := range args {
		form[name] = values
	}
	form.Set("wstoken", token)
	form.Set("wsfunction", function)
	form.Set("moodlewsrestformat", "json")
	body := form.Encode()
	var out []byte
	err := c.politely(ctx, retry, func() (*throttle, error) {
		actx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(actx, "POST", c.Base+"/webservice/rest/server.php", strings.NewReader(body))
		if err != nil {
			return nil, errors.New("invalid site configuration")
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr // the caller's own cancellation or deadline, not a network failure
			}
			return nil, errors.New(Redact(err.Error(), token))
		}
		defer resp.Body.Close()
		if throttled := throttleOf(resp); throttled != nil {
			return throttled, nil
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("Moodle HTTP status %d", resp.StatusCode)
		}
		content, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
		if err != nil {
			return nil, errors.New("reading Moodle response failed")
		}
		if len(content) > 32<<20 {
			return nil, errors.New("Moodle response too large")
		}
		// Moodle reports its own errors as JSON with HTTP 200; those are not rate limits.
		var problem struct{ Exception, Errorcode string }
		_ = json.Unmarshal(content, &problem)
		if problem.Exception != "" || problem.Errorcode != "" {
			return nil, &APIError{Code: problem.Errorcode}
		}
		out = content
		return nil, nil
	})
	return out, err
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
	var auth Auth
	err := c.WS(ctx, siteInfoFn, nil, &auth)
	auth.ExpirySource = "unknown: get_site_info and mobile launch do not expose this token's validuntil; site duration policy is not an expiry timestamp"
	if staticToken, ok := c.Tokens.(StaticToken); ok && staticToken.File != "" {
		var meta struct {
			ObtainedAt time.Time `json:"obtained_at"`
		}
		if metaContent, readErr := os.ReadFile(staticToken.File + ".meta.json"); readErr == nil && json.Unmarshal(metaContent, &meta) == nil && !meta.ObtainedAt.IsZero() {
			t := meta.ObtainedAt.Format(time.RFC3339)
			age := max(0, int(time.Since(meta.ObtainedAt).Hours()/24))
			auth.TokenObtainedAt = &t
			auth.TokenAgeDays = &age
		}
	}
	return auth, err
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
func CleanName(name, lang string) string {
	loc := year.FindStringIndex(name)
	if loc == nil {
		for _, sep := range []string{"-1C", "-2C", "-1S", "-2S", "Bachelor", "Student", "Convenio-Bilateral s"} {
			if idx := strings.Index(name, sep); idx > 0 {
				end := idx
				if strings.HasPrefix(sep, "-") {
					end += len(sep)
				}
				if lang == "en" {
					part := strings.TrimSpace(name[end:])
					if part != "" {
						return part
					}
				}
				return strings.TrimSpace(name[:idx])
			}
		}
		return strings.TrimSpace(name)
	}
	spanish := strings.TrimSpace(name[:loc[0]])
	english := strings.TrimSpace(name[loc[1]:])
	if next := year.FindStringIndex(english); next != nil {
		english = strings.TrimSpace(english[:next[0]])
	}
	if lang == "en" && english != "" {
		return english
	}
	return spanish
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
		listErr := c.WS(ctx, "core_course_get_enrolled_courses_by_timeline_classification", url.Values{"classification": {class}, "limit": {"100"}, "offset": {strconv.Itoa(offset)}}, &res)
		if listErr != nil {
			var apiErr *APIError
			if errors.Is(listErr, ErrTokenExpired) || !errors.As(listErr, &apiErr) || (apiErr.Code != "invalidparameter" && apiErr.Code != "servicenotavailable" && apiErr.Code != "wsfunctionnotavailable" && apiErr.Code != "accessexception") {
				return nil, listErr
			}
			auth, err := c.Auth(ctx)
			if err != nil {
				return nil, err
			}
			if err = c.WS(ctx, "core_enrol_get_users_courses", url.Values{"userid": {strconv.FormatInt(auth.UserID, 10)}}, &all); err != nil {
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
		name := all[i].Display
		if name == "" {
			name = all[i].Fullname
		}
		c.rememberCourse(all[i].ID, name)
		all[i].Name = CleanName(name, lang)
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
		for _, allowed := range list {
			if strings.TrimPrefix(strings.ToLower(allowed), ".") == ext {
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
	var sections []Section
	if err := c.WS(ctx, "core_course_get_contents", url.Values{"courseid": {strconv.FormatInt(id, 10)}}, &sections); err != nil {
		return nil, err
	}
	for sectionIndex := range sections {
		for moduleIndex := range sections[sectionIndex].Modules {
			module := &sections[sectionIndex].Modules[moduleIndex]
			out := []File{}
			for _, file := range module.Files {
				if file.Type != "file" || !Accept(file.Filename, include, exclude) {
					continue
				}
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
				file.CourseName = name
				file.Handle = StableHandle(id, module.ID, file.Filepath, file.Filename)
				file.CourseID = id
				file.ModuleID = module.ID
				file.Section = sections[sectionIndex].Name
				file.Module = module.Name
				out = append(out, file)
			}
			module.Files = out
		}
	}
	return sections, nil
}
func (c *Client) Download(ctx context.Context, file File) (*http.Response, error) {
	token, err := c.Tokens.Get(ctx)
	if err != nil {
		return nil, err
	}
	fileURL, err := url.Parse(file.URL)
	base, _ := url.Parse(c.Base)
	if err != nil || fileURL.Host != base.Host || fileURL.Scheme != base.Scheme || fileURL.User != nil {
		return nil, errors.New("file URL outside AulaGlobal refused")
	}
	query := fileURL.Query()
	query.Set("token", token)
	fileURL.RawQuery = query.Encode()
	for attempt := 0; attempt < 3; attempt++ {
		var resp *http.Response
		var requestErr error
		// GET is idempotent: a 429/503 pauses every request and is retried by politely.
		if err = c.politely(ctx, true, func() (*throttle, error) {
			req, _ := http.NewRequestWithContext(ctx, "GET", fileURL.String(), nil)
			resp, requestErr = c.HTTP.Do(req)
			if throttled := throttleOf(resp); throttled != nil {
				resp.Body.Close()
				resp = nil
				return throttled, nil
			}
			return nil, nil
		}); err != nil {
			return nil, err
		}
		transient := false
		if requestErr != nil {
			var netErr net.Error
			transient = errors.As(requestErr, &netErr) && netErr.Timeout()
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
		if requestErr != nil {
			return nil, errors.New(Redact(requestErr.Error(), token))
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
	encoded, _ := json.Marshal([]any{course, module, path, name})
	sum := sha256.Sum256(encoded)
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
