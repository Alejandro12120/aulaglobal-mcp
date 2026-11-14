package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurity(t *testing.T) {
	h := SecureHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), "secret", false, []string{"localhost:8080"}, []string{"http://localhost:8080"})
	for _, tc := range []struct {
		host, origin, auth string
		want               int
	}{
		{"localhost:8080", "", "Bearer secret", 204}, {"localhost:8080", "", "", 401}, {"evil.test", "", "Bearer secret", 403}, {"localhost:8080", "https://evil.test", "Bearer secret", 403}, {"localhost:8080", "http://localhost:8080", "Bearer secret", 204},
	} {
		r := httptest.NewRequest("POST", "http://"+tc.host+"/mcp", strings.NewReader("{}"))
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v: %d", tc, w.Code)
		}
	}
}
