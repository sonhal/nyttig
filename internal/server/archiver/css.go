package archiver

import (
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
)

// cssRef is one `url(...)` or `@import` found in a stylesheet. [Start, End)
// is the span to replace.
type cssRef struct {
	Start, End int
	URL        string
	Import     bool
	Font       bool   // inside @font-face
	Media      string // @import only: the media query list, if any
}

// scanCSS finds url() and @import references. It skips comments and strings
// and tracks @font-face blocks so fonts can be fetched last.
func scanCSS(css string) []cssRef {
	var refs []cssRef
	var blocks []bool // one entry per open {, true for @font-face
	fontPending := false
	inFont := func() bool {
		for _, b := range blocks {
			if b {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(css); {
		c := css[i]
		switch {
		case c == '/' && strings.HasPrefix(css[i:], "/*"):
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				return refs
			}
			i += end + 4
		case c == '"' || c == '\'':
			_, i = readCSSString(css, i)
		case c == '{':
			blocks = append(blocks, fontPending)
			fontPending = false
			i++
		case c == '}':
			if len(blocks) > 0 {
				blocks = blocks[:len(blocks)-1]
			}
			i++
		case c == '@':
			name, j := readCSSIdent(css, i+1)
			switch strings.ToLower(name) {
			case "font-face":
				fontPending = true
				i = j
			case "import":
				ref, end := scanImport(css, i, j)
				if ref != nil {
					refs = append(refs, *ref)
				}
				i = end
			default:
				i = j
			}
		case (c == 'u' || c == 'U') && (i == 0 || !isIdentChar(css[i-1])) && len(css) >= i+4 && strings.EqualFold(css[i:i+4], "url("):
			val, end, ok := readURLToken(css, i+4)
			if ok {
				refs = append(refs, cssRef{Start: i, End: end, URL: val, Font: inFont()})
			}
			i = end
		default:
			i++
		}
	}
	return refs
}

// scanImport parses `@import <string|url()> [media];` starting after the
// at-keyword. It returns nil for a malformed rule and the index to resume at.
func scanImport(css string, start, i int) (*cssRef, int) {
	i = skipCSSSpace(css, i)
	var val string
	switch {
	case i < len(css) && (css[i] == '"' || css[i] == '\''):
		val, i = readCSSString(css, i)
	case len(css) >= i+4 && strings.EqualFold(css[i:i+4], "url("):
		var ok bool
		val, i, ok = readURLToken(css, i+4)
		if !ok {
			return nil, i
		}
	default:
		return nil, i
	}
	// The media list runs to the semicolon, skipping strings and parens.
	mediaStart := i
	for i < len(css) && css[i] != ';' {
		switch css[i] {
		case '"', '\'':
			_, i = readCSSString(css, i)
			continue
		case '{', '}':
			// Malformed: no semicolon before a block. Stop here.
			return &cssRef{Start: start, End: i, URL: val, Import: true, Media: strings.TrimSpace(css[mediaStart:i])}, i
		}
		i++
	}
	media := strings.TrimSpace(css[mediaStart:min(i, len(css))])
	if i < len(css) {
		i++ // the semicolon
	}
	return &cssRef{Start: start, End: i, URL: val, Import: true, Media: media}, i
}

func skipCSSSpace(css string, i int) int {
	for i < len(css) && strings.IndexByte(" \t\r\n\f", css[i]) >= 0 {
		i++
	}
	return i
}

func isIdentChar(c byte) bool {
	return c == '-' || c == '_' || c >= 0x80 || (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

func readCSSIdent(css string, i int) (string, int) {
	start := i
	for i < len(css) && isIdentChar(css[i]) {
		i++
	}
	return css[start:i], i
}

// readCSSString reads a quoted string starting at the quote and returns its
// decoded value and the index after the closing quote (or the end of input
// or an unescaped newline, which CSS treats as a bad string).
func readCSSString(css string, i int) (string, int) {
	quote := css[i]
	i++
	var sb strings.Builder
	for i < len(css) {
		c := css[i]
		switch c {
		case quote:
			return sb.String(), i + 1
		case '\n':
			return sb.String(), i
		case '\\':
			var r string
			r, i = readCSSEscape(css, i)
			sb.WriteString(r)
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return sb.String(), i
}

// readCSSEscape decodes the escape at css[i] == '\\'.
func readCSSEscape(css string, i int) (string, int) {
	i++
	if i >= len(css) {
		return "", i
	}
	if css[i] == '\n' { // line continuation
		return "", i + 1
	}
	j := i
	for j < len(css) && j-i < 6 && isHex(css[j]) {
		j++
	}
	if j == i {
		return css[i : i+1], i + 1
	}
	n, _ := strconv.ParseInt(css[i:j], 16, 32)
	if j < len(css) && strings.IndexByte(" \t\n\f", css[j]) >= 0 {
		j++
	}
	if n == 0 || n > 0x10FFFF || (n >= 0xD800 && n <= 0xDFFF) {
		n = 0xFFFD
	}
	return string(rune(n)), j
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'f')
}

// readURLToken reads the inside of url( starting just after the paren and
// returns the value and the index after the closing paren.
func readURLToken(css string, i int) (string, int, bool) {
	i = skipCSSSpace(css, i)
	if i >= len(css) {
		return "", i, false
	}
	if css[i] == '"' || css[i] == '\'' {
		val, j := readCSSString(css, i)
		j = skipCSSSpace(css, j)
		if j < len(css) && css[j] == ')' {
			return val, j + 1, true
		}
		return "", j, false
	}
	var sb strings.Builder
	for i < len(css) {
		c := css[i]
		switch c {
		case ')':
			return strings.TrimSpace(sb.String()), i + 1, true
		case '\\':
			var r string
			r, i = readCSSEscape(css, i)
			sb.WriteString(r)
		case '"', '\'', '(':
			return "", i, false
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return "", i, false
}

// rewriteCSS replaces each ref's span with repl(ref).
func rewriteCSS(css string, refs []cssRef, repl func(cssRef) string) string {
	var sb strings.Builder
	last := 0
	for _, r := range refs {
		sb.WriteString(css[last:r.Start])
		sb.WriteString(repl(r))
		last = r.End
	}
	sb.WriteString(css[last:])
	return sb.String()
}

// decodeCSS returns the stylesheet as UTF-8: UTF-8 as is (CSS's default),
// anything else read as windows-1252.
func decodeCSS(e *asset) string {
	b := e.body
	b = []byte(strings.TrimPrefix(string(b), "\ufeff"))
	if utf8.Valid(b) {
		return string(b)
	}
	r, err := charset.NewReaderLabel("windows-1252", strings.NewReader(string(b)))
	if err != nil {
		return strings.ToValidUTF8(string(b), "\ufffd")
	}
	out, _ := io.ReadAll(r)
	return string(out)
}
