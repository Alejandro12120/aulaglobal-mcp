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
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "moodlemobile" {
		return "", "", errors.New("expected moodlemobile redirect; session may need login/confirmation")
	}
	encoded := strings.TrimPrefix(location, "moodlemobile://token=")
	if encoded == location || strings.ContainsAny(encoded, "&#?") {
		return "", "", errors.New("malformed launch redirect")
	}
	encoded, err = url.PathUnescape(encoded)
	if err != nil {
		return "", "", errors.New("invalid launch encoding")
	}
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
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

// TokenFromCookie exchanges the browser session cookie for a mobile token through
// launch.php (moodle_mobile_app service, passport signed with MD5(site+passport)).
// The flow is based on AGDownloader (MIT, see THIRD_PARTY_NOTICES.md).
func (c *Client) TokenFromCookie(ctx context.Context, cookie string) (string, string, error) {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", "", err
	}
	passport := hex.EncodeToString(nonce[:])
	req, err := http.NewRequestWithContext(ctx, "GET", c.Base+"/admin/tool/mobile/launch.php?"+url.Values{"service": {"moodle_mobile_app"}, "passport": {passport}, "urlscheme": {"moodlemobile"}}.Encode(), nil)
	if err != nil {
		return "", "", errors.New("invalid site")
	}
	req.AddCookie(&http.Cookie{Name: "MoodleSessionag", Value: strings.TrimSpace(cookie)})
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var status int
	var location string
	// Same gate as every other request; a 429/503 pauses everybody. GET: safe to retry.
	if err = c.politely(ctx, true, func() (*throttle, error) {
		actx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		resp, doErr := client.Do(req.WithContext(actx))
		if doErr != nil {
			return nil, errors.New("launch request failed")
		}
		defer resp.Body.Close()
		if throttled := throttleOf(resp); throttled != nil {
			return throttled, nil
		}
		status, location = resp.StatusCode, resp.Header.Get("Location")
		return nil, nil
	}); err != nil {
		return "", "", err
	}
	if status < 300 || status > 399 {
		return "", "", errors.New("launch did not return redirect")
	}
	return ParseLaunch(location, c.Base, passport)
}
func WriteSecret(path, value string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("cannot create secret directory")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return errors.New("secret directory must be private (0700)")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create secret file (must not already exist)")
	}
	_, err = file.WriteString(value + "\n")
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// SaveSecret uses an exclusive temporary file and an atomic replacement with --force.
func SaveSecret(path, value string, force bool) error {
	if !force {
		return WriteSecret(path, value)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return errors.New("secret directory must be private (0700)")
	}
	file, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return errors.New("cannot create secret temporary file")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = file.Chmod(0600); err != nil {
		return err
	}
	if _, err = file.WriteString(value + "\n"); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
