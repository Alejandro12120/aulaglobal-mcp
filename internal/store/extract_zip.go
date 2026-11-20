package store

import (
	"archive/zip"
	"context"
	"path"
	"sort"
	"strconv"
	"strings"
)

const (
	maxEntryBytes = 32 << 20
	maxSlides     = 2048
)

// zipDocument is an OOXML or ODF package; each XML part is inflated once into memory and scanned in place.
type zipDocument struct {
	ctx   context.Context
	files []*zip.File
	index map[string]*zip.File
	ticks int
}

func (document *zipDocument) buildIndex() {
	document.index = make(map[string]*zip.File, len(document.files))
	for _, entry := range document.files {
		document.index[entry.Name] = entry
	}
}

func (document *zipDocument) find(name string) *zip.File {
	if document.index != nil {
		return document.index[name]
	}
	for _, entry := range document.files {
		if entry.Name == name {
			return entry
		}
	}
	return nil
}

func (document *zipDocument) scanner(entry *zip.File) (*xmlScanner, error) {
	file, err := entry.Open()
	if err != nil {
		return nil, errXML
	}
	data, err := readSized(file, int64(entry.UncompressedSize64), maxEntryBytes+1)
	file.Close()
	if err != nil || len(data) > maxEntryBytes {
		return nil, errXML
	}
	return newScanner(data), nil
}

// check bounds work on untrusted XML: periodic cancellation plus the output cap.
func (document *zipDocument) check(md *markdown) error {
	document.ticks++
	if document.ticks&255 == 0 {
		if err := document.ctx.Err(); err != nil {
			return err
		}
	}
	if md != nil {
		if md.err != nil {
			return md.err
		}
		if md.size() > textCap {
			return fail(ErrLimit, "XML output limit")
		}
	}
	return nil
}

// relationships calls visit for every internal relationship until it returns false.
func (document *zipDocument) relationships(entry *zip.File, visit func(id, kind, target string) bool) error {
	scan, err := document.scanner(entry)
	if err != nil {
		return err
	}
	for count := 0; ; {
		kind, err := scan.next()
		if err != nil {
			return err
		}
		if kind == tokenEOF {
			return nil
		}
		if err = document.check(nil); err != nil {
			return err
		}
		if kind != tokenStart || string(scan.name) != "Relationship" || string(scan.attr("TargetMode")) == "External" {
			continue
		}
		if count++; count > 4*maxSlides {
			return fail(ErrLimit, "relationship limit")
		}
		if !visit(scan.attrString("Id"), scan.attrString("Type"), scan.attrString("Target")) {
			return nil
		}
	}
}

func resolveTarget(directory, target string) string {
	if strings.HasPrefix(target, "/") {
		return path.Clean(target[1:])
	}
	return path.Join(directory, target)
}

type partRef struct {
	entry  *zip.File
	number int
	name   string
}

// numberedParts lists "<prefix>N.xml" entries sorted by N, for packages whose order index is missing.
func (document *zipDocument) numberedParts(prefix string) []partRef {
	var parts []partRef
	for _, entry := range document.files {
		name := entry.Name
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".xml") {
			continue
		}
		if number, err := strconv.Atoi(name[len(prefix) : len(name)-4]); err == nil && number > 0 {
			parts = append(parts, partRef{entry: entry, number: number})
		}
	}
	sort.Slice(parts, func(left, right int) bool { return parts[left].number < parts[right].number })
	return parts
}

// orderedParts follows the ordered list element of a package (slides, sheets) through its relationships.
// A missing or broken index returns no parts, so the caller can fall back to filename order.
func (document *zipDocument) orderedParts(listPart, relsPart, baseDir, tag string) ([]partRef, error) {
	listEntry, relsEntry := document.find(listPart), document.find(relsPart)
	if listEntry == nil || relsEntry == nil {
		return nil, nil
	}
	targets := map[string]string{}
	if err := document.relationships(relsEntry, func(id, kind, target string) bool {
		targets[id] = target
		return true
	}); err != nil {
		return nil, document.fallbackError()
	}
	scan, err := document.scanner(listEntry)
	if err != nil {
		return nil, document.fallbackError()
	}
	var parts []partRef
	position := 0
	for {
		kind, err := scan.next()
		if err != nil {
			return nil, document.fallbackError()
		}
		if kind == tokenEOF {
			return parts, nil
		}
		if err = document.check(nil); err != nil {
			return nil, err
		}
		if kind != tokenStart || string(scan.name) != tag {
			continue
		}
		if position++; position > maxSlides {
			return nil, fail(ErrLimit, "ZIP entry limit")
		}
		if target, found := targets[string(scan.prefixedAttr("id"))]; found {
			if entry := document.find(resolveTarget(baseDir, target)); entry != nil {
				parts = append(parts, partRef{entry: entry, number: position, name: scan.attrString("name")})
			}
		}
	}
}

// fallbackError keeps cancellation visible while a broken index just means "use the filename order".
func (document *zipDocument) fallbackError() error { return document.ctx.Err() }
