package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
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
	case ".pdf", ".docx", ".pptx", ".odt", ".odp", ".xlsx", ".xlsm", ".ods", ".doc", ".ppt", ".xls":
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
		return doc, fail(ErrLimit, "extraction input exceeds 64 MiB")
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("v2:%s:%d:%d", path, info.Size(), info.ModTime().UnixNano())))
	cache := filepath.Join(".aulaglobal-cache", hex.EncodeToString(key[:])+".json")
	if entry, ok := s.cachedEntry(cache); ok {
		file.Close()
		if entry.Code != "" {
			return doc, failureFromCode(entry.Code, entry.Error)
		}
		return document{Pages: entry.Pages}, nil
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
			} else if code := failureCode(result.err); code != "" {
				if content, err := json.Marshal(cacheEntry{Code: code, Error: result.err.Error()}); err == nil {
					s.atomic(cache, content)
				}
			}
			done <- result
		}()
		content, err := readSized(file, info.Size(), 64<<20+1)
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

// cacheEntry is the cache file: pages on success, or a deterministic failure that must not be parsed again.
type cacheEntry struct {
	Pages []Page `json:"pages,omitempty"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Store) cachedEntry(path string) (cacheEntry, bool) {
	var entry cacheEntry
	content, err := s.cacheRead(path)
	if err != nil || json.Unmarshal(content, &entry) != nil {
		return entry, false
	}
	return entry, entry.Code == "" || failureFromCode(entry.Code, "") != nil
}

type pageSink struct {
	pages []Page
	total int
}

func (sink *pageSink) add(number int, text string) error {
	if text == "" {
		return nil
	}
	if sink.total += len(text); sink.total > textCap {
		return fail(ErrLimit, "extracted text exceeds 8 MiB")
	}
	sink.pages = append(sink.pages, Page{number, strings.ToValidUTF8(text, "�")})
	return nil
}

func decodeDocument(ctx context.Context, path string, content []byte) (document, error) {
	return (&Store{}).decodeDocument(ctx, path, content)
}

// decodeDocument identifies the format from the content and reads it as Markdown (or raw text).
func (s *Store) decodeDocument(ctx context.Context, path string, content []byte) (document, error) {
	ext := strings.ToLower(filepath.Ext(path))
	sink := &pageSink{pages: []Page{}}
	switch sniff(ext, content) {
	case formatPDF:
		return s.decodePDF(ctx, sink, content)
	case formatOLE:
		return document{Pages: sink.pages}, oleError(content)
	case formatZIP:
		err := decodeArchive(ctx, ext, sink, content)
		return document{Pages: sink.pages}, err
	}
	switch {
	case legacyExtension(ext):
		return document{Pages: sink.pages}, fail(ErrUnsupported, "legacy binary Office format (.doc/.ppt/.xls) is not supported; save it as docx, pptx, xlsx or PDF")
	case officeExtension(ext):
		return document{Pages: sink.pages}, fail(ErrMalformed, "invalid ZIP document")
	}
	if len(content) > textCap {
		return document{Pages: sink.pages}, fail(ErrLimit, "text exceeds 8 MiB")
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return document{Pages: sink.pages}, nil
	}
	text := string(content)
	if ext == ".html" || ext == ".htm" {
		text = PlainHTML(text)
	}
	err := sink.add(0, text)
	return document{Pages: sink.pages}, err
}

func (s *Store) decodePDF(ctx context.Context, sink *pageSink, content []byte) (document, error) {
	if s.pdfExtractor != nil {
		return s.pdfExtractor(ctx, content)
	}
	doc := document{Pages: sink.pages}
	reader, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		if errors.Is(err, pdf.ErrInvalidPassword) || bytes.Contains(content[max(0, len(content)-2048):], []byte("/Encrypt")) {
			return doc, fail(ErrEncrypted, "PDF is encrypted; password-protected PDFs are not supported")
		}
		return doc, fail(ErrMalformed, "invalid PDF")
	}
	pageCount := reader.NumPage()
	if pageCount > 500 {
		return doc, fail(ErrLimit, "PDF exceeds 500 pages")
	}
	hasText := false
	for page := 1; page <= pageCount; page++ {
		if err := ctx.Err(); err != nil {
			return doc, err
		}
		text, err := reader.Page(page).GetPlainText(nil)
		if err != nil {
			return doc, fail(ErrMalformed, "PDF text extraction failed")
		}
		hasText = hasText || len(strings.TrimSpace(text)) > 0
		if err = sink.add(page, text); err != nil {
			return doc, err
		}
	}
	if pageCount > 0 && !hasText {
		return doc, fail(ErrNeedsOCR, "PDF has no text layer (scanned); OCR not supported")
	}
	return document{Pages: sink.pages}, nil
}

func decodeArchive(ctx context.Context, ext string, sink *pageSink, content []byte) error {
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return fail(ErrMalformed, "invalid ZIP document")
	}
	if len(archive.File) > 2048 {
		return fail(ErrLimit, "ZIP entry limit")
	}
	var total uint64
	for _, entry := range archive.File {
		if entry.UncompressedSize64 > 32<<20 || entry.UncompressedSize64 > max(uint64(1), entry.CompressedSize64)*200 {
			return fail(ErrLimit, "ZIP expansion refused")
		}
		total += entry.UncompressedSize64
		if total > 64<<20 {
			return fail(ErrLimit, "ZIP size limit")
		}
	}
	document := &zipDocument{ctx: ctx, files: archive.File}
	writer := acquireMarkdown()
	defer writer.release()
	switch kind := classify(ext, archive.File); kind {
	case formatDOCX:
		return document.decodeDOCX(sink, writer)
	case formatPPTX:
		return document.decodePPTX(sink, writer)
	case formatXLSX:
		return document.decodeXLSX(sink, writer)
	case formatODT, formatODP, formatODS:
		return document.decodeODF(kind, sink, writer)
	}
	return fail(ErrUnsupported, "ZIP archive is not a supported document")
}

func (d document) text() string {
	if len(d.Pages) == 1 && d.Pages[0].Number <= 0 {
		return d.Pages[0].Text
	}
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
	var size int64
	if info, err := file.Stat(); err == nil {
		size = info.Size()
	}
	content, err := readSized(file, size, 2*textCap+1)
	if len(content) > 2*textCap {
		return nil, errors.New("cache too large")
	}
	return content, err
}

// Use the file size to avoid growing and copying the buffer on large reads.
// Keep reading to EOF or the limit because the file can change after Stat.
func readSized(reader io.Reader, size, limit int64) ([]byte, error) {
	bounded := io.LimitReader(reader, limit)
	if size <= bytes.MinRead {
		return io.ReadAll(bounded)
	}
	var content bytes.Buffer
	content.Grow(int(min(size, limit)) + bytes.MinRead)
	_, err := content.ReadFrom(bounded)
	return content.Bytes(), err
}
