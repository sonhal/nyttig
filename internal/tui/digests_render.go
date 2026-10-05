package tui

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// Rendering of a digest body for the terminal: the same Markdown subset as the
// web app (headings, paragraphs, lists, quotes, code blocks, rules, strong,
// emphasis, code, links, plain https:// addresses and [#123] item
// references), as styled and wrapped lines.
//
// A digest body is untrusted (an LLM wrote it, and it can repeat the markup
// and the escape sequences of the feed it read). Nothing from it reaches the
// terminal raw: control characters (ESC included) and bidirectional overrides
// are removed before anything else happens, so the only escape sequences in
// the output are the ones lipgloss writes for our own styles. Work is bounded:
// the input is cut at maxMarkdownInput, quotes and lists indent at most three
// levels, and every search for a closing marker is cached, so a body of
// unclosed markers takes time linear in its size.

const (
	maxMarkdownInput = 256 * 1024
	maxMarkdownDepth = 3
	maxLinkTarget    = 2048
	// maxLinkParens caps the parentheses nested in a link target (CommonMark
	// also stops at 32). Every "](" holds a "(", so a target scan passes at
	// most this many other links: without it, a body of "[a](" repeated
	// scanned maxLinkTarget bytes for each one.
	maxLinkParens = 32
	maxRefTitle   = 60
)

var (
	mdStrong = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	mdEm     = lipgloss.NewStyle().Italic(true)
	mdCode   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6A9955"))
	mdLink   = lipgloss.NewStyle().Foreground(lipgloss.Color("#569CD6")).Underline(true)
	mdRef    = lipgloss.NewStyle().Foreground(lipgloss.Color("#569CD6"))
	mdDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	mdHead   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	mdHead1  = mdHead.Underline(true)
)

// stripBidi removes the Unicode bidirectional overrides and isolates, which
// make text read differently from how it is stored.
func stripBidi(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E, r == 0x200F, r == 0x061C:
			return -1
		}
		return r
	}, s)
}

// cleanText makes a body safe to print: valid UTF-8, no control characters but
// newline and tab, no bidirectional overrides.
func cleanText(s string) string {
	return stripBidi(SanitizeText(strings.ToValidUTF8(s, "�")))
}

// cleanLine makes one-line untrusted text (a title, a name) safe to print.
func cleanLine(s string) string {
	return stripBidi(SanitizeLine(strings.ToValidUTF8(s, "�")))
}

// ── Spans ─────────────────────────────────────────────────────

type spanKind int

const (
	spText spanKind = iota
	spStrong
	spEm
	spCode
	spLink
	spRef
	spDim
	spHead
	spHead1
)

type span struct {
	text string
	kind spanKind
}

func (k spanKind) style() lipgloss.Style {
	switch k {
	case spStrong:
		return mdStrong
	case spEm:
		return mdEm
	case spCode:
		return mdCode
	case spLink:
		return mdLink
	case spRef:
		return mdRef
	case spDim:
		return mdDim
	case spHead:
		return mdHead
	case spHead1:
		return mdHead1
	}
	return lipgloss.NewStyle()
}

// finder remembers where the next closing marker is (see Finder in the web
// app's markdown.ts): the scan only moves forward, so a search that found
// nothing is not repeated for every opening marker.
type finder struct {
	cache map[string][2]int // key -> {from, pos}
}

func (f *finder) find(key string, from int, search func(from int) int) int {
	if c, ok := f.cache[key]; ok && from >= c[0] && (c[1] == -1 || from <= c[1]) {
		return c[1]
	}
	pos := search(from)
	f.cache[key] = [2]int{from, pos}
	return pos
}

// safeURL returns u when it is an absolute http(s) URL with a host.
func safeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '/'
}

// inlineSpans splits one line of text into styled spans. refs resolves [#id]
// to the items the digest is based on.
func inlineSpans(s string, refs map[int64]*pb.DigestItem) []span {
	var out []span
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, span{buf.String(), spText})
			buf.Reset()
		}
	}
	f := &finder{cache: map[string][2]int{}}
	targets := map[int]*linkTarget{}

	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && strings.IndexByte("\\`*[]()#<>_-+.!|~{}", s[i+1]) >= 0:
			buf.WriteByte(s[i+1])
			i += 2

		case c == '`':
			n := 1
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			mark := strings.Repeat("`", n)
			end := f.find("code"+strconv.Itoa(n), i+n, func(from int) int {
				if j := strings.Index(s[from:], mark); j >= 0 {
					return from + j
				}
				return -1
			})
			if end >= 0 {
				flush()
				out = append(out, span{s[i+n : end], spCode})
				i = end + n
			} else {
				buf.WriteString(mark)
				i += n
			}

		case c == '*':
			if i+1 < len(s) && s[i+1] == '*' && i+2 < len(s) && s[i+2] != ' ' && s[i+2] != '\t' {
				end := f.find("strong", i+2, func(from int) int {
					if j := strings.Index(s[from:], "**"); j >= 0 {
						return from + j
					}
					return -1
				})
				if end > i+2 {
					flush()
					out = append(out, span{s[i+2 : end], spStrong})
					i = end + 2
					continue
				}
			} else if i+1 < len(s) && s[i+1] != '*' && s[i+1] != ' ' && s[i+1] != '\t' {
				end := f.find("em", i+1, func(from int) int {
					for j := from; j < len(s); j++ {
						if s[j] == '*' && (j == 0 || s[j-1] != '*') && (j+1 >= len(s) || s[j+1] != '*') {
							return j
						}
					}
					return -1
				})
				if end > i+1 {
					flush()
					out = append(out, span{s[i+1 : end], spEm})
					i = end + 1
					continue
				}
			}
			// A run of unmatched stars is text, taken whole.
			n := 1
			for i+n < len(s) && s[i+n] == '*' {
				n++
			}
			buf.WriteString(s[i : i+n])
			i += n

		case c == '[':
			if id, n := itemRef(s[i:]); n > 0 {
				flush()
				if it := refs[id]; it != nil && safeURL(it.Link) != "" {
					out = append(out, span{"[" + truncateRunes(cleanLine(it.Title), maxRefTitle) + "]", spRef})
				} else {
					out = append(out, span{s[i : i+n], spText})
				}
				i += n
				continue
			}
			mid := f.find("bracket", i+1, func(from int) int {
				if j := strings.Index(s[from:], "]("); j >= 0 {
					return from + j
				}
				return -1
			})
			if mid >= 0 {
				if tg := linkTargetAt(s, mid, targets); tg != nil {
					flush()
					text := s[i+1 : mid]
					if tg.href != "" {
						out = append(out, span{text, spLink}, span{" (" + tg.href + ")", spDim})
					} else {
						out = append(out, span{text, spText})
					}
					i = tg.end + 1
					continue
				}
			}
			buf.WriteByte(c)
			i++

		case c == 'h' && (strings.HasPrefix(s[i:], "http://") || strings.HasPrefix(s[i:], "https://")) && !prevIsWord(s, i):
			j := i
			for j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != '<' && s[j] != '>' && s[j] != '"' {
				j++
			}
			raw := strings.TrimRight(s[i:j], ".,;:!?)]'*")
			if raw == "" {
				buf.WriteByte(c)
				i++
				continue
			}
			flush()
			if safeURL(raw) != "" {
				out = append(out, span{raw, spLink})
			} else {
				out = append(out, span{raw, spText})
			}
			i += len(raw)

		default:
			buf.WriteByte(c)
			i++
		}
	}
	flush()
	return out
}

func prevIsWord(s string, i int) bool {
	if i == 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return isWordRune(r)
}

// itemRef reads "[#123]" at the start of s: the ID and the length, or 0.
func itemRef(s string) (int64, int) {
	if !strings.HasPrefix(s, "[#") {
		return 0, 0
	}
	j := 2
	for j < len(s) && j < 2+18 && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == 2 || j >= len(s) || s[j] != ']' {
		return 0, 0
	}
	id, err := strconv.ParseInt(s[2:j], 10, 64)
	if err != nil {
		return 0, 0
	}
	return id, j + 1
}

type linkTarget struct {
	href string // "" when the target is not a safe URL
	end  int    // where the closing ")" is
}

// linkTargetAt reads the target of the link whose "](" is at pos: one URL
// without whitespace, parentheses balanced, at most maxLinkTarget long. It is
// remembered, so the many "[" before one "](" do not each scan it again.
func linkTargetAt(s string, pos int, memo map[int]*linkTarget) *linkTarget {
	if t, ok := memo[pos]; ok {
		return t
	}
	var res *linkTarget
	open := 0
	for k := pos + 2; k < len(s) && k-pos <= maxLinkTarget; k++ {
		ch := s[k]
		if ch == '(' {
			open++
			if open > maxLinkParens {
				break
			}
		} else if ch == ')' {
			if open == 0 {
				if raw := s[pos+2 : k]; raw != "" {
					res = &linkTarget{href: safeURL(raw), end: k}
				}
				break
			}
			open--
		} else if ch == ' ' || ch == '\t' {
			break
		}
	}
	memo[pos] = res
	return res
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// ── Wrapping ──────────────────────────────────────────────────

type token struct {
	text  string
	kind  spanKind
	space bool // a space comes before it
}

// wrapSpans lays the spans out in lines of at most width cells, the first
// after prefix and the others after cont, which has the same width.
func wrapSpans(spans []span, prefix, cont string, width int) []string {
	avail := width - lipgloss.Width(prefix)
	if avail < 10 {
		avail = 10
	}
	var lines []string
	var cur strings.Builder
	w := 0
	first := true
	emit := func() {
		p := cont
		if first {
			p = prefix
		}
		lines = append(lines, p+cur.String())
		cur.Reset()
		w = 0
		first = false
	}
	for _, tk := range spansToTokens(spans) {
		word := tk.text
		for lipgloss.Width(word) > avail {
			// A word longer than a line is cut where the line ends.
			if w > 0 {
				emit()
			}
			head, rest := cutWidth(word, avail)
			cur.WriteString(tk.kind.style().Render(head))
			w = lipgloss.Width(head)
			emit()
			word = rest
		}
		if word == "" {
			continue
		}
		ww := lipgloss.Width(word)
		sep := 0
		if tk.space && w > 0 {
			sep = 1
		}
		if w > 0 && w+sep+ww > avail {
			emit()
			sep = 0
		}
		if sep == 1 {
			cur.WriteByte(' ')
			w++
		}
		cur.WriteString(tk.kind.style().Render(word))
		w += ww
	}
	if w > 0 || len(lines) == 0 {
		emit()
	}
	return lines
}

// cutWidth splits s after at most width cells.
func cutWidth(s string, width int) (string, string) {
	w := 0
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > width {
			if i == 0 {
				return s[:utf8.RuneLen(r)], s[utf8.RuneLen(r):]
			}
			return s[:i], s[i:]
		}
		w += rw
	}
	return s, ""
}

// spansToTokens is tokens with the spaces inside spans kept: "a b" in one span
// is two words with a space between.
func spansToTokens(spans []span) []token {
	var out []token
	space := false
	for _, sp := range spans {
		var word strings.Builder
		flush := func() {
			if word.Len() > 0 {
				out = append(out, token{word.String(), sp.kind, space})
				word.Reset()
				space = false
			}
		}
		for _, r := range sp.text {
			if r == ' ' || r == '\t' {
				flush()
				space = true
				continue
			}
			word.WriteRune(r)
		}
		flush()
	}
	return out
}

// ── Blocks ────────────────────────────────────────────────────

var (
	mdFenceRe   = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	mdHeadingRe = regexp.MustCompile(`^ {0,3}(#{1,6})[ \t]+(.*)$`)
	mdListRe    = regexp.MustCompile(`^( *)([-*+]|\d{1,9}[.)])[ \t]+(\S.*)$`)
	mdQuoteRe   = regexp.MustCompile(`^ {0,3}>[ \t]?`)
)

func isRule(line string) bool {
	t := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, line)
	if len(t) < 3 || len(line)-len(strings.TrimLeft(line, " ")) > 3 {
		return false
	}
	return strings.Trim(t, t[:1]) == "" && strings.ContainsAny(t[:1], "-*_")
}

// renderMarkdown renders a digest body to lines of at most width cells. items
// are the ones the digest is based on, by ID, for [#id] references. The body
// is cleaned first; see the note at the top of this file.
func renderMarkdown(body string, items []*pb.DigestItem, width int) []string {
	if width < 20 {
		width = 20
	}
	if len(body) > maxMarkdownInput {
		body = body[:maxMarkdownInput]
	}
	refs := make(map[int64]*pb.DigestItem, len(items))
	for _, it := range items {
		refs[it.ItemId] = it
	}

	var out []string
	inFence := false
	fenceMark := ""
	for _, line := range strings.Split(cleanText(body), "\n") {
		line = strings.ReplaceAll(line, "\t", "    ")

		if inFence {
			if strings.HasPrefix(strings.TrimSpace(line), fenceMark) && strings.Trim(strings.TrimSpace(line), fenceMark[:1]) == "" {
				inFence = false
				continue
			}
			out = append(out, wrapSpans([]span{{line, spCode}}, "  ", "  ", width)...)
			continue
		}
		if m := mdFenceRe.FindStringSubmatch(line); m != nil {
			inFence, fenceMark = true, m[1]
			continue
		}
		if strings.TrimSpace(line) == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		if isRule(line) {
			n := width
			if n > 40 {
				n = 40
			}
			out = append(out, mdDim.Render(strings.Repeat("─", n)))
			continue
		}

		prefix := ""
		for depth := 0; depth < maxMarkdownDepth; depth++ {
			m := mdQuoteRe.FindString(line)
			if m == "" {
				break
			}
			line = line[len(m):]
			prefix += mdDim.Render("│ ")
		}
		cont := prefix

		if m := mdHeadingRe.FindStringSubmatch(line); m != nil {
			kind := spHead
			if len(m[1]) == 1 {
				kind = spHead1
			}
			text := strings.TrimRight(strings.TrimRight(m[2], " \t"), "#")
			spans := inlineSpans(strings.TrimRight(text, " \t"), refs)
			for k := range spans {
				spans[k].kind = kind
			}
			out = append(out, wrapSpans(spans, prefix, cont, width)...)
			continue
		}
		if m := mdListRe.FindStringSubmatch(line); m != nil {
			level := len(m[1]) / 2
			if level > maxMarkdownDepth {
				level = maxMarkdownDepth
			}
			bullet := "• "
			if m[2][0] >= '0' && m[2][0] <= '9' {
				bullet = m[2][:len(m[2])-1] + ". "
			}
			pad := strings.Repeat("  ", level)
			prefix += pad + mdDim.Render(bullet)
			cont += pad + strings.Repeat(" ", lipgloss.Width(bullet))
			line = m[3]
		}
		out = append(out, wrapSpans(inlineSpans(line, refs), prefix, cont, width)...)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
