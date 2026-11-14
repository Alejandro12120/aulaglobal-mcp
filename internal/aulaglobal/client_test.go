package aulaglobal

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWSErrors(t *testing.T) {
	for _, code := range []string{"invalidtoken", "accessexception", "someerror"} {
		t.Run(code, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.RawQuery != "" {
					t.Error("credentials must be POST body")
				}
				r.ParseForm()
				if r.Form.Get("wstoken") != "secret" {
					t.Error("missing token")
				}
				fmt.Fprintf(w, `{"exception":"moodle_exception","errorcode":%q,"message":"secret"}`, code)
			}))
			defer ts.Close()
			c := New(ts.URL, StaticToken{Value: "secret"})
			_, e := c.Auth(context.Background())
			var api *APIError
			if !errors.As(e, &api) || api.Code != code || strings.Contains(e.Error(), "secret") {
				t.Fatalf("bad error: %v", e)
			}
		})
	}
}
func TestCleanName(t *testing.T) {
	for _, tc := range []struct{ s, lang, want string }{
		{"Tecnología de Computadores 21/22-2C Computer Technology 21/22-S2", "es", "Tecnología de Computadores"},
		{"Tecnología de Computadores 21/22-2CComputer Technology 21/22-S2", "en", "Computer Technology"},
		{"Álgebra", "es", "Álgebra"}, {"Álgebra 25/26-1C", "en", "Álgebra"},
	} {
		if got := CleanName(tc.s, tc.lang); got != tc.want {
			t.Errorf("%q != %q", got, tc.want)
		}
	}
	if Accept("x.pdf", nil, []string{"pdf"}) || Accept("x.pdf", []string{"pdf"}, []string{"pdf"}) || !Accept("x.PDF", []string{".pdf"}, nil) {
		t.Fatal("extension filtering")
	}
}
func TestRedact(t *testing.T) {
	secret := "sensitive123"
	msg := Redact("Get https://site/file?token="+secret+": failed "+secret, secret)
	var b bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&b, nil))
	logger.Error("failure", "error", msg)
	if strings.Contains(b.String(), secret) {
		t.Fatal("token leaked")
	}
	if strings.Contains(Redact("x?wstoken=unknown&foo=bar"), "unknown") {
		t.Fatal("query token leaked")
	}
}
func TestLaunch(t *testing.T) {
	for _, private := range []string{"", ":::private"} {
		h := md5.Sum([]byte(Site + "passport"))
		payload := fmt.Sprintf("%x:::token%s", h, private)
		token, p, e := ParseLaunch("moodlemobile://token="+base64.StdEncoding.EncodeToString([]byte(payload)), Site, "passport")
		if e != nil || token != "token" || (":::"+p != private && private != "") {
			t.Fatalf("%s %s %v", token, p, e)
		}
	}
	if _, _, e := ParseLaunch("https://login", Site, "x"); e == nil {
		t.Fatal("accepted login")
	}
	if _, _, e := ParseLaunch("moodlemobile://token="+base64.StdEncoding.EncodeToString([]byte("bad:::token")), Site, "x"); e == nil {
		t.Fatal("signature unchecked")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, e := r.Cookie("MoodleSessionag"); e != nil {
			t.Error(e)
		}
		h := md5.Sum([]byte("http://" + r.Host + r.URL.Query().Get("passport")))
		w.Header().Set("Location", "moodlemobile://token="+base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%x:::token:::private", h))))
		w.WriteHeader(303)
	}))
	defer ts.Close()
	token, p, e := New(ts.URL, StaticToken{}).TokenFromCookie(context.Background(), "fake")
	if e != nil || token != "token" || p != "private" {
		t.Fatalf("%s %s %v", token, p, e)
	}
}

func TestCatalogAndFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.Form.Get("wsfunction") {
		case "core_course_get_enrolled_courses_by_timeline_classification":
			fmt.Fprint(w, `{"exception":"x","errorcode":"servicenotavailable"}`)
		case "core_webservice_get_site_info":
			fmt.Fprint(w, `{"fullname":"Test User","userid":42,"sitename":"Test","release":"4.5"}`)
		case "core_enrol_get_users_courses":
			if r.Form.Get("userid") != "42" {
				t.Error("wrong user")
			}
			fmt.Fprint(w, `[{"id":1,"fullname":"Álgebra 25/26-1C Algebra 25/26-S1","shortname":"ALG"}]`)
		case "core_course_get_contents":
			fmt.Fprintf(w, `[{"id":1,"name":"../section","modules":[{"id":2,"name":"module","contents":[{"type":"file","filename":"doc.pdf","fileurl":"http://%s/file?token=secret","filesize":5},{"type":"file","filename":"doc.txt","fileurl":"http://%s/other","filesize":5},{"type":"url","filename":"link"}]}]}]`, r.Host, r.Host)
		}
	}))
	defer ts.Close()
	c := New(ts.URL, StaticToken{Value: "secret"})
	courses, e := c.Courses(context.Background(), "inprogress", "en")
	if e != nil || len(courses) != 1 || courses[0].Name != "Algebra" {
		t.Fatalf("%+v %v", courses, e)
	}
	ss, e := c.Files(context.Background(), 1, nil, []string{"txt"})
	if e != nil || len(ss[0].Modules[0].Files) != 1 {
		t.Fatalf("%+v %v", ss, e)
	}
	f := ss[0].Modules[0].Files[0]
	if f.RawURL != "" || strings.Contains(f.URL, "secret") || f.Handle == "" {
		t.Fatalf("URL/handle error %+v", f)
	}
	a, e := c.Auth(context.Background())
	if e != nil || a.ExpiresAt != nil || !strings.HasPrefix(a.ExpirySource, "unknown") {
		t.Fatalf("%+v %v", a, e)
	}
}
func TestDownloadHostAndRedirect(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("credentials leaked across host") }))
	defer evil.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, evil.URL, 302) }))
	defer ts.Close()
	c := New(ts.URL, StaticToken{Value: "secret"})
	for _, u := range []string{evil.URL, ts.URL} {
		if _, e := c.Download(context.Background(), File{URL: u}); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatalf("redirect/host error %v", e)
		}
	}
}

func TestWriteSecret(t *testing.T) {
	path := t.TempDir() + "/private/token"
	if e := WriteSecret(path, "fake"); e != nil {
		t.Fatal(e)
	}
	st, e := os.Stat(path)
	if e != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0600) {
		t.Fatal("secret permissions", e)
	}
	st, e = os.Stat(filepath.Dir(path))
	if e != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0700) {
		t.Fatal("directory permissions", e)
	}
	if e = WriteSecret(path, "overwrite"); e == nil {
		t.Fatal("overwrote existing secret")
	}
}

func TestTokenClassification(t *testing.T) {
	for _, tc := range []struct {
		name, code, siteInfo      string
		fn                        string
		expired, tokenOK          bool
		wantSubstr, wantNotSubstr string
		siteInfoCalls             int32
	}{
		{"invalidtoken is dead without probing", "invalidtoken", "", "core_course_get_contents", true, false, "ha caducado", "denegado", 0},
		{"invalidtimedtoken is dead", "invalidtimedtoken", "", "core_course_get_contents", true, false, "ha caducado", "denegado", 0},
		{"accessexception, site_info dead", "accessexception", `{"errorcode":"invalidtoken","exception":"x"}`, "core_course_get_contents", true, false, "aulaglobal-mcp setup", "denegado", 1},
		{"accessexception, site_info itself", "accessexception", "", "core_webservice_get_site_info", true, false, "aulaglobal-mcp setup", "denegado", 0},
		{"accessexception, token valid", "accessexception", `{"fullname":"Ana","userid":1}`, "core_course_get_contents", false, true, "denegado el acceso", "aulaglobal-mcp setup", 1},
		{"accessexception, site_info unreachable", "accessexception", "<html>", "core_course_get_contents", false, false, "Moodle web service error: accessexception", "setup", 1},
		{"other code", "invalidparameter", "", "core_course_get_contents", false, false, "invalidparameter", "setup", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var probes atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				if r.Form.Get("wsfunction") == "core_webservice_get_site_info" {
					probes.Add(1)
					if tc.fn == "core_webservice_get_site_info" {
						fmt.Fprintf(w, `{"exception":"x","errorcode":%q}`, tc.code)
					} else {
						fmt.Fprint(w, tc.siteInfo)
					}
					return
				}
				fmt.Fprintf(w, `{"exception":"x","errorcode":%q}`, tc.code)
			}))
			defer ts.Close()
			c := New(ts.URL, StaticToken{Value: "secret"})
			e := c.WS(context.Background(), tc.fn, nil, &struct{}{})
			var api *APIError
			if !errors.As(e, &api) || api.Code != tc.code {
				t.Fatalf("bad error: %v", e)
			}
			if errors.Is(e, ErrTokenExpired) != tc.expired || api.TokenOK != tc.tokenOK {
				t.Fatalf("expired=%v tokenOK=%v: %v", errors.Is(e, ErrTokenExpired), api.TokenOK, e)
			}
			if !strings.Contains(e.Error(), tc.wantSubstr) || strings.Contains(e.Error(), tc.wantNotSubstr) || strings.Contains(e.Error(), "secret") {
				t.Fatalf("message: %v", e)
			}
			if probes.Load() != tc.siteInfoCalls && !(tc.fn == "core_webservice_get_site_info" && probes.Load() == 1) {
				t.Fatalf("site_info calls = %d, want %d", probes.Load(), tc.siteInfoCalls)
			}
		})
	}
}

func TestMissingTokenMessage(t *testing.T) {
	_, e := StaticToken{}.Get(context.Background())
	if e == nil || !strings.Contains(e.Error(), "aulaglobal-mcp setup") {
		t.Fatal(e)
	}
}
