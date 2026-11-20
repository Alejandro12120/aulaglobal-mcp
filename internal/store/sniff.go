package store

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
)

type format uint8

const (
	formatText format = iota
	formatPDF
	formatZIP
	formatOLE
	formatDOCX
	formatPPTX
	formatXLSX
	formatODT
	formatODP
	formatODS
)

var oleMagic = []byte("\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1")

func officeExtension(ext string) bool {
	switch ext {
	case ".docx", ".pptx", ".xlsx", ".xlsm", ".odt", ".odp", ".ods":
		return true
	}
	return false
}

func legacyExtension(ext string) bool { return ext == ".doc" || ext == ".ppt" || ext == ".xls" }

// sniff trusts the leading bytes before the extension, so a mislabeled file is read as what it is.
func sniff(ext string, content []byte) format {
	switch {
	case bytes.HasPrefix(content, []byte("%PDF-")):
		return formatPDF
	case bytes.HasPrefix(content, []byte("PK\x03\x04")):
		return formatZIP
	case bytes.HasPrefix(content, oleMagic):
		return formatOLE
	case ext == ".pdf":
		return formatPDF
	}
	return formatText
}

// classify decides the document type from the ZIP entries; the extension only breaks ODF ties.
func classify(ext string, files []*zip.File) format {
	var word, slides, workbook, content bool
	var mimetype *zip.File
	for _, entry := range files {
		switch name := entry.Name; {
		case name == "word/document.xml":
			word = true
		case name == "xl/workbook.xml":
			workbook = true
		case name == "ppt/presentation.xml" || strings.HasPrefix(name, "ppt/slides/"):
			slides = true
		case name == "content.xml":
			content = true
		case name == "mimetype":
			mimetype = entry
		}
	}
	if mimetype != nil && mimetype.UncompressedSize64 <= 256 {
		if file, err := mimetype.Open(); err == nil {
			value, _ := io.ReadAll(io.LimitReader(file, 257))
			file.Close()
			switch strings.TrimSpace(string(value)) {
			case "application/vnd.oasis.opendocument.text":
				return formatODT
			case "application/vnd.oasis.opendocument.presentation":
				return formatODP
			case "application/vnd.oasis.opendocument.spreadsheet":
				return formatODS
			}
		}
	}
	switch {
	case word:
		return formatDOCX
	case workbook:
		return formatXLSX
	case slides:
		return formatPPTX
	case content:
		switch ext {
		case ".odp":
			return formatODP
		case ".ods":
			return formatODS
		}
		return formatODT
	}
	return formatZIP
}

// oleError tells encrypted OOXML (an OLE container around EncryptedPackage) from legacy binary Office files.
func oleError(content []byte) error {
	if bytes.Contains(content, []byte("E\x00n\x00c\x00r\x00y\x00p\x00t\x00e\x00d\x00P\x00a\x00c\x00k\x00a\x00g\x00e\x00")) {
		return fail(ErrEncrypted, "password-protected Office document; encrypted files are not supported")
	}
	return fail(ErrUnsupported, "legacy binary Office format (.doc/.ppt/.xls) is not supported; save it as docx, pptx, xlsx or PDF")
}
