package store

import (
	"bytes"
	"strconv"
	"unicode/utf8"
)

type tokenKind uint8

const (
	tokenEOF tokenKind = iota
	tokenStart
	tokenEnd
	tokenText
)

var errXML = fail(ErrMalformed, "invalid document XML")

// xmlScanner is a zero-allocation pull tokenizer for the well-formed subset that Office packages use.
// encoding/xml allocates per token and validates names with Unicode tables, which dominated extraction time.
// Names are local (prefix removed); end tags are not matched, so callers keep their own depth.
type xmlScanner struct {
	data       []byte
	pos        int
	name       []byte
	attrs      []byte
	text       []byte
	scratch    []byte
	pendingEnd bool
}

func newScanner(data []byte) *xmlScanner {
	return &xmlScanner{data: bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))}
}

func isXMLSpace(char byte) bool { return char == ' ' || char == '\t' || char == '\n' || char == '\r' }

func localName(name []byte) []byte {
	if colon := bytes.LastIndexByte(name, ':'); colon >= 0 {
		return name[colon+1:]
	}
	return name
}

func (scanner *xmlScanner) next() (tokenKind, error) {
	if scanner.pendingEnd {
		scanner.pendingEnd = false
		return tokenEnd, nil
	}
	for scanner.pos < len(scanner.data) {
		data := scanner.data
		if data[scanner.pos] != '<' {
			end := bytes.IndexByte(data[scanner.pos:], '<')
			if end < 0 {
				end = len(data) - scanner.pos
			}
			scanner.text = scanner.decode(data[scanner.pos : scanner.pos+end])
			scanner.pos += end
			return tokenText, nil
		}
		if scanner.pos+1 >= len(data) {
			return tokenEOF, errXML
		}
		switch data[scanner.pos+1] {
		case '/':
			end := bytes.IndexByte(data[scanner.pos:], '>')
			if end < 0 {
				return tokenEOF, errXML
			}
			scanner.name = localName(bytes.TrimSpace(data[scanner.pos+2 : scanner.pos+end]))
			scanner.pos += end + 1
			return tokenEnd, nil
		case '?':
			if !scanner.skipTo("?>") {
				return tokenEOF, errXML
			}
		case '!':
			rest := data[scanner.pos:]
			switch {
			case bytes.HasPrefix(rest, []byte("<!--")):
				if !scanner.skipTo("-->") {
					return tokenEOF, errXML
				}
			case bytes.HasPrefix(rest, []byte("<![CDATA[")):
				end := bytes.Index(rest, []byte("]]>"))
				if end < 0 {
					return tokenEOF, errXML
				}
				scanner.text = rest[9:end]
				scanner.pos += end + 3
				return tokenText, nil
			default:
				if !scanner.skipDeclaration() {
					return tokenEOF, errXML
				}
			}
		default:
			return scanner.startTag()
		}
	}
	return tokenEOF, nil
}

func (scanner *xmlScanner) skipTo(terminator string) bool {
	end := bytes.Index(scanner.data[scanner.pos:], []byte(terminator))
	if end < 0 {
		return false
	}
	scanner.pos += end + len(terminator)
	return true
}

// skipDeclaration skips <!DOCTYPE ...>, including an internal subset in brackets.
func (scanner *xmlScanner) skipDeclaration() bool {
	depth := 0
	for index := scanner.pos; index < len(scanner.data); index++ {
		switch scanner.data[index] {
		case '[':
			depth++
		case ']':
			depth--
		case '>':
			if depth <= 0 {
				scanner.pos = index + 1
				return true
			}
		}
	}
	return false
}

func (scanner *xmlScanner) startTag() (tokenKind, error) {
	data := scanner.data
	index := scanner.pos + 1
	nameStart := index
	for index < len(data) && !isXMLSpace(data[index]) && data[index] != '>' && data[index] != '/' {
		index++
	}
	if index == nameStart {
		return tokenEOF, errXML
	}
	scanner.name = localName(data[nameStart:index])
	attrStart := index
	var quote byte
	for ; index < len(data); index++ {
		char := data[index]
		if quote != 0 {
			if char == quote {
				quote = 0
			}
		} else if char == '"' || char == '\'' {
			quote = char
		} else if char == '>' {
			break
		}
	}
	if index >= len(data) {
		return tokenEOF, errXML
	}
	scanner.attrs = data[attrStart:index]
	if n := len(scanner.attrs); n > 0 && scanner.attrs[n-1] == '/' {
		scanner.attrs = scanner.attrs[:n-1]
		scanner.pendingEnd = true
	}
	scanner.pos = index + 1
	return tokenStart, nil
}

// nextAttr iterates the attributes of the current start tag; index starts at 0.
func (scanner *xmlScanner) nextAttr(index *int) (name, value []byte, ok bool) {
	attrs := scanner.attrs
	position := *index
	for position < len(attrs) && isXMLSpace(attrs[position]) {
		position++
	}
	nameStart := position
	for position < len(attrs) && attrs[position] != '=' && !isXMLSpace(attrs[position]) {
		position++
	}
	name = attrs[nameStart:position]
	for position < len(attrs) && (isXMLSpace(attrs[position]) || attrs[position] == '=') {
		position++
	}
	if position >= len(attrs) || (attrs[position] != '"' && attrs[position] != '\'') {
		return nil, nil, false
	}
	end := bytes.IndexByte(attrs[position+1:], attrs[position])
	if end < 0 {
		return nil, nil, false
	}
	value = attrs[position+1 : position+1+end]
	*index = position + end + 2
	return name, value, true
}

// attribute returns the raw value of the first attribute with this local name.
func (scanner *xmlScanner) attribute(local string, prefixed bool) []byte {
	for index := 0; ; {
		name, value, ok := scanner.nextAttr(&index)
		if !ok {
			return nil
		}
		if string(localName(name)) == local && (!prefixed || len(name) > len(local)) {
			return value
		}
	}
}

func (scanner *xmlScanner) attr(local string) []byte { return scanner.attribute(local, false) }

// prefixedAttr finds an attribute that carries a namespace prefix such as r:id.
func (scanner *xmlScanner) prefixedAttr(local string) []byte { return scanner.attribute(local, true) }

func (scanner *xmlScanner) attrString(local string) string {
	return string(scanner.decode(scanner.attribute(local, false)))
}

func (scanner *xmlScanner) attrInt(local string, fallback int) int {
	value := scanner.attribute(local, false)
	if len(value) == 0 || len(value) > 9 {
		return fallback
	}
	number := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return fallback
		}
		number = number*10 + int(char-'0')
	}
	return number
}

// decode resolves the predefined and numeric entities. Unknown entities are kept as written.
func (scanner *xmlScanner) decode(raw []byte) []byte {
	first := bytes.IndexByte(raw, '&')
	if first < 0 {
		return raw
	}
	out := append(scanner.scratch[:0], raw[:first]...)
	for index := first; index < len(raw); {
		if raw[index] != '&' {
			out = append(out, raw[index])
			index++
			continue
		}
		end := bytes.IndexByte(raw[index:], ';')
		if end < 0 || end > 12 {
			out = append(out, '&')
			index++
			continue
		}
		entity := raw[index+1 : index+end]
		switch string(entity) {
		case "amp":
			out = append(out, '&')
		case "lt":
			out = append(out, '<')
		case "gt":
			out = append(out, '>')
		case "quot":
			out = append(out, '"')
		case "apos":
			out = append(out, '\'')
		default:
			code, err := int64(0), error(nil)
			if len(entity) > 1 && entity[0] == '#' {
				if entity[1] == 'x' || entity[1] == 'X' {
					code, err = strconv.ParseInt(string(entity[2:]), 16, 32)
				} else {
					code, err = strconv.ParseInt(string(entity[1:]), 10, 32)
				}
			}
			if err != nil || code <= 0 {
				out = append(out, raw[index:index+end+1]...)
			} else {
				out = utf8.AppendRune(out, rune(code))
			}
		}
		index += end + 1
	}
	scanner.scratch = out
	return out
}
