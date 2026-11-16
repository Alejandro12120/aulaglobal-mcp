package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

const (
	maxJSONBytes     = 1 << 20
	maxChecksumBytes = 1 << 20
	maxSigBytes      = 16 << 10
	maxArchiveBytes  = 100 << 20

	apiTimeout      = 15 * time.Second
	smallTimeout    = 30 * time.Second
	archiveTimeout  = 10 * time.Minute
	checkTimeout    = 5 * time.Second
	checksumsAsset  = "checksums.txt"
	signatureSuffix = ".sig"
)

// Updater talks to GitHub Releases and replaces the running executable.
// Every field is overridable so tests never reach the network or the real binary.
type Updater struct {
	Current      string // installed version, as printed by `version`
	Repo         string
	APIBase      string // https://api.github.com
	DownloadBase string // https://github.com
	Key          string // trusted public key, authorized_keys format
	GOOS, GOARCH string
	ExePath      string // executable to replace; resolved when empty
	HTTP         *http.Client
}

// New returns an Updater wired to the real GitHub repository and platform.
func New(current string) *Updater {
	return &Updater{
		Current:      current,
		Repo:         Repo,
		APIBase:      "https://api.github.com",
		DownloadBase: "https://github.com",
		Key:          TrustedKey(),
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		HTTP:         &http.Client{CheckRedirect: checkRedirect},
	}
}

// Release is the latest published release.
type Release struct {
	Tag    string          // validated "vX.Y.Z"
	Assets map[string]bool // asset names published with the release
}

// Version returns the parsed tag.
func (r Release) Version() Version { parsed, _ := ParseVersion(r.Tag); return parsed }

func (u *Updater) userAgent() string {
	if parsed, ok := ParseVersion(u.Current); ok {
		return "aulaglobal-mcp/" + parsed.String()
	}
	return "aulaglobal-mcp/dev"
}

// get downloads url with a timeout, a status check and a hard size cap.
func (u *Updater) get(ctx context.Context, url, accept string, limit int64, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", u.userAgent())
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no se pudo conectar con GitHub: %w", unwrapURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusNotFound:
			return nil, errors.New("GitHub respondió 404: recurso no encontrado")
		case http.StatusForbidden, http.StatusTooManyRequests:
			return nil, fmt.Errorf("GitHub respondió %d (posible límite de peticiones); inténtalo más tarde", resp.StatusCode)
		}
		return nil, fmt.Errorf("GitHub respondió con el estado HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, errors.New("la respuesta supera el tamaño máximo permitido")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("descarga interrumpida: %w", unwrapURL(err))
	}
	if int64(len(body)) > limit {
		return nil, errors.New("la respuesta supera el tamaño máximo permitido")
	}
	return body, nil
}

func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Latest queries the latest non-draft, non-prerelease release.
func (u *Updater) Latest(ctx context.Context) (Release, error) {
	body, err := u.get(ctx, u.APIBase+"/repos/"+u.Repo+"/releases/latest", "application/vnd.github+json", maxJSONBytes, apiTimeout)
	if err != nil {
		return Release{}, err
	}
	var doc struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return Release{}, errors.New("respuesta de GitHub no válida")
	}
	parsed, ok := ParseVersion(doc.TagName)
	if !ok || !strings.HasPrefix(doc.TagName, "v") {
		return Release{}, errors.New("la última versión publicada tiene una etiqueta no válida")
	}
	if doc.Draft || doc.Prerelease {
		return Release{}, errors.New("la última versión publicada no es una versión estable")
	}
	release := Release{Tag: parsed.String(), Assets: map[string]bool{}}
	for _, asset := range doc.Assets {
		release.Assets[asset.Name] = true
	}
	return release, nil
}

func (u *Updater) assetURL(tag, name string) string {
	return u.DownloadBase + "/" + u.Repo + "/releases/download/" + tag + "/" + name
}

// Install downloads rel, verifies signature and checksum, and replaces the
// executable. progress (optional) receives one finished line per stage.
func (u *Updater) Install(ctx context.Context, release Release, progress func(string)) error {
	say := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	if _, ok := ParseVersion(release.Tag); !ok {
		return errors.New("etiqueta de versión no válida")
	}
	archive, err := AssetName(strings.TrimPrefix(release.Tag, "v"), u.GOOS, u.GOARCH)
	if err != nil {
		return err
	}
	sigName := checksumsAsset + signatureSuffix
	for _, need := range []string{archive, checksumsAsset} {
		if !release.Assets[need] {
			return fmt.Errorf("la versión %s no incluye %s", release.Tag, need)
		}
	}
	if !release.Assets[sigName] {
		return fmt.Errorf("la versión %s no incluye la firma (%s); no se instala por seguridad", release.Tag, sigName)
	}

	exe := u.ExePath
	if exe == "" {
		if exe, err = ResolveExecutable(); err != nil {
			return fmt.Errorf("no se pudo localizar el programa en ejecución: %w", err)
		}
	}

	sums, err := u.get(ctx, u.assetURL(release.Tag, checksumsAsset), "application/octet-stream", maxChecksumBytes, smallTimeout)
	if err != nil {
		return err
	}
	sig, err := u.get(ctx, u.assetURL(release.Tag, sigName), "application/octet-stream", maxSigBytes, smallTimeout)
	if err != nil {
		return err
	}
	// The signature is checked before anything listed in checksums.txt is trusted.
	if err := VerifySSHSig(u.Key, Namespace, sig, sums); err != nil {
		return fmt.Errorf("la firma de la versión no es válida, no se instala: %w", err)
	}
	say("✓ Firma de la versión verificada")
	want, err := checksumFor(sums, archive)
	if err != nil {
		return err
	}
	say("· Descargando " + archive)
	data, err := u.get(ctx, u.assetURL(release.Tag, archive), "application/octet-stream", maxArchiveBytes, archiveTimeout)
	if err != nil {
		return err
	}
	if err := verifyChecksum(data, want); err != nil {
		return err
	}
	say("✓ Checksum SHA-256 correcto")
	bin := BinaryName
	if u.GOOS == "windows" {
		bin += ".exe"
	}
	exeData, err := extractBinary(data, u.GOOS == "windows", bin)
	if err != nil {
		return err
	}
	return installBinary(exe, exeData, u.GOOS == "windows")
}

// checkRedirect follows at most 5 redirects and never downgrades https to http.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("demasiadas redirecciones")
	}
	if req.URL.Scheme != "https" && via[0].URL.Scheme == "https" {
		return errors.New("redirección a una conexión no cifrada rechazada")
	}
	return nil
}
