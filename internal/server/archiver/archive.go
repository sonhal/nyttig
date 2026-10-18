// Package archiver saves a web page as one self-contained file: HTML with
// its stylesheets, images and fonts inlined and everything active removed, or
// a PDF as fetched. It does no database access.
package archiver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

const (
	// minTextLength is the visible text under which a saved page gets a note.
	minTextLength  = 500
	littleTextNote = "little text; may be a consent wall or paywall"

	// csp lets a copy opened from disk load nothing from the network.
	csp = "default-src 'none'; img-src data:; style-src 'unsafe-inline' data:; font-src data:; media-src data:"
)

// now is replaced in tests.
var now = time.Now

// Archive downloads pageURL and returns a self-contained copy. An error means
// the archive failed; its message is short enough to show to the user.
func Archive(ctx context.Context, client *http.Client, pageURL string, lim Limits) (*Result, error) {
	lim = withDefaults(lim)
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, lim.Deadline)
	defer cancel()

	u, err := url.Parse(pageURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("not an http(s) link")
	}
	fail := func(err error) (*Result, error) {
		if parent.Err() != nil {
			return nil, errors.New("canceled")
		}
		return nil, errors.New(shortError(err))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("not an http(s) link")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/pdf;q=0.9,*/*;q=0.5")
	resp, err := client.Do(req)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	final := resp.Request.URL

	br := bufio.NewReader(resp.Body)
	mt := mediaType(resp.Header.Get("Content-Type"))
	if mt == "" || mt == "application/octet-stream" || mt == "binary/octet-stream" {
		head, _ := br.Peek(512)
		mt = mediaType(http.DetectContentType(head))
	}
	res := &Result{FinalURL: final.String(), HTTPStatus: resp.StatusCode}
	switch mt {
	case "application/pdf":
		body, err := readLimited(br, lim.MaxTotalBytes, "page")
		if err != nil {
			return fail(err)
		}
		res.ContentType, res.Body = "application/pdf", body
		return res, nil
	case "text/html", "application/xhtml+xml":
	default:
		return nil, fmt.Errorf("content type %s is not archived", mt)
	}

	body, err := readLimited(br, lim.MaxPageBytes, "page")
	if err != nil {
		return fail(err)
	}
	r, err := charset.NewReader(bytes.NewReader(body), resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, errors.New("cannot decode page")
	}
	doc, err := html.ParseWithOptions(r, html.ParseOptionEnableScripting(false))
	if err != nil {
		// The parser refuses nesting past 512 elements, which also bounds
		// the recursion in the walk and in html.Render.
		if strings.Contains(err.Error(), "exceeds 512") {
			return nil, errors.New("page markup is nested too deeply")
		}
		return nil, errors.New("cannot parse page")
	}

	res.TextLength = visibleTextLength(doc)
	a := newAssets(client, lim, int64(len(body)))
	inline(ctx, doc, final, a)
	if parent.Err() != nil {
		return nil, errors.New("canceled")
	}
	addHeader(doc, final.String())

	var out bytes.Buffer
	if err := html.Render(&out, doc); err != nil {
		return nil, errors.New("cannot write page")
	}
	res.ContentType = "text/html; charset=utf-8"
	res.Body = out.Bytes()
	res.MissingAssets = a.missingCount()
	if res.TextLength < minTextLength {
		res.Note = littleTextNote
	}
	return res, nil
}

func withDefaults(lim Limits) Limits {
	d := DefaultLimits()
	if lim.MaxPageBytes <= 0 {
		lim.MaxPageBytes = d.MaxPageBytes
	}
	if lim.MaxAssetBytes <= 0 {
		lim.MaxAssetBytes = d.MaxAssetBytes
	}
	if lim.MaxTotalBytes <= 0 {
		lim.MaxTotalBytes = d.MaxTotalBytes
	}
	if lim.MaxAssets <= 0 {
		lim.MaxAssets = d.MaxAssets
	}
	if lim.Parallel <= 0 {
		lim.Parallel = d.Parallel
	}
	if lim.Deadline <= 0 {
		lim.Deadline = d.Deadline
	}
	return lim
}

type tooLargeError struct {
	what string
	max  int64
}

func (e tooLargeError) Error() string {
	return fmt.Sprintf("%s larger than %s", e.what, formatSize(e.max))
}

func formatSize(n int64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d bytes", n)
}

func readLimited(r io.Reader, max int64, what string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, tooLargeError{what, max}
	}
	return b, nil
}

// shortError turns a transport error into a one-line reason.
func shortError(err error) string {
	var tl tooLargeError
	if errors.As(err, &tl) {
		return tl.Error()
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return "timed out"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return msg
}

// ── Document ─────────────────────────────────────────────────────

// visibleTextLength counts the characters of the text a reader would see,
// with whitespace collapsed. <noscript> is skipped: it is hidden when the
// page's scripts run, and a script-rendered page often says only "enable
// JavaScript" there.
func visibleTextLength(doc *html.Node) int {
	var sb strings.Builder
	skip := map[atom.Atom]bool{atom.Head: true, atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true}
	var stack []*html.Node
	stack = append(stack, doc)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch {
		case n.Type == html.TextNode:
			sb.WriteString(n.Data)
			sb.WriteByte(' ')
			continue
		case n.Type == html.ElementNode && skip[n.DataAtom]:
			continue
		}
		// Push children in reverse so text is read in document order.
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	return utf8.RuneCountInString(strings.Join(strings.Fields(sb.String()), " "))
}

// addHeader writes the charset and CSP meta tags as the first children of
// <head> and a provenance comment before <html>.
func addHeader(doc *html.Node, source string) {
	var head, root *html.Node
	forEach(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.Html:
				root = n
			case atom.Head:
				head = n
				return false
			}
		}
		return true
	})
	if head != nil {
		cspMeta := &html.Node{Type: html.ElementNode, DataAtom: atom.Meta, Data: "meta",
			Attr: []html.Attribute{{Key: "http-equiv", Val: "Content-Security-Policy"}, {Key: "content", Val: csp}}}
		charsetMeta := &html.Node{Type: html.ElementNode, DataAtom: atom.Meta, Data: "meta",
			Attr: []html.Attribute{{Key: "charset", Val: "utf-8"}}}
		first := head.FirstChild
		head.InsertBefore(charsetMeta, first)
		head.InsertBefore(cspMeta, first)
	}
	if root != nil {
		safe := strings.NewReplacer("--", "-%2D", ">", "%3E", "<", "%3C").Replace(source)
		comment := &html.Node{Type: html.CommentNode,
			Data: fmt.Sprintf(" Archived from %s at %s ", safe, now().UTC().Format("2006-01-02 15:04:05")+" UTC")}
		root.Parent.InsertBefore(comment, root)
	}
}
