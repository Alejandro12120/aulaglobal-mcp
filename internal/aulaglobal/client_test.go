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
			moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.RawQuery != "" {
					t.Error("credentials must be POST body")
				}
				r.ParseForm()
				if r.Form.Get("wstoken") != "secret" {
					t.Error("missing token")
				}
				fmt.Fprintf(w, `{"exception":"moodle_exception","errorcode":%q,"message":"secret"}`, code)
			}))
			defer moodle.Close()
			client := New(moodle.URL, StaticToken{Value: "secret"})
			_, err := client.Auth(context.Background())
			var api *APIError
			if !errors.As(err, &api) || api.Code != code || strings.Contains(err.Error(), "secret") {
				t.Fatalf("bad error: %v", err)
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
		token, p, err := ParseLaunch("moodlemobile://token="+base64.StdEncoding.EncodeToString([]byte(payload)), Site, "passport")
		if err != nil || token != "token" || (":::"+p != private && private != "") {
			t.Fatalf("%s %s %v", token, p, err)
		}
	}
	if _, _, err := ParseLaunch("https://login", Site, "x"); err == nil {
		t.Fatal("accepted login")
	}
	if _, _, err := ParseLaunch("moodlemobile://token="+base64.StdEncoding.EncodeToString([]byte("bad:::token")), Site, "x"); err == nil {
		t.Fatal("signature unchecked")
	}
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("MoodleSessionag"); err != nil {
			t.Error(err)
		}
		h := md5.Sum([]byte("http://" + r.Host + r.URL.Query().Get("passport")))
		w.Header().Set("Location", "moodlemobile://token="+base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%x:::token:::private", h))))
		w.WriteHeader(303)
	}))
	defer moodle.Close()
	token, p, err := New(moodle.URL, StaticToken{}).TokenFromCookie(context.Background(), "fake")
	if err != nil || token != "token" || p != "private" {
		t.Fatalf("%s %s %v", token, p, err)
	}
}

func TestCatalogAndFallback(t *testing.T) {
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer moodle.Close()
	client := New(moodle.URL, StaticToken{Value: "secret"})
	courses, err := client.Courses(context.Background(), "inprogress", "en")
	if err != nil || len(courses) != 1 || courses[0].Name != "Algebra" {
		t.Fatalf("%+v %v", courses, err)
	}
	ss, err := client.Files(context.Background(), 1, nil, []string{"txt"})
	if err != nil || len(ss[0].Modules[0].Files) != 1 {
		t.Fatalf("%+v %v", ss, err)
	}
	f := ss[0].Modules[0].Files[0]
	if f.RawURL != "" || strings.Contains(f.URL, "secret") || f.Handle == "" {
		t.Fatalf("URL/handle error %+v", f)
	}
	a, err := client.Auth(context.Background())
	if err != nil || a.ExpiresAt != nil || !strings.HasPrefix(a.ExpirySource, "unknown") {
		t.Fatalf("%+v %v", a, err)
	}
}
func TestDownloadHostAndRedirect(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("credentials leaked across host") }))
	defer evil.Close()
	moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, evil.URL, 302) }))
	defer moodle.Close()
	client := New(moodle.URL, StaticToken{Value: "secret"})
	for _, u := range []string{evil.URL, moodle.URL} {
		if _, err := client.Download(context.Background(), File{URL: u}); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("redirect/host error %v", err)
		}
	}
}

func TestWriteSecret(t *testing.T) {
	path := t.TempDir() + "/private/token"
	if err := WriteSecret(path, "fake"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0600) {
		t.Fatal("secret permissions", err)
	}
	st, err = os.Stat(filepath.Dir(path))
	if err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0700) {
		t.Fatal("directory permissions", err)
	}
	if err = WriteSecret(path, "overwrite"); err == nil {
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
			moodle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			defer moodle.Close()
			client := New(moodle.URL, StaticToken{Value: "secret"})
			err := client.WS(context.Background(), tc.fn, nil, &struct{}{})
			var api *APIError
			if !errors.As(err, &api) || api.Code != tc.code {
				t.Fatalf("bad error: %v", err)
			}
			if errors.Is(err, ErrTokenExpired) != tc.expired || api.TokenOK != tc.tokenOK {
				t.Fatalf("expired=%v tokenOK=%v: %v", errors.Is(err, ErrTokenExpired), api.TokenOK, err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) || strings.Contains(err.Error(), tc.wantNotSubstr) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("message: %v", err)
			}
			if probes.Load() != tc.siteInfoCalls && !(tc.fn == "core_webservice_get_site_info" && probes.Load() == 1) {
				t.Fatalf("site_info calls = %d, want %d", probes.Load(), tc.siteInfoCalls)
			}
		})
	}
}

func TestMissingTokenMessage(t *testing.T) {
	_, err := StaticToken{}.Get(context.Background())
	if err == nil || !strings.Contains(err.Error(), "aulaglobal-mcp setup") {
		t.Fatal(err)
	}
}
