package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

const textCap = 8 << 20

type Page struct {
	Number int    `json:"page"`
	Text   string `json:"text"`
}
type document struct {
	Pages []Page `json:"pages"`
}

// A stuck third-party parser occupies a slot, rather than allowing unlimited goroutines.
var extractionSlots = make(chan struct{}, 2)

func supported(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".pdf", ".docx", ".pptx", ".odt", ".odp":
		return true
	}
	return textLike(p)
}
func (s *Store) extract(ctx context.Context, path string) (document, error) {
	var d document
	if e := valid(path); e != nil {
		return d, e
	}
	f, e := s.Root.Open(path)
	if e != nil {
		return d, errors.New("cannot open file inside download root")
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		f.Close()
		return d, errors.New("regular file required")
	}
	if !supported(path) {
		f.Close()
		return d, nil
	}
	if st.Size() > 64<<20 {
		f.Close()
		return d, errors.New("extraction input exceeds 64 MiB")
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("v1:%s:%d:%d", path, st.Size(), st.ModTime().UnixNano())))
	cache := filepath.Join(".aulaglobal-cache", hex.EncodeToString(key[:])+".json")
	if b, err := s.cacheRead(cache); err == nil && json.Unmarshal(b, &d) == nil {
		f.Close()
		return d, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case extractionSlots <- struct{}{}:
	case <-ctx.Done():
		f.Close()
		return d, ctx.Err()
	}
	type result struct {
		d document
		e error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-extractionSlots; f.Close() }()
		r := result{}
		defer func() {
			if recover() != nil {
				r.e = errors.New("malformed document refused")
			}
			done <- r
		}()
		b, err := io.ReadAll(io.LimitReader(f, 64<<20+1))
		if err != nil {
			r.e = err
			return
		}
		r.d, r.e = decodeDocument(ctx, path, b)
	}()
	select {
	case <-ctx.Done():
		return d, ctx.Err()
	case r := <-done:
		if r.e != nil {
			return d, r.e
		}
		b, _ := json.Marshal(r.d)
		if e = s.atomic(cache, b); e != nil {
			return d, e
		}
		return r.d, nil
	}
}
func decodeDocument(ctx context.Context, path string, b []byte) (document, error) {
	d := document{Pages: []Page{}}
	add := func(page int, text string) error {
		total := len(text)
		for _, p := range d.Pages {
			total += len(p.Text)
		}
		if total > textCap {
			return errors.New("extracted text exceeds 8 MiB")
		}
		d.Pages = append(d.Pages, Page{page, strings.ToValidUTF8(text, "�")})
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		r, e := pdf.NewReader(bytes.NewReader(b), int64(len(b)))
		if e != nil {
			return d, errors.New("invalid PDF")
		}
		n := r.NumPage()
		if n > 500 {
			return d, errors.New("PDF exceeds 500 pages")
		}
		for i := 1; i <= n; i++ {
			if e := ctx.Err(); e != nil {
				return d, e
			}
			text, e := r.Page(i).GetPlainText(nil)
			if e != nil {
				return d, errors.New("PDF text extraction failed")
			}
			if e = add(i, text); e != nil {
				return d, e
			}
		}
	case ".docx", ".pptx", ".odt", ".odp":
		z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if e != nil {
			return d, errors.New("invalid ZIP document")
		}
		if len(z.File) > 2048 {
			return d, errors.New("ZIP entry limit")
		}
		var total uint64
		for _, f := range z.File {
			if f.UncompressedSize64 > 32<<20 || f.UncompressedSize64 > max(uint64(1), f.CompressedSize64)*200 {
				return d, errors.New("ZIP expansion refused")
			}
			total += f.UncompressedSize64
			if total > 64<<20 {
				return d, errors.New("ZIP size limit")
			}
		}
		sort.Slice(z.File, func(i, j int) bool { return z.File[i].Name < z.File[j].Name })
		for _, f := range z.File {
			name := f.Name
			selected := name == "word/document.xml" || name == "content.xml" || (strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml"))
			if !selected {
				continue
			}
			fd, e := f.Open()
			if e != nil {
				return d, e
			}
			dec := xml.NewDecoder(io.LimitReader(fd, 32<<20+1))
			var text strings.Builder
			for {
				if e = ctx.Err(); e != nil {
					fd.Close()
					return d, e
				}
				tok, err := dec.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					fd.Close()
					return d, errors.New("invalid document XML")
				}
				switch t := tok.(type) {
				case xml.CharData:
					text.Write(t)
				case xml.EndElement:
					if t.Name.Local == "p" || t.Name.Local == "t" {
						text.WriteByte('\n')
					}
				}
				if text.Len() > textCap {
					fd.Close()
					return d, errors.New("XML output limit")
				}
			}
			fd.Close()
			number := 0
			if strings.HasPrefix(name, "ppt/slides/slide") {
				fmt.Sscanf(name, "ppt/slides/slide%d.xml", &number)
			}
			if e = add(number, text.String()); e != nil {
				return d, e
			}
		}
	default:
		if len(b) > textCap {
			return d, errors.New("text exceeds 8 MiB")
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
			return d, nil
		}
		text := string(b)
		if ext := strings.ToLower(filepath.Ext(path)); ext == ".html" || ext == ".htm" {
			text = PlainHTML(text)
		}
		if e := add(0, text); e != nil {
			return d, e
		}
	}
	return d, nil
}
func (d document) text() string {
	var b strings.Builder
	for _, p := range d.Pages {
		if p.Number > 0 {
			fmt.Fprintf(&b, "\n[Page %d]\n", p.Number)
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

func (s *Store) cacheRead(path string) ([]byte, error) {
	f, e := s.Root.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 2*textCap+1))
	if len(b) > 2*textCap {
		return nil, errors.New("cache too large")
	}
	return b, e
}
