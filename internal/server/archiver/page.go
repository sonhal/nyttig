package archiver

import (
	"context"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// lazyAttrs hold the real image URL on pages that load images with script.
var lazyAttrs = []string{"data-src", "data-lazy-src", "data-original"}

// urlAttrs can carry a script URL.
var urlAttrs = map[string]bool{
	"href": true, "src": true, "action": true, "formaction": true,
	"background": true, "poster": true, "data": true, "cite": true,
}

// sheetNode is a stylesheet source in the document: a <link> or <style>
// element, or a style attribute.
type sheetNode struct {
	node *html.Node
	attr int // index into node.Attr for a style attribute, else -1
	link bool
	*sheet
}

// sheet is CSS text with its references. imports lines up with the import
// refs in refs order; nil means the import was not saved.
type sheet struct {
	base    *url.URL
	text    string
	refs    []cssRef
	imports []*sheet
}

func newSheet(base *url.URL, text string) *sheet {
	return &sheet{base: base, text: text, refs: scanCSS(text)}
}

type imgNode struct {
	node *html.Node
	url  string // absolute; "" when there is nothing to fetch
}

// page rewrites one parsed document into a self-contained one.
type page struct {
	base   *url.URL
	assets *assets
	seen   map[*html.Node]bool
	sheets []*sheetNode
	imgs   []imgNode
}

// inline makes doc self-contained and inert. final is the page's URL after
// redirects.
func inline(ctx context.Context, doc *html.Node, final *url.URL, a *assets) {
	p := &page{base: final, assets: a, seen: map[*html.Node]bool{}}
	p.base = baseURL(doc, final)
	p.walk(doc)
	p.loadSheets(ctx)
	p.fetchAssets(ctx)
	p.render()
}

// baseURL honours the first <base href>, which is removed afterwards.
func baseURL(doc *html.Node, final *url.URL) *url.URL {
	var found *url.URL
	forEach(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.DataAtom == atom.Base {
			if href := attr(n, "href"); href != "" {
				if u, err := final.Parse(href); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
					found = u
					return false
				}
			}
		}
		return true
	})
	if found != nil {
		return found
	}
	return final
}

// ── Walk ─────────────────────────────────────────────────────────

func (p *page) walk(parent *html.Node) {
	for c := parent.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case html.CommentNode:
			parent.RemoveChild(c)
		case html.ElementNode:
			next = p.element(parent, c, next)
			if c.Parent == parent {
				p.walk(c)
			}
		}
		c = next
	}
}

// element rewrites c and returns the node to continue from.
func (p *page) element(parent, c, next *html.Node) *html.Node {
	switch c.DataAtom {
	case atom.Noscript:
		first := next
		for ch := c.FirstChild; ch != nil; ch = c.FirstChild {
			c.RemoveChild(ch)
			parent.InsertBefore(ch, c)
			if first == next {
				first = ch
			}
		}
		parent.RemoveChild(c)
		return first
	case atom.Script, atom.Base, atom.Frame, atom.Frameset, atom.Object, atom.Embed, atom.Applet:
		parent.RemoveChild(c)
		return next
	case atom.Iframe, atom.Video, atom.Audio:
		p.replaceWithLink(parent, c)
		return next
	case atom.Meta:
		if hasAttr(c, "http-equiv") || hasAttr(c, "charset") {
			parent.RemoveChild(c)
			return next
		}
	case atom.Link:
		rel := strings.Fields(strings.ToLower(attr(c, "rel")))
		if !contains(rel, "stylesheet") || contains(rel, "alternate") {
			parent.RemoveChild(c)
			return next
		}
		if u, ok := resolveAsset(p.base, attr(c, "href")); ok {
			c.Attr = []html.Attribute{{Key: "href", Val: u}, {Key: "media", Val: attr(c, "media")}}
			p.sheets = append(p.sheets, &sheetNode{node: c, attr: -1, link: true, sheet: &sheet{base: p.base}})
		} else {
			parent.RemoveChild(c)
		}
		return next
	case atom.Style:
		var sb strings.Builder
		for ch := c.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type == html.TextNode {
				sb.WriteString(ch.Data)
			}
		}
		p.sheets = append(p.sheets, &sheetNode{node: c, attr: -1, sheet: newSheet(p.base, sb.String())})
		p.cleanAttrs(c)
		return next
	case atom.Picture:
		p.picture(c)
	case atom.Img:
		p.img(c, "")
	case atom.Input:
		if strings.EqualFold(attr(c, "type"), "image") {
			removeAttr(c, "src")
		}
	}
	p.cleanAttrs(c)
	return next
}

// cleanAttrs removes event handlers and script URLs, makes links absolute
// and queues style attributes.
func (p *page) cleanAttrs(n *html.Node) {
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		switch {
		case strings.HasPrefix(key, "on"), key == "srcdoc", key == "ping":
			continue
		case urlAttrs[key] && isScriptURL(a.Val):
			continue
		case key == "href" && (n.DataAtom == atom.A || n.DataAtom == atom.Area):
			a.Val = p.absolute(a.Val)
		case key == "action" || key == "formaction":
			a.Val = p.absolute(a.Val)
		}
		kept = append(kept, a)
	}
	n.Attr = kept
	for i, a := range n.Attr {
		if a.Key == "style" {
			p.sheets = append(p.sheets, &sheetNode{node: n, attr: i, sheet: newSheet(p.base, a.Val)})
		}
	}
}

func (p *page) absolute(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || ref[0] == '#' {
		return ref
	}
	u, err := p.base.Parse(ref)
	if err != nil {
		return ref
	}
	return u.String()
}

// isScriptURL reports a javascript:, vbscript: or data: URL the way a
// browser reads it: control characters and spaces are ignored.
func isScriptURL(v string) bool {
	v = strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f {
			return -1
		}
		return r
	}, v)
	v = strings.ToLower(v)
	return strings.HasPrefix(v, "javascript:") || strings.HasPrefix(v, "vbscript:") || strings.HasPrefix(v, "data:text/html")
}

// replaceWithLink swaps an embed for a link to its source, or drops it.
func (p *page) replaceWithLink(parent, c *html.Node) {
	src := attr(c, "src")
	if src == "" {
		forEach(c, func(n *html.Node) bool {
			if n.Type == html.ElementNode && n.DataAtom == atom.Source && attr(n, "src") != "" {
				src = attr(n, "src")
				return false
			}
			return true
		})
	}
	u := p.absolute(src)
	if parsed, err := url.Parse(u); src == "" || err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		parent.RemoveChild(c)
		return
	}
	a := &html.Node{Type: html.ElementNode, DataAtom: atom.A, Data: "a", Attr: []html.Attribute{{Key: "href", Val: u}}}
	a.AppendChild(&html.Node{Type: html.TextNode, Data: u})
	parent.InsertBefore(a, c)
	parent.RemoveChild(c)
}

// ── Images ───────────────────────────────────────────────────────

// picture picks the image of a <picture> and drops its <source>s.
func (p *page) picture(pic *html.Node) {
	var img *html.Node
	var sources []*html.Node
	for ch := pic.FirstChild; ch != nil; ch = ch.NextSibling {
		switch ch.DataAtom {
		case atom.Img:
			img = ch
		case atom.Source:
			sources = append(sources, ch)
		}
	}
	if img != nil {
		var fallback string
		for _, s := range sources {
			if v := attr(s, "srcset"); v != "" {
				fallback = v
				break
			}
		}
		p.img(img, fallback)
	}
	for _, s := range sources {
		pic.RemoveChild(s)
	}
}

// img resolves lazy attributes and srcset into a single src. sourceSet is the
// first <source> srcset of an enclosing <picture>, used only when the img
// has none of its own.
func (p *page) img(n *html.Node, sourceSet string) {
	if p.seen[n] {
		return
	}
	p.seen[n] = true
	src := strings.TrimSpace(attr(n, "src"))
	srcset := attr(n, "srcset")
	if src == "" || strings.HasPrefix(strings.ToLower(src), "data:") {
		for _, k := range lazyAttrs {
			if v := strings.TrimSpace(attr(n, k)); v != "" {
				src = v
				break
			}
		}
	}
	if srcset == "" {
		srcset = attr(n, "data-srcset")
	}
	if srcset == "" && src == "" {
		srcset = sourceSet
	}
	if pick := pickSrcset(srcset); pick != "" {
		src = pick
	}
	for _, k := range []string{"srcset", "sizes", "data-srcset", "data-src", "data-lazy-src", "data-original", "loading"} {
		removeAttr(n, k)
	}
	if strings.HasPrefix(strings.ToLower(src), "data:image/") {
		setAttr(n, "src", src)
		return
	}
	if u, ok := resolveAsset(p.base, src); ok {
		p.imgs = append(p.imgs, imgNode{node: n, url: u})
		return
	}
	removeAttr(n, "src")
}

// ── Assets ───────────────────────────────────────────────────────

// loadSheets fetches linked stylesheets, then the @imports of every sheet,
// one level deep.
func (p *page) loadSheets(ctx context.Context) {
	var reqs []assetReq
	for _, s := range p.sheets {
		if s.link {
			reqs = append(reqs, assetReq{attr(s.node, "href"), kindSheet})
		}
	}
	p.assets.fetchAll(ctx, reqs)
	for _, s := range p.sheets {
		if s.link {
			if e := p.assets.get(attr(s.node, "href")); e != nil {
				base, _ := url.Parse(attr(s.node, "href"))
				s.sheet = newSheet(base, decodeCSS(e))
			}
		}
	}
	reqs = reqs[:0]
	for _, s := range p.sheets {
		for _, r := range s.refs {
			if u, ok := resolveAsset(s.base, r.URL); r.Import && ok {
				reqs = append(reqs, assetReq{u, kindSheet})
			}
		}
	}
	p.assets.fetchAll(ctx, reqs)
	for _, s := range p.sheets {
		s.loadImports(p.assets, false)
	}
}

// loadImports links each @import to its fetched sheet. Imports inside an
// imported sheet are not followed.
func (s *sheet) loadImports(a *assets, nested bool) {
	for _, r := range s.refs {
		if !r.Import {
			continue
		}
		var child *sheet
		switch u, ok := resolveAsset(s.base, r.URL); {
		case nested || !ok:
			a.addMissing()
		default:
			if e := a.get(u); e != nil {
				pu, _ := url.Parse(u)
				child = newSheet(pu, decodeCSS(e))
				child.loadImports(a, true)
			}
		}
		s.imports = append(s.imports, child)
	}
}

// fetchAssets fetches images in document order, then CSS images, then fonts.
func (p *page) fetchAssets(ctx context.Context) {
	var reqs []assetReq
	for _, im := range p.imgs {
		reqs = append(reqs, assetReq{im.url, kindImage})
	}
	var fonts []assetReq
	var walk func(s *sheet)
	walk = func(s *sheet) {
		imp := 0
		for _, r := range s.refs {
			if r.Import {
				if c := s.imports[imp]; c != nil {
					walk(c)
				}
				imp++
				continue
			}
			if u, ok := resolveAsset(s.base, r.URL); ok {
				if r.Font {
					fonts = append(fonts, assetReq{u, kindFont})
				} else {
					reqs = append(reqs, assetReq{u, kindImage})
				}
			}
		}
	}
	for _, s := range p.sheets {
		walk(s.sheet)
	}
	p.assets.fetchAll(ctx, append(reqs, fonts...))
}

// render writes the inlined sheets and images back into the tree.
func (p *page) render() {
	for _, im := range p.imgs {
		if d := p.assets.dataURI(im.url); d != "" {
			setAttr(im.node, "src", d)
		} else {
			removeAttr(im.node, "src")
		}
	}
	for _, s := range p.sheets {
		css := p.renderSheet(s.sheet)
		switch {
		case s.attr >= 0:
			s.node.Attr[s.attr].Val = css
		case s.link:
			s.node.Data, s.node.DataAtom = "style", atom.Style
			media := attr(s.node, "media")
			s.node.Attr = nil
			if media != "" {
				s.node.Attr = []html.Attribute{{Key: "media", Val: media}}
			}
			s.node.AppendChild(&html.Node{Type: html.TextNode, Data: css})
		default:
			for ch := s.node.FirstChild; ch != nil; ch = s.node.FirstChild {
				s.node.RemoveChild(ch)
			}
			s.node.AppendChild(&html.Node{Type: html.TextNode, Data: css})
		}
	}
}

func (p *page) renderSheet(s *sheet) string {
	if s == nil {
		return ""
	}
	imp := 0
	css := rewriteCSS(s.text, s.refs, func(r cssRef) string {
		if r.Import {
			child := s.imports[imp]
			imp++
			if child == nil {
				return ""
			}
			inner := p.renderSheet(child)
			if r.Media != "" {
				return "@media " + r.Media + "{" + inner + "}"
			}
			return inner
		}
		if u, ok := resolveAsset(s.base, r.URL); ok {
			if d := p.assets.dataURI(u); d != "" {
				return `url("` + d + `")`
			}
			return `url("data:,")`
		}
		if r.URL == "" || r.URL[0] == '#' || strings.HasPrefix(strings.ToLower(r.URL), "data:") {
			return `url("` + strings.NewReplacer(`"`, `%22`, "\n", "", `\`, `%5C`).Replace(r.URL) + `")`
		}
		return `url("data:,")`
	})
	// A stylesheet must not be able to close its <style> element.
	return strings.ReplaceAll(css, "</", `<\/`)
}

// ── Helpers ──────────────────────────────────────────────────────

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func removeAttr(n *html.Node, key string) {
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Key != key {
			kept = append(kept, a)
		}
	}
	n.Attr = kept
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// forEach visits root and its descendants in document order, without
// recursion, until f returns false.
func forEach(root *html.Node, f func(*html.Node) bool) {
	n := root
	for n != nil {
		if !f(n) {
			return
		}
		if n.FirstChild != nil {
			n = n.FirstChild
			continue
		}
		for n != root && n.NextSibling == nil {
			n = n.Parent
		}
		if n == root {
			return
		}
		n = n.NextSibling
	}
}
