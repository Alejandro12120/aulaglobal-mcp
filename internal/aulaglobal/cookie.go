package aulaglobal

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func ParseLaunch(location, site, passport string) (string, string, error) {
	// Custom scheme uses token= as authority, not an ordinary URL query.
	u, e := url.Parse(location)
	if e != nil || u.Scheme != "moodlemobile" {
		return "", "", errors.New("expected moodlemobile redirect; session may need login/confirmation")
	}
	encoded := strings.TrimPrefix(location, "moodlemobile://token=")
	if encoded == location || strings.ContainsAny(encoded, "&#?") {
		return "", "", errors.New("malformed launch redirect")
	}
	encoded, e = url.PathUnescape(encoded)
	if e != nil {
		return "", "", errors.New("invalid launch encoding")
	}
	b, e := base64.StdEncoding.DecodeString(encoded)
	if e != nil {
		return "", "", errors.New("invalid launch base64")
	}
	parts := strings.Split(string(b), ":::")
	if len(parts) < 2 || len(parts) > 3 || parts[1] == "" {
		return "", "", errors.New("invalid launch payload")
	}
	hash := md5.Sum([]byte(strings.TrimRight(site, "/") + passport))
	if subtle.ConstantTimeCompare([]byte(parts[0]), []byte(hex.EncodeToString(hash[:]))) != 1 {
		return "", "", errors.New("launch signature mismatch")
	}
	private := ""
	if len(parts) == 3 {
		private = parts[2]
	}
	return parts[1], private, nil
}
func (c *Client) TokenFromCookie(ctx context.Context, cookie string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var nonce [24]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		return "", "", e
	}
	passport := hex.EncodeToString(nonce[:])
	req, e := http.NewRequestWithContext(ctx, "GET", c.Base+"/admin/tool/mobile/launch.php?"+url.Values{"service": {"moodle_mobile_app"}, "passport": {passport}, "urlscheme": {"moodlemobile"}}.Encode(), nil)
	if e != nil {
		return "", "", errors.New("invalid site")
	}
	req.AddCookie(&http.Cookie{Name: "MoodleSessionag", Value: strings.TrimSpace(cookie)})
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := client.Do(req)
	if e != nil {
		return "", "", errors.New("launch request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return "", "", errors.New("launch did not return redirect")
	}
	return ParseLaunch(resp.Header.Get("Location"), c.Base, passport)
}
func WriteSecret(path, value string) error {
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return errors.New("cannot create secret directory")
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || (runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0) {
		return errors.New("secret directory must be private (0700)")
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return errors.New("cannot create secret file (must not already exist)")
	}
	_, e = f.WriteString(value + "\n")
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}

// SaveSecret uses an exclusive temporary file and an atomic replacement with --force.
func SaveSecret(path, value string, force bool) error {
	if !force {
		return WriteSecret(path, value)
	}
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || (runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0) {
		return errors.New("secret directory must be private (0700)")
	}
	f, e := os.CreateTemp(dir, ".token-*")
	if e != nil {
		return errors.New("cannot create secret temporary file")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if e = f.Chmod(0600); e != nil {
		return e
	}
	if _, e = f.WriteString(value + "\n"); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
