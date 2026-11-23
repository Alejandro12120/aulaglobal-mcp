package store

import (
	"fmt"
	"testing"
)

func TestHTMLText(t *testing.T) {
	n := 0
	image := func(src, alt string) string {
		if src == "" {
			return "[external image]"
		}
		n++
		return fmt.Sprintf("[image %d: %s|%s]", n, src, alt)
	}
	for _, tc := range []struct{ name, in, want string }{
		{"paragraphs and breaks", "<p>One &amp; two</p><p>Three<br/>four<br>five</p>", "One & two\n\nThree\nfour\nfive"},
		{"blank lines collapse", "<p>a</p>\n\n\n<p></p><p></p><p>b</p>", "a\n\nb"},
		{"list", "<ul><li>first</li><li><b>second</b> item</li></ul>", "- first\n- second item"},
		{"inline tags join", "hel<b>lo</b> <i>wor</i>ld", "hello world"},
		{"script and style", "a<script>alert('x')</script><style>p{}</style>b", "a b"},
		{"entities and nbsp", "x&nbsp;&nbsp;y &lt;z&gt; &#233;", "x y <z> é"},
		{"image in place", `<p>See <img src="https://a/x.png" alt="Fig &amp; 1"> here</p>`, "See [image 1: https://a/x.png|Fig & 1] here"},
		{"single quotes", `<img alt='q' src='/y.png'>`, "[image 1: /y.png|q]"},
		{"unquoted", `<img src=/z.png>`, "[image 1: /z.png|]"},
		{"no src", `<img alt="only">`, "[external image]"},
		{"marker not reparsed", `<img src="a&lt;b.png">`, "[image 1: a<b.png|]"},
		{"table", "<table><tr><td>a</td><td>b</td></tr><tr><td>c</td><td>d</td></tr></table>", "a b\nc d"},
		{"nul in input", "a\x000\x00b", "a0b"},
		{"empty", "", ""},
	} {
		n = 0
		t.Run(tc.name, func(t *testing.T) {
			if got := HTMLText(tc.in, image); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := HTMLText(`a<img src="x.png">b`, nil); got != "ab" {
		t.Errorf("nil image func: %q", got)
	}
	// PlainHTML, used by list_assignments, keeps its behaviour.
	if got := PlainHTML("<p>a</p><p>b</p>"); got != " a  b " {
		t.Errorf("PlainHTML changed: %q", got)
	}
}
