package store

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

var (
	imgTag     = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	imgAttr    = regexp.MustCompile(`(?is)\b(src|alt)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>"']+))`)
	listItem   = regexp.MustCompile(`(?is)<li\b[^>]*>`)
	blockTag   = regexp.MustCompile(`(?is)</?(?:br|p|div|blockquote|pre|h[1-6]|hr|table)\b[^>]*>|</tr\b[^>]*>`)
	listEnd    = regexp.MustCompile(`(?is)</li\s*>|</?(?:ul|ol)\b[^>]*>|<tr\b[^>]*>`)
	cellTag    = regexp.MustCompile(`(?is)</?(?:td|th)\b[^>]*>`)
	spaceRun   = regexp.MustCompile(`[ \t\f\v]+`)
	blankLines = regexp.MustCompile(`\n{3,}`)
)

// HTMLText turns HTML into plain text that keeps its line structure: block tags,
// <br> and list items become line breaks (list items start with "- "), blank
// lines are collapsed and entities are unescaped. Unlike PlainHTML it never
// fetches or interprets anything. Each <img> is replaced in place by whatever
// image returns for its src and alt (nil drops images); image is called in
// document order. Script and style content is dropped.
func HTMLText(markup string, image func(src, alt string) string) string {
	// NUL delimits the image placeholders below, so it must not occur in the input.
	markup = scripts.ReplaceAllString(strings.ReplaceAll(markup, "\x00", ""), " ")
	// Markers go in after tag stripping and unescaping, so their text (a file
	// name may contain "<" or "&") is never reinterpreted as markup.
	var markers []string
	markup = imgTag.ReplaceAllStringFunc(markup, func(tag string) string {
		if image == nil {
			return ""
		}
		var src, alt string
		for _, m := range imgAttr.FindAllStringSubmatch(tag, -1) {
			value := html.UnescapeString(m[2] + m[3] + m[4])
			if strings.EqualFold(m[1], "src") {
				src = value
			} else {
				alt = value
			}
		}
		markers = append(markers, image(src, alt))
		return fmt.Sprintf("\x00%d\x00", len(markers)-1)
	})
	markup = listItem.ReplaceAllString(markup, "\n- ")
	markup = listEnd.ReplaceAllString(markup, "")
	markup = blockTag.ReplaceAllString(markup, "\n")
	markup = cellTag.ReplaceAllString(markup, " ")
	text := html.UnescapeString(tags.ReplaceAllString(markup, ""))
	text = restoreMarkers(strings.ReplaceAll(text, "\u00a0", " "), markers)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(spaceRun.ReplaceAllString(line, " "))
	}
	return strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// restoreMarkers swaps the placeholders left by HTMLText for the image markers.
// Text that merely looks like a placeholder ends up unchanged because only
// indexes in range are replaced.
func restoreMarkers(text string, markers []string) string {
	if len(markers) == 0 {
		return text
	}
	parts := strings.Split(text, "\x00")
	var out strings.Builder
	for i, part := range parts {
		if i%2 == 0 {
			out.WriteString(part)
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n >= 0 && n < len(markers) {
			out.WriteString(markers[n])
		}
	}
	return out.String()
}
