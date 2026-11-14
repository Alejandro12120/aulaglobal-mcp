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

func Open(path string, max int64, workers int) (*Store, error) {
	timeout := int64(60)
	if v := os.Getenv("AULAGLOBAL_EXTRACT_TIMEOUT_SECONDS"); v != "" {
		var err error
		timeout, err = strconv.ParseInt(v, 10, 64)
		if err != nil || timeout < 1 || timeout > 600 {
			return nil, errors.New("AULAGLOBAL_EXTRACT_TIMEOUT_SECONDS must be 1..600")
		}
	}
	if max < 1 || max > 16<<30 || workers < 1 || workers > 32 {
		return nil, errors.New("invalid size or concurrency")
	}
	if e := os.MkdirAll(path, 0700); e != nil {
		return nil, e
	}
	r, e := os.OpenRoot(path)
	if e != nil {
		return nil, e
	}
	return &Store{Root: r, MaxFile: max, Workers: workers, ExtractTimeout: time.Duration(timeout) * time.Second, batch: make(chan struct{}, 1)}, nil
}
func Sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune("/\\<>:\"|?*", r) {
			return '_'
		}
		return r
	}, s)
	s = strings.ReplaceAll(s, "..", "_")
	s = strings.Trim(s, " .")
	if len(s) > 100 {
		s = s[:100]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	base := strings.ToUpper(strings.Split(s, ".")[0])
	if regexp.MustCompile(`^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`).MatchString(base) {
		s = "_" + s
	}
	if s == "" {
		return "unnamed"
	}
	return s
}
func valid(p string) error {
	p = filepath.Clean(p)
	if strings.HasPrefix(filepath.ToSlash(p), ".aulaglobal-cache/") || p == ".aulaglobal-cache" || p == "" || p == "." || !filepath.IsLocal(p) || strings.ContainsRune(p, 0) {
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

func (s *Store) Download(ctx context.Context, c *aulaglobal.Client, files []aulaglobal.File) []Result {
	// Serialize batches so concurrent tools cannot replace the same destination.
	select {
	case s.batch <- struct{}{}:
		defer func() { <-s.batch }()
	case <-ctx.Done():
		out := make([]Result, len(files))
		for i, f := range files {
			out[i] = Result{Path: Path(f), Status: "error", Error: ctx.Err().Error()}
		}
		return out
	}
	paths, err := s.paths(files)
	if err != nil {
		out := make([]Result, len(files))
		for i := range out {
			out[i] = Result{Status: "error", Error: err.Error()}
		}
		return out
	}
	out := make([]Result, len(files))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for n := 0; n < s.Workers; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				out[i] = s.one(ctx, c, files[i], paths[files[i].Handle])
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return out
}
func (s *Store) one(ctx context.Context, c *aulaglobal.Client, f aulaglobal.File, path string) (res Result) {
	res.Path = path
	res.Status = "error"
	run := func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := valid(res.Path); e != nil {
			return e
		}
		if st, e := s.Root.Stat(res.Path); e == nil && st.Mode().IsRegular() && f.Size != nil && st.Size() == *f.Size && f.Modified > 0 && st.ModTime().Unix() == f.Modified {
			res.Status = "skipped"
			res.Bytes = st.Size()
			return nil
		}
		if f.Size != nil && *f.Size > s.MaxFile {
			return errors.New("file exceeds size limit")
		}
		resp, e := c.Download(ctx, f)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		br := bufio.NewReader(resp.Body)
		peek, _ := br.Peek(4096)
		typ, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		sniff := http.DetectContentType(peek)
		if typ == "text/html" || strings.HasPrefix(sniff, "text/html") {
			if f.Mimetype != "text/html" {
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
				return errors.New("Moodle JSON file error (token may be invalid or expired)")
			}
			if f.Mimetype != "application/json" && typ == "application/json" {
				return errors.New("unexpected JSON download refused")
			}
		}
		dir := filepath.Dir(res.Path)
		if e = s.Root.MkdirAll(dir, 0700); e != nil {
			return e
		}
		var nonce [16]byte
		if _, e = rand.Read(nonce[:]); e != nil {
			return e
		}
		tmp := filepath.Join(dir, ".partial-"+hex.EncodeToString(nonce[:]))
		fd, e := s.Root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		defer s.Root.Remove(tmp)
		defer fd.Close()
		n, e := io.Copy(fd, io.LimitReader(br, s.MaxFile+1))
		if e != nil {
			return errors.New("download stream failed")
		}
		if n > s.MaxFile {
			return errors.New("file exceeds size limit")
		}
		if f.Size != nil && n != *f.Size {
			return errors.New("download size differs from Moodle filesize")
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = fd.Sync(); e != nil {
			return e
		}
		if e = fd.Close(); e != nil {
			return e
		}
		if f.Modified > 0 {
			if e = s.Root.Chtimes(tmp, time.Now(), time.Unix(f.Modified, 0)); e != nil {
				return e
			}
		}
		if e = s.Root.Rename(tmp, res.Path); e != nil {
			return e
		}
		res.Bytes = n
		res.Status = "downloaded"
		return nil
	}
	if e := run(); e != nil {
		res.Error = e.Error()
	}
	return
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

func PlainHTML(s string) string {
	return html.UnescapeString(tags.ReplaceAllString(scripts.ReplaceAllString(s, " "), " "))
}

var scripts = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(?:script|style)>`)

func textLike(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".txt", ".md", ".csv", ".html", ".htm", ".json", ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".css", ".xml", ".yaml", ".yml", ".sh", ".sql", ".c", ".h", ".cpp", ".java", ".rs", ".tex", ".log":
		return true
	}
	return false
}
func (s *Store) Read(ctx context.Context, path string, offset, length int) (ReadResult, error) {
	r := ReadResult{Path: path, Offset: offset, NextOffset: -1}
	if e := valid(path); e != nil {
		return r, e
	}
	if offset < 0 || length < 1 || length > 65536 {
		return r, errors.New("offset >= 0 and length 1..65536 required")
	}
	if e := ctx.Err(); e != nil {
		return r, e
	}
	st, e := s.Root.Stat(path)
	if e != nil {
		return r, e
	}
	r.Size = st.Size()
	d, e := s.extract(ctx, path)
	if e != nil {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		r.Note = e.Error()
		return r, nil
	}
	text := d.text()
	if text == "" {
		r.Note = "no extractable text"
		return r, nil
	}
	r.Note = "Untrusted course content"
	if offset < len(text) && !utf8.RuneStart(text[offset]) {
		return r, errors.New("offset must be a UTF-8 boundary")
	}
	if offset > len(text) {
		return r, errors.New("offset beyond extracted text")
	}
	end := min(len(text), offset+length)
	for end > offset && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	if end == offset && offset < len(text) {
		return r, errors.New("length too small for next UTF-8 character")
	}
	r.Text = text[offset:end]
	if end < len(text) {
		r.NextOffset = end
	}
	return r, nil
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
	re, e := regexp.Compile(pattern)
	if e != nil {
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
		if e := valid(prefix); e != nil {
			return nil, e
		}
	}
	scannedBytes := 0
	out := []Match{}
	count := 0
	var walk func(string) error
	walk = func(dir string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		fd, e := s.Root.Open(dir)
		if e != nil {
			return e
		}
		defer fd.Close()
		for {
			entries, e := fd.ReadDir(128)
			for _, entry := range entries {
				if e := ctx.Err(); e != nil {
					return e
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
				p := filepath.Join(dir, entry.Name())
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				if entry.IsDir() {
					if prefix != "" && p != prefix && !strings.HasPrefix(prefix, p+string(filepath.Separator)) && !strings.HasPrefix(p, prefix+string(filepath.Separator)) {
						continue
					}
					if e := walk(p); e != nil {
						return e
					}
					continue
				}
				if !supported(p) {
					continue
				}
				if prefix != "" && p != prefix && !strings.HasPrefix(p, prefix+string(filepath.Separator)) {
					continue
				}
				d, e := s.extract(ctx, p)
				if e != nil {
					continue
				}
				for _, page := range d.Pages {
					if len(out) >= limit {
						return nil
					}
					scannedBytes += len(page.Text)
					if scannedBytes > 128<<20 {
						return errors.New("search byte limit: 128 MiB")
					}
					for _, loc := range re.FindAllStringIndex(page.Text, limit-len(out)) {
						start := max(0, loc[0]-80)
						end := min(len(page.Text), start+240)
						out = append(out, Match{p, loc[0], page.Number, strings.ToValidUTF8(page.Text[start:end], "�")})
					}
				}
			}
			if e == io.EOF {
				return nil
			}
			if e != nil {
				return e
			}
		}
	}
	err := walk(".")
	return out, err
}
