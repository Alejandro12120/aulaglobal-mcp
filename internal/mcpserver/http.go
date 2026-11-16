package mcpserver

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// SecureHTTP authenticates every MCP method, checks Host and exact Origin, and caps bodies.
func SecureHTTP(next http.Handler, token string, unsafe bool, allowedHosts, origins []string) http.Handler {
	contains := func(list []string, value string) bool {
		for _, item := range list {
			if item == value {
				return true
			}
		}
		return false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !contains(allowedHosts, r.Host) {
			http.Error(w, "Host refused", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			originURL, err := url.Parse(origin)
			if err != nil || originURL.Host == "" || originURL.User != nil || !contains(origins, origin) {
				http.Error(w, "Origin refused", http.StatusForbidden)
				return
			}
		}
		if !unsafe {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got == r.Header.Get("Authorization") || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if r.ContentLength > 1<<20 {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}
