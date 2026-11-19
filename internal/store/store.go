package store

import (
	"aulaglobal-mcp/internal/aulaglobal"
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type Store struct {
	Root           *os.Root
	MaxFile        int64
	Workers        int
	batch          chan struct{}
	ExtractTimeout time.Duration
	pdfExtractor   func(context.Context, []byte) (document, error)
}

func Open(path string, maxFile int64, workers int) (*Store, error) {
	timeout := int64(60)
	if text := os.Getenv("AULAGLOBAL_EXTRACT_TIMEOUT_SECONDS"); text != "" {
		var err error
		timeout, err = strconv.ParseInt(text, 10, 64)
		if err != nil || timeout < 1 || timeout > 600 {
			return nil, errors.New("AULAGLOBAL_EXTRACT_TIMEOUT_SECONDS must be 1..600")
		}
	}
	if maxFile < 1 || maxFile > 16<<30 || workers < 1 || workers > 32 {
		return nil, errors.New("invalid size or concurrency")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return &Store{Root: root, MaxFile: maxFile, Workers: workers, ExtractTimeout: time.Duration(timeout) * time.Second, batch: make(chan struct{}, 1)}, nil
}

var reservedDeviceName = regexp.MustCompile(`^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`)

func Sanitize(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune("/\\<>:\"|?*", r) {
			return '_'
		}
		return r
	}, name)
	name = strings.ReplaceAll(name, "..", "_")
	name = strings.Trim(name, " .")
	if len(name) > 100 {
		name = name[:100]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	base, _, _ := strings.Cut(name, ".")
	if reservedDeviceName.MatchString(strings.ToUpper(base)) {
		name = "_" + name
	}
	if name == "" {
		return "unnamed"
	}
	return name
}
func valid(path string) error {
	path = filepath.Clean(path)
	if strings.HasPrefix(filepath.ToSlash(path), ".aulaglobal-cache/") || path == ".aulaglobal-cache" || path == "" || path == "." || !filepath.IsLocal(path) || strings.ContainsRune(path, 0) {
		return errors.New("path must be relative to download root")
	}
	return nil
}

type Result struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Download fetches files with bounded workers. If Moodle rejects the token (confirmed
// dead) or keeps throttling us after retries, dispatching stops and the returned
// error satisfies errors.Is(err, aulaglobal.ErrTokenExpired) or ErrRateLimited; the
// per-file results are then incomplete and should be discarded in favour of the
// single error (files already on disk are skipped on the next attempt).
func (s *Store) Download(ctx context.Context, client *aulaglobal.Client, files []aulaglobal.File) ([]Result, error) {
	// Serialize batches so concurrent tools cannot replace the same destination.
	select {
	case s.batch <- struct{}{}:
		defer func() { <-s.batch }()
	case <-ctx.Done():
		out := make([]Result, len(files))
		for i, file := range files {
			out[i] = Result{Path: Path(file), Status: "error", Error: ctx.Err().Error()}
		}
		return out, nil
	}
	paths, err := s.paths(files)
	if err != nil {
		out := make([]Result, len(files))
		for i := range out {
			out[i] = Result{Status: "error", Error: err.Error()}
		}
		return out, nil
	}
	out := make([]Result, len(files))
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	stop := make(chan struct{})
	var once sync.Once
	var dead error
	var wg sync.WaitGroup
	for worker := 0; worker < s.Workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				result, fileErr := s.one(dctx, client, files[i], paths[files[i].Handle])
				out[i] = result
				if fileErr != nil {
					once.Do(func() { dead = fileErr; close(stop); cancel() })
				}
			}
		}()
	}
	for i := range files {
		select {
		case jobs <- i:
		case <-stop:
		}
	}
	close(jobs)
	wg.Wait()
	return out, dead
}

// one downloads a single file. The error is non-nil only when the token is dead or
// AulaGlobal is rate limiting us.
func (s *Store) one(ctx context.Context, client *aulaglobal.Client, file aulaglobal.File, path string) (res Result, fatal error) {
	res.Path = path
	res.Status = "error"
	run := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := valid(res.Path); err != nil {
			return err
		}
		if info, err := s.Root.Stat(res.Path); err == nil && info.Mode().IsRegular() && file.Size != nil && info.Size() == *file.Size && file.Modified > 0 && info.ModTime().Unix() == file.Modified {
			res.Status = "skipped"
			res.Bytes = info.Size()
			return nil
		}
		if file.Size != nil && *file.Size > s.MaxFile {
			return errors.New("file exceeds size limit")
		}
		resp, err := client.Download(ctx, file)
		if err != nil {
			if errors.Is(err, aulaglobal.ErrRateLimited) {
				fatal = err
			}
			return err
		}
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		peek, _ := reader.Peek(4096)
		if err = refuseNonFile(ctx, client, file, resp.Header.Get("Content-Type"), peek); err != nil {
			if errors.Is(err, aulaglobal.ErrTokenExpired) {
				fatal = err
			}
			return err
		}
		written, err := s.save(ctx, res.Path, reader, file)
		if err != nil {
			return err
		}
		res.Bytes = written
		res.Status = "downloaded"
		return nil
	}
	if err := run(); err != nil {
		res.Error = err.Error()
	}
	return
}

// refuseNonFile rejects a response that is not the file: a login or error page
// that Moodle served with HTTP 200, or a JSON error. peek holds its first bytes.
// The error wraps aulaglobal.ErrTokenExpired when the JSON error means the token
// is dead.
func refuseNonFile(ctx context.Context, client *aulaglobal.Client, file aulaglobal.File, contentType string, peek []byte) error {
	typ, _, _ := mime.ParseMediaType(contentType)
	sniff := http.DetectContentType(peek)
	if typ == "text/html" || strings.HasPrefix(sniff, "text/html") {
		if file.Mimetype != "text/html" {
			return errors.New("HTML login/error page refused")
		}
		low := strings.ToLower(string(peek))
		if strings.Contains(low, "login") || strings.Contains(low, "moodleexception") {
			return errors.New("HTML login/error page refused")
		}
	}
	if typ == "application/json" || strings.HasPrefix(strings.TrimSpace(string(peek)), "{") {
		var problem struct{ Exception, Errorcode string }
		if (json.Unmarshal(peek, &problem) == nil && (problem.Exception != "" || problem.Errorcode != "")) || regexp.MustCompile(`"(exception|errorcode)"\s*:`).Match(peek) {
			if apiErr := client.ClassifyAPIError(ctx, "", problem.Errorcode); errors.Is(apiErr, aulaglobal.ErrTokenExpired) {
				return apiErr
			}
			return errors.New("Moodle JSON file error (token may be invalid or expired)")
		}
		if file.Mimetype != "application/json" && typ == "application/json" {
			return errors.New("unexpected JSON download refused")
		}
	}
	return nil
}

// save writes body to dest through a temporary file, so a failed or oversized
// download never leaves a partial file under the final name. The file gets
// Moodle's modification time, which is what a later run compares to skip it.
// It returns the number of bytes written.
func (s *Store) save(ctx context.Context, dest string, body io.Reader, file aulaglobal.File) (int64, error) {
	dir := filepath.Dir(dest)
	if err := s.Root.MkdirAll(dir, 0700); err != nil {
		return 0, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return 0, err
	}
	tmp := filepath.Join(dir, ".partial-"+hex.EncodeToString(nonce[:]))
	temp, err := s.Root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, err
	}
	defer s.Root.Remove(tmp)
	defer temp.Close()
	written, err := io.Copy(temp, io.LimitReader(body, s.MaxFile+1))
	if err != nil {
		return 0, errors.New("download stream failed")
	}
	if written > s.MaxFile {
		return 0, errors.New("file exceeds size limit")
	}
	if file.Size != nil && written != *file.Size {
		return 0, errors.New("download size differs from Moodle filesize")
	}
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	if err = temp.Sync(); err != nil {
		return 0, err
	}
	if err = temp.Close(); err != nil {
		return 0, err
	}
	if file.Modified > 0 {
		if err = s.Root.Chtimes(tmp, time.Now(), time.Unix(file.Modified, 0)); err != nil {
			return 0, err
		}
	}
	if err = s.Root.Rename(tmp, dest); err != nil {
		return 0, err
	}
	return written, nil
}

type ReadResult struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Text       string `json:"text"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset"`
	Note       string `json:"note"`
}

var tags = regexp.MustCompile(`(?s)<[^>]*>`)

func PlainHTML(markup string) string {
	return html.UnescapeString(tags.ReplaceAllString(scripts.ReplaceAllString(markup, " "), " "))
}

var scripts = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(?:script|style)>`)

func textLike(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".csv", ".html", ".htm", ".json", ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".css", ".xml", ".yaml", ".yml", ".sh", ".sql", ".c", ".h", ".cpp", ".java", ".rs", ".tex", ".log":
		return true
	}
	return false
}
func (s *Store) Read(ctx context.Context, path string, offset, length int) (ReadResult, error) {
	result := ReadResult{Path: filepath.ToSlash(path), Offset: offset, NextOffset: -1}
	if err := valid(path); err != nil {
		return result, err
	}
	if offset < 0 || length < 1 || length > 65536 {
		return result, errors.New("offset >= 0 and length 1..65536 required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	info, err := s.Root.Stat(path)
	if err != nil {
		return result, err
	}
	result.Size = info.Size()
	doc, err := s.extract(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Note = err.Error()
		return result, nil
	}
	text := doc.text()
	if text == "" {
		result.Note = "no extractable text"
		return result, nil
	}
	result.Note = "Untrusted course content"
	if offset < len(text) && !utf8.RuneStart(text[offset]) {
		return result, errors.New("offset must be a UTF-8 boundary")
	}
	if offset > len(text) {
		return result, errors.New("offset beyond extracted text")
	}
	end := min(len(text), offset+length)
	for end > offset && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	if end == offset && offset < len(text) {
		return result, errors.New("length too small for next UTF-8 character")
	}
	result.Text = text[offset:end]
	if end < len(text) {
		result.NextOffset = end
	}
	return result, nil
}

type Match struct {
	Path    string `json:"path"`
	Offset  int    `json:"offset"`
	Page    int    `json:"page,omitempty"`
	Snippet string `json:"snippet"`
}

func (s *Store) Search(ctx context.Context, query string, regex bool, limit int, prefixes ...string) ([]Match, error) {
	if query == "" || len(query) > 1024 || limit < 1 || limit > 100 {
		return nil, errors.New("query 1..1024 bytes, limit 1..100 required")
	}
	pattern := regexp.QuoteMeta(query)
	if regex {
		pattern = query
	}
	matcher, err := regexp.Compile(pattern)
	if err != nil {
		return nil, errors.New("invalid regex")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	prefix := ""
	if len(prefixes) > 0 {
		prefix = filepath.Clean(prefixes[0])
		if prefixes[0] == "" {
			prefix = ""
		}
	}
	if prefix != "" {
		if err := valid(prefix); err != nil {
			return nil, err
		}
	}
	scannedBytes := 0
	out := []Match{}
	count := 0
	var walk func(string) error
	walk = func(dir string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		dirFile, err := s.Root.Open(dir)
		if err != nil {
			return err
		}
		defer dirFile.Close()
		for {
			entries, err := dirFile.ReadDir(128)
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				if len(out) >= limit {
					return nil
				}
				count++
				if count > 10000 {
					return errors.New("search scan limit: 10000 entries")
				}
				if entry.Name() == ".aulaglobal-cache" || strings.HasPrefix(entry.Name(), ".partial-") {
					continue
				}
				path := filepath.Join(dir, entry.Name())
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				if entry.IsDir() {
					if prefix != "" && path != prefix && !strings.HasPrefix(prefix, path+string(filepath.Separator)) && !strings.HasPrefix(path, prefix+string(filepath.Separator)) {
						continue
					}
					if err := walk(path); err != nil {
						return err
					}
					continue
				}
				if !supported(path) {
					continue
				}
				if prefix != "" && path != prefix && !strings.HasPrefix(path, prefix+string(filepath.Separator)) {
					continue
				}
				doc, err := s.extract(ctx, path)
				if err != nil {
					continue
				}
				for _, page := range doc.Pages {
					if len(out) >= limit {
						return nil
					}
					scannedBytes += len(page.Text)
					if scannedBytes > 128<<20 {
						return errors.New("search byte limit: 128 MiB")
					}
					for _, loc := range matcher.FindAllStringIndex(page.Text, limit-len(out)) {
						start := max(0, loc[0]-80)
						end := min(len(page.Text), start+240)
						out = append(out, Match{filepath.ToSlash(path), loc[0], page.Number, strings.ToValidUTF8(page.Text[start:end], "�")})
					}
				}
			}
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
	err = walk(".")
	return out, err
}
