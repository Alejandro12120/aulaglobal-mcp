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

func supported(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf", ".docx", ".pptx", ".odt", ".odp":
		return true
	}
	return textLike(path)
}
func (s *Store) extract(ctx context.Context, path string) (document, error) {
	var doc document
	if err := valid(path); err != nil {
		return doc, err
	}
	file, err := s.Root.Open(path)
	if err != nil {
		return doc, errors.New("cannot open file inside download root")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return doc, errors.New("regular file required")
	}
	if !supported(path) {
		file.Close()
		return doc, nil
	}
	if info.Size() > 64<<20 {
		file.Close()
		return doc, errors.New("extraction input exceeds 64 MiB")
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("v1:%s:%d:%d", path, info.Size(), info.ModTime().UnixNano())))
	cache := filepath.Join(".aulaglobal-cache", hex.EncodeToString(key[:])+".json")
	if content, err := s.cacheRead(cache); err == nil && json.Unmarshal(content, &doc) == nil {
		file.Close()
		return doc, nil
	}
	budget := s.ExtractTimeout
	if budget <= 0 {
		budget = 60 * time.Second
	}
	parseCtx := context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	select {
	case extractionSlots <- struct{}{}:
	case <-ctx.Done():
		file.Close()
		return doc, extractionWaitError(ctx)
	}
	type result struct {
		doc document
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-extractionSlots; file.Close() }()
		result := result{}
		defer func() {
			if recover() != nil {
				result.err = errors.New("malformed document refused")
			}
			if result.err == nil {
				content, err := json.Marshal(result.doc)
				if err == nil {
					err = s.atomic(cache, content)
				}
				result.err = err
			}
			done <- result
		}()
		content, err := io.ReadAll(io.LimitReader(file, 64<<20+1))
		if err != nil {
			result.err = err
			return
		}
		result.doc, result.err = s.decodeDocument(parseCtx, path, content)
	}()
	select {
	case <-ctx.Done():
		return doc, extractionWaitError(ctx)
	case result := <-done:
		if result.err != nil {
			return doc, result.err
		}
		return result.doc, nil
	}
}
func extractionWaitError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return ctx.Err()
	}
	return errors.New("extraction in progress, retry shortly")
}
func decodeDocument(ctx context.Context, path string, content []byte) (document, error) {
	return (&Store{}).decodeDocument(ctx, path, content)
}
func (s *Store) decodeDocument(ctx context.Context, path string, content []byte) (document, error) {
	doc := document{Pages: []Page{}}
	add := func(page int, text string) error {
		total := len(text)
		for _, p := range doc.Pages {
			total += len(p.Text)
		}
		if total > textCap {
			return errors.New("extracted text exceeds 8 MiB")
		}
		doc.Pages = append(doc.Pages, Page{page, strings.ToValidUTF8(text, "�")})
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		if s.pdfExtractor != nil {
			return s.pdfExtractor(ctx, content)
		}
		reader, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			return doc, errors.New("invalid PDF")
		}
		pageCount := reader.NumPage()
		if pageCount > 500 {
			return doc, errors.New("PDF exceeds 500 pages")
		}
		for i := 1; i <= pageCount; i++ {
			if err := ctx.Err(); err != nil {
				return doc, err
			}
			text, err := reader.Page(i).GetPlainText(nil)
			if err != nil {
				return doc, errors.New("PDF text extraction failed")
			}
			if err = add(i, text); err != nil {
				return doc, err
			}
		}
	case ".docx", ".pptx", ".odt", ".odp":
		archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			return doc, errors.New("invalid ZIP document")
		}
		if len(archive.File) > 2048 {
			return doc, errors.New("ZIP entry limit")
		}
		var total uint64
		for _, entry := range archive.File {
			if entry.UncompressedSize64 > 32<<20 || entry.UncompressedSize64 > max(uint64(1), entry.CompressedSize64)*200 {
				return doc, errors.New("ZIP expansion refused")
			}
			total += entry.UncompressedSize64
			if total > 64<<20 {
				return doc, errors.New("ZIP size limit")
			}
		}
		sort.Slice(archive.File, func(i, j int) bool { return archive.File[i].Name < archive.File[j].Name })
		for _, entry := range archive.File {
			name := entry.Name
			selected := name == "word/document.xml" || name == "content.xml" || (strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml"))
			if !selected {
				continue
			}
			entryFile, err := entry.Open()
			if err != nil {
				return doc, err
			}
			decoder := xml.NewDecoder(io.LimitReader(entryFile, 32<<20+1))
			var text strings.Builder
			for {
				if err = ctx.Err(); err != nil {
					entryFile.Close()
					return doc, err
				}
				tok, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					entryFile.Close()
					return doc, errors.New("invalid document XML")
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
					entryFile.Close()
					return doc, errors.New("XML output limit")
				}
			}
			entryFile.Close()
			number := 0
			if strings.HasPrefix(name, "ppt/slides/slide") {
				fmt.Sscanf(name, "ppt/slides/slide%d.xml", &number)
			}
			if err = add(number, text.String()); err != nil {
				return doc, err
			}
		}
	default:
		if len(content) > textCap {
			return doc, errors.New("text exceeds 8 MiB")
		}
		if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
			return doc, nil
		}
		text := string(content)
		if ext := strings.ToLower(filepath.Ext(path)); ext == ".html" || ext == ".htm" {
			text = PlainHTML(text)
		}
		if err := add(0, text); err != nil {
			return doc, err
		}
	}
	return doc, nil
}
func (d document) text() string {
	var builder strings.Builder
	for _, page := range d.Pages {
		if page.Number > 0 {
			fmt.Fprintf(&builder, "\n[Page %d]\n", page.Number)
		}
		builder.WriteString(page.Text)
	}
	return builder.String()
}

func (s *Store) cacheRead(path string) ([]byte, error) {
	file, err := s.Root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 2*textCap+1))
	if len(content) > 2*textCap {
		return nil, errors.New("cache too large")
	}
	return content, err
}
