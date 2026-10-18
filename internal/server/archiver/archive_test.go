package archiver

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// 1x1 transparent GIF.
var gif = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")

var gifURI = "data:image/gif;base64," + base64.StdEncoding.EncodeToString(gif)

// longText keeps a fixture above the little-text threshold.
var longText = strings.Repeat("Lorem ipsum dolor sit amet. ", 40)

type route struct {
	ctype string
	body  []byte
	code  int
}

// site serves routes by path and records the requests it saw.
func site(t *testing.T, routes map[string]route) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		rt, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if rt.ctype != "" {
			w.Header().Set("Content-Type", rt.ctype)
		}
		if rt.code != 0 {
			w.WriteHeader(rt.code)
		}
		_, _ = w.Write(rt.body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func htmlRoute(body string) route {
	return route{ctype: "text/html; charset=utf-8", body: []byte(body)}
}

func archive(t *testing.T, srv *httptest.Server, path string, lim Limits) (*Result, error) {
	t.Helper()
	return Archive(context.Background(), srv.Client(), srv.URL+path, lim)
}

func mustArchive(t *testing.T, srv *httptest.Server, path string, lim Limits) string {
	t.Helper()
	res, err := archive(t, srv, path, lim)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	return string(res.Body)
}

func TestArchive_RelativeAndAbsoluteURLs(t *testing.T) {
	srv, _ := site(t, map[string]route{
		"/dir/a.html": htmlRoute(`<html><body><p>` + longText + `</p>
<a href="next.html">rel</a> <a href="/root.html">root</a> <a href="https://other.example/x">abs</a> <a href="#top">frag</a>
<img src="img/a.gif"> <img id="b" src="/img/b.gif">
</body></html>`),
		"/dir/img/a.gif": {ctype: "image/gif", body: gif},
		"/img/b.gif":     {ctype: "image/gif", body: gif},
	})
	res, err := archive(t, srv, "/dir/a.html", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Body)
	for _, want := range []string{
		`href="` + srv.URL + `/dir/next.html"`,
		`href="` + srv.URL + `/root.html"`,
		`href="https://other.example/x"`,
		`href="#top"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in output", want)
		}
	}
	if n := strings.Count(out, `src="`+gifURI+`"`); n != 2 {
		t.Errorf("inlined images = %d, want 2", n)
	}
	if res.MissingAssets != 0 || res.Note != "" {
		t.Errorf("missing=%d note=%q", res.MissingAssets, res.Note)
	}
	if res.ContentType != "text/html; charset=utf-8" || res.HTTPStatus != 200 || res.FinalURL != srv.URL+"/dir/a.html" {
		t.Errorf("result = %+v", res)
	}
	if !strings.Contains(out, "Archived from "+srv.URL+"/dir/a.html at ") {
		t.Error("missing provenance comment")
	}
}

func TestArchive_FinalURLAfterRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/new/page.html", http.StatusFound) })
	mux.HandleFunc("/new/page.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, `<p>%s</p><img src="a.gif"><a href="x.html">x</a>`, longText)
	})
	mux.HandleFunc("/new/a.gif", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write(gif)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	res, err := Archive(context.Background(), srv.Client(), srv.URL+"/old", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalURL != srv.URL+"/new/page.html" {
		t.Errorf("FinalURL = %s", res.FinalURL)
	}
	if out := string(res.Body); !strings.Contains(out, gifURI) || !strings.Contains(out, srv.URL+"/new/x.html") {
		t.Errorf("relative URLs were not resolved against the final URL:\n%s", out)
	}
}

func TestPickSrcset(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"widths under cap", "a.jpg 400w, b.jpg 800w, c.jpg 1200w", "c.jpg"},
		{"largest at or under 1600", "a.jpg 800w, b.jpg 1600w, c.jpg 3200w", "b.jpg"},
		{"all over cap picks smallest", "a.jpg 2400w, b.jpg 1800w, c.jpg 3200w", "b.jpg"},
		{"densities", "a.jpg 1x, b.jpg 2x, c.jpg 3x", "b.jpg"},
		{"all densities over 2x", "a.jpg 3x, b.jpg 4x", "a.jpg"},
		{"no descriptor is 1x", "a.jpg, b.jpg 2x", "b.jpg"},
		{"single", "only.jpg", "only.jpg"},
		{"commas in url", "/i/w_400,h_300/a.jpg 400w, /i/w_800,h_600/a.jpg 800w", "/i/w_800,h_600/a.jpg"},
		{"newlines", "a.jpg\n 100w,\n b.jpg\n 200w", "b.jpg"},
		{"trailing comma on url", "a.jpg, b.jpg 2x", "b.jpg"},
		{"comma without space belongs to the url", "a.jpg,b.jpg 2x", "a.jpg,b.jpg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickSrcset(tt.in); got != tt.want {
				t.Errorf("pickSrcset(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestArchive_SrcsetAndPicture(t *testing.T) {
	small, big := []byte("GIF89a-small-image-bytes"), []byte("GIF89a-big-image-bytes")
	srv, seen := site(t, map[string]route{
		"/p.html": htmlRoute(`<p>` + longText + `</p>
<img id="one" src="fallback.gif" srcset="s.gif 400w, b.gif 1200w, huge.gif 4000w" sizes="100vw">
<picture><source srcset="webp.gif" type="image/webp"><source srcset="other.gif"><img id="two" src="fallback.gif"></picture>
<picture><source srcset="s.gif 1x, b.gif 2x"><img id="three"></picture>`),
		"/s.gif":        {ctype: "image/gif", body: small},
		"/b.gif":        {ctype: "image/gif", body: big},
		"/fallback.gif": {ctype: "image/gif", body: gif},
	})
	out := mustArchive(t, srv, "/p.html", Limits{})
	uri := func(b []byte) string { return "data:image/gif;base64," + base64.StdEncoding.EncodeToString(b) }
	doc, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	forEach(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "img" {
			got[attr(n, "id")] = attr(n, "src")
			for _, k := range []string{"srcset", "sizes"} {
				if hasAttr(n, k) {
					t.Errorf("img %s still has %s", attr(n, "id"), k)
				}
			}
		}
		if n.Type == html.ElementNode && n.Data == "source" {
			t.Error("<source> survived")
		}
		return true
	})
	want := map[string]string{"one": uri(big), "two": uri(gif), "three": uri(big)}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("img %s src = %.40q, want %.40q", id, got[id], w)
		}
	}
	for _, p := range seen() {
		if p == "/huge.gif" || p == "/webp.gif" || p == "/other.gif" {
			t.Errorf("fetched unused candidate %s", p)
		}
	}
}

func TestArchive_LazyImagesAndNoscript(t *testing.T) {
	srv, _ := site(t, map[string]route{
		"/l.html": htmlRoute(`<p>` + longText + `</p>
<img id="a" src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" data-src="real-a.gif">
<img id="b" data-lazy-src="real-b.gif">
<img id="c" data-original="real-c.gif">
<img id="d" src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" data-srcset="real-a.gif 1x, real-d.gif 2x">
<img id="e" src="keep.gif" data-src="ignored.gif">
<noscript><img id="f" src="noscript.gif"></noscript>`),
		"/real-a.gif":   {ctype: "image/gif", body: []byte("GIF89a-a")},
		"/real-b.gif":   {ctype: "image/gif", body: []byte("GIF89a-b")},
		"/real-c.gif":   {ctype: "image/gif", body: []byte("GIF89a-c")},
		"/real-d.gif":   {ctype: "image/gif", body: []byte("GIF89a-d")},
		"/keep.gif":     {ctype: "image/gif", body: []byte("GIF89a-keep")},
		"/noscript.gif": {ctype: "image/gif", body: []byte("GIF89a-noscript")},
	})
	out := mustArchive(t, srv, "/l.html", Limits{})
	for id, body := range map[string]string{"a": "a", "b": "b", "c": "c", "d": "d", "e": "keep", "f": "noscript"} {
		re := regexp.MustCompile(`<img id="` + id + `"[^>]*src="([^"]*)"`)
		m := re.FindStringSubmatch(out)
		if m == nil {
			t.Errorf("img %s has no src", id)
			continue
		}
		want := "data:image/gif;base64," + base64.StdEncoding.EncodeToString([]byte("GIF89a-"+body))
		if m[1] != want {
			t.Errorf("img %s src = %.50q, want %.50q", id, m[1], want)
		}
	}
	if strings.Contains(out, "<noscript") {
		t.Error("<noscript> was not unwrapped")
	}
}

func TestArchive_Stylesheets(t *testing.T) {
	srv, seen := site(t, map[string]route{
		"/s.html": htmlRoute(`<html><head>
<link rel="stylesheet" href="css/main.css" media="screen">
<link rel="stylesheet" href="css/missing.css">
<link rel="alternate stylesheet" href="css/alt.css">
<link rel="icon" href="favicon.ico">
<link rel="preload" href="x.js" as="script">
<style>@import "css/extra.css"; p { background: url(img/inline.gif) }</style>
</head><body><p style="background:url('img/attr.gif')">` + longText + `</p></body></html>`),
		"/css/main.css": {ctype: "text/css", body: []byte(`
/* url(img/comment.gif) */
@import url("sub/imp.css") print;
@font-face { font-family: f; src: url(../fonts/f.woff2) format("woff2"), url('../fonts/f.ttf'); }
body { background: url( "../img/bg.gif" ); content: "url(img/string.gif)"; }
.x { background: url(data:image/gif;base64,AAAA); mask: url(#m); }
.y { background: url(../img/gone.gif); }
`)},
		"/css/extra.css":      {ctype: "text/css", body: []byte(`.extra { color: red; background: url(e.gif) }`)},
		"/css/sub/imp.css":    {ctype: "text/css", body: []byte(`.imp { background: url(i.gif) } @import "deeper.css";`)},
		"/css/sub/i.gif":      {ctype: "image/gif", body: []byte("GIF89a-i")},
		"/css/e.gif":          {ctype: "image/gif", body: []byte("GIF89a-e")},
		"/img/bg.gif":         {ctype: "image/gif", body: []byte("GIF89a-bg")},
		"/img/inline.gif":     {ctype: "image/gif", body: []byte("GIF89a-inline")},
		"/img/attr.gif":       {ctype: "image/gif", body: []byte("GIF89a-attr")},
		"/fonts/f.woff2":      {ctype: "font/woff2", body: []byte("wOF2-font")},
		"/fonts/f.ttf":        {ctype: "application/octet-stream", body: []byte("\x00\x01\x00\x00fontdata")},
		"/css/sub/deeper.css": {ctype: "text/css", body: []byte(`.deeper{}`)},
	})
	res, err := archive(t, srv, "/s.html", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Body)
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	for name, want := range map[string]string{
		"imported sheet in a media block": `@media print{.imp { background: url("data:image/gif;base64,` + b64("GIF89a-i"),
		"font":                            `url("data:font/woff2;base64,` + b64("wOF2-font"),
		"sniffed font":                    `data:font/ttf;base64,` + b64("\x00\x01\x00\x00fontdata"),
		"body background":                 `background: url("data:image/gif;base64,` + b64("GIF89a-bg"),
		"inline @import":                  `.extra { color: red; background: url("data:image/gif;base64,` + b64("GIF89a-e"),
		"style element url":               `p { background: url("data:image/gif;base64,` + b64("GIF89a-inline"),
		"style attribute":                 `style="background:url(&#34;data:image/gif;base64,` + b64("GIF89a-attr"),
		"fragment url kept":               `mask: url("#m")`,
		"data url kept":                   `url("data:image/gif;base64,AAAA")`,
		"string untouched":                `content: "url(img/string.gif)"`,
		"media on link":                   `<style media="screen">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%s: %q not found", name, want)
		}
	}
	if strings.Contains(out, "<link") {
		t.Error("<link> elements remain")
	}
	if strings.Contains(out, "alt.css") || strings.Contains(out, "comment.gif") && !strings.Contains(out, "/* url(img/comment.gif) */") {
		t.Error("alternate sheet or comment mishandled")
	}
	// missing.css (404), gone.gif (404) and the nested @import deeper.css.
	if res.MissingAssets != 3 {
		t.Errorf("MissingAssets = %d, want 3", res.MissingAssets)
	}
	for _, p := range seen() {
		switch p {
		case "/css/alt.css", "/favicon.ico", "/x.js", "/img/comment.gif", "/img/string.gif", "/css/sub/deeper.css":
			t.Errorf("fetched %s", p)
		}
	}
	if strings.Contains(out, "url(../") || strings.Contains(out, "url(sub/") || strings.Contains(out, "url(e.gif") {
		t.Errorf("a relative url() remains:\n%s", out)
	}
}

func TestArchive_AssetOrder(t *testing.T) {
	// Stylesheets first, then images in document order, then fonts.
	var order []string
	mux := http.NewServeMux()
	mux.HandleFunc("/p.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, `<link rel=stylesheet href="/a.css"><p>%s</p><img src="/1.gif"><img src="/2.gif">`, longText)
	})
	mux.HandleFunc("/a.css", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, r.URL.Path)
		w.Header().Set("Content-Type", "text/css")
		_, _ = fmt.Fprint(w, `@font-face{src:url(/f.woff2)} body{background:url(/bg.gif)}`)
	})
	for _, p := range []string{"/1.gif", "/2.gif", "/bg.gif", "/f.woff2"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			order = append(order, r.URL.Path)
			if strings.HasSuffix(r.URL.Path, "gif") {
				w.Header().Set("Content-Type", "image/gif")
			} else {
				w.Header().Set("Content-Type", "font/woff2")
			}
			_, _ = w.Write([]byte("GIF89a"))
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	lim := DefaultLimits()
	lim.Parallel = 1
	if _, err := Archive(context.Background(), srv.Client(), srv.URL+"/p.html", lim); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, " "); got != "/a.css /1.gif /2.gif /bg.gif /f.woff2" {
		t.Errorf("fetch order = %s", got)
	}
}

const hostile = `<!DOCTYPE html><html><head>
<meta charset="windows-1252">
<meta http-equiv="refresh" content="0;url=https://evil.example/">
<meta http-equiv="Content-Security-Policy" content="default-src *">
<base href="/b/">
<script>alert(1)</script>
<script src="/x.js"></script>
</head><body onload="steal()" onclick="x()">
<p>` + "%s" + `</p>
<a href="javascript:alert(1)" onclick="x()">js</a>
<a href="  JaVa&#x09;Script:alert(2)">js2</a>
<a href="vbscript:x">vb</a>
<a href="data:text/html,<script>alert(3)</script>">data</a>
<a href="/ok.html" ONMOUSEOVER="x()">ok</a>
<iframe src="https://video.example/embed/1"></iframe>
<iframe srcdoc="<script>1</script>"></iframe>
<frameset><frame src="/f.html"></frameset>
<object data="/o.swf"></object><embed src="/e.swf">
<video src="/v.mp4" controls poster="/p.png"></video>
<audio controls><source src="/a.mp3"></audio>
<form action="javascript:evil()"><input type="image" src="/btn.png" onfocus="x()"></form>
<svg onload="x()"><script>alert(4)</script><a href="javascript:x()"><text>t</text></a></svg>
<!-- a comment -->
<img src="/x.gif" onerror="x()">
</body></html>`

func TestArchive_InertCopy(t *testing.T) {
	srv, seen := site(t, map[string]route{
		"/h.html": htmlRoute(fmt.Sprintf(hostile, longText)),
		"/x.gif":  {ctype: "image/gif", body: gif},
	})
	out := mustArchive(t, srv, "/h.html", Limits{})
	doc, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	forEach(doc, func(n *html.Node) bool {
		if n.Type == html.CommentNode && strings.Contains(n.Data, "a comment") {
			t.Error("source comment kept")
		}
		if n.Type != html.ElementNode {
			return true
		}
		switch n.Data {
		case "script", "iframe", "frame", "frameset", "object", "embed", "base", "video", "audio", "noscript":
			t.Errorf("<%s> survived", n.Data)
		case "meta":
			if he := attr(n, "http-equiv"); he != "" && !strings.EqualFold(he, "Content-Security-Policy") {
				t.Errorf("meta http-equiv=%s survived", he)
			}
		}
		for _, a := range n.Attr {
			k := strings.ToLower(a.Key)
			if strings.HasPrefix(k, "on") || k == "srcdoc" {
				t.Errorf("<%s %s> survived", n.Data, a.Key)
			}
			v := strings.ToLower(strings.Join(strings.Fields(a.Val), ""))
			if strings.HasPrefix(v, "javascript:") || strings.HasPrefix(v, "vbscript:") || strings.HasPrefix(v, "data:text/html") {
				t.Errorf("<%s %s=%q> survived", n.Data, a.Key, a.Val)
			}
		}
		return true
	})
	// Exactly one CSP meta (ours), first in <head>, after the charset.
	if n := strings.Count(out, "Content-Security-Policy"); n != 1 {
		t.Errorf("CSP metas = %d, want 1", n)
	}
	var head *html.Node
	forEach(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "head" {
			head = n
			return false
		}
		return true
	})
	first, second := head.FirstChild, head.FirstChild.NextSibling
	if attr(first, "charset") != "utf-8" || attr(second, "http-equiv") != "Content-Security-Policy" || attr(second, "content") != csp {
		t.Errorf("head does not start with the charset and CSP metas:\n%.400s", out)
	}
	if strings.Contains(out, "windows-1252") || strings.Contains(out, "evil.example") {
		t.Error("source charset or base survived")
	}
	// Embeds are replaced by links, never fetched.
	for _, want := range []string{
		`<a href="https://video.example/embed/1">https://video.example/embed/1</a>`,
		`<a href="` + srv.URL + `/v.mp4">`,
		`<a href="` + srv.URL + `/a.mp3">`,
		`<a href="` + srv.URL + `/ok.html">ok</a>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(out, "<script") || strings.Contains(out, "<SCRIPT") {
		t.Error("output contains <script")
	}
	for _, p := range seen() {
		if p != "/h.html" && p != "/x.gif" {
			t.Errorf("fetched %s", p)
		}
	}
}

func TestArchive_StyleCannotCloseElement(t *testing.T) {
	srv, _ := site(t, map[string]route{
		"/e.html": htmlRoute(`<link rel=stylesheet href="/e.css"><p>` + longText + `</p>`),
		"/e.css":  {ctype: "text/css", body: []byte(`/* </style><img src=x onerror=alert(1)> */ p{color:red}`)},
	})
	out := mustArchive(t, srv, "/e.html", Limits{})
	if strings.Contains(out, "</style><img") {
		t.Errorf("stylesheet closed its element:\n%s", out)
	}
	if n := strings.Count(out, "</style>"); n != 1 {
		t.Errorf("</style> count = %d", n)
	}
}

func TestArchive_BaseHref(t *testing.T) {
	srv, _ := site(t, map[string]route{
		"/p.html":       htmlRoute(`<base href="/assets/"><p>` + longText + `</p><img src="a.gif"><a href="x.html">x</a>`),
		"/assets/a.gif": {ctype: "image/gif", body: gif},
	})
	out := mustArchive(t, srv, "/p.html", Limits{})
	if !strings.Contains(out, gifURI) || !strings.Contains(out, `href="`+srv.URL+`/assets/x.html"`) {
		t.Errorf("<base href> was not honoured:\n%s", out)
	}
}

func TestArchive_Charset(t *testing.T) {
	text := strings.Repeat("Caf\xe9 cr\xe8me br\xfbl\xe9e. ", 40)
	srv, _ := site(t, map[string]route{
		"/header.html": {ctype: "text/html; charset=ISO-8859-1", body: []byte("<html><body><p>" + text + "</p></body></html>")},
		"/meta.html":   {ctype: "text/html", body: []byte(`<html><head><meta http-equiv="Content-Type" content="text/html; charset=iso-8859-1"></head><body><p>` + text + `</p></body></html>`)},
	})
	for _, p := range []string{"/header.html", "/meta.html"} {
		res, err := archive(t, srv, p, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(res.Body, []byte("Café crème brûlée.")) {
			t.Errorf("%s: not converted to UTF-8: %.200s", p, res.Body)
		}
		if res.TextLength < 500 {
			t.Errorf("%s: TextLength = %d", p, res.TextLength)
		}
	}
}

func TestArchive_Limits(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 3000)
	routes := map[string]route{
		"/p.html":  htmlRoute(`<p>` + longText + `</p><img src="/ok.gif"><img src="/big.gif"><img src="/ok2.gif"><img src="/ok3.gif">`),
		"/ok.gif":  {ctype: "image/gif", body: []byte("GIF89a-1")},
		"/ok2.gif": {ctype: "image/gif", body: []byte("GIF89a-2")},
		"/ok3.gif": {ctype: "image/gif", body: []byte("GIF89a-3")},
		"/big.gif": {ctype: "image/gif", body: big},
	}
	t.Run("oversized asset", func(t *testing.T) {
		srv, _ := site(t, routes)
		lim := DefaultLimits()
		lim.MaxAssetBytes = 1000
		res, err := archive(t, srv, "/p.html", lim)
		if err != nil {
			t.Fatal(err)
		}
		if res.MissingAssets != 1 {
			t.Errorf("MissingAssets = %d, want 1", res.MissingAssets)
		}
		if strings.Contains(string(res.Body), "eHh4") { // base64 of "xxx"
			t.Error("oversized asset was inlined")
		}
	})
	t.Run("total budget", func(t *testing.T) {
		srv, _ := site(t, routes)
		lim := DefaultLimits()
		lim.Parallel = 1
		// The page, then room for exactly two small images (as base64).
		lim.MaxTotalBytes = int64(len(routes["/p.html"].body)) + 2*int64(base64.StdEncoding.EncodedLen(8)) + 10
		res, err := archive(t, srv, "/p.html", lim)
		if err != nil {
			t.Fatal(err)
		}
		// ok and ok2 fit in order; big and the rest do not.
		if res.MissingAssets != 2 {
			t.Errorf("MissingAssets = %d, want 2", res.MissingAssets)
		}
		out := string(res.Body)
		if !strings.Contains(out, base64.StdEncoding.EncodeToString([]byte("GIF89a-1"))) {
			t.Error("first image missing")
		}
	})
	t.Run("asset count", func(t *testing.T) {
		srv, _ := site(t, routes)
		lim := DefaultLimits()
		lim.Parallel = 1
		lim.MaxAssets = 2
		res, err := archive(t, srv, "/p.html", lim)
		if err != nil {
			t.Fatal(err)
		}
		if res.MissingAssets != 2 {
			t.Errorf("MissingAssets = %d, want 2", res.MissingAssets)
		}
	})
	t.Run("page too large", func(t *testing.T) {
		srv, _ := site(t, routes)
		lim := DefaultLimits()
		lim.MaxPageBytes = 100
		_, err := archive(t, srv, "/p.html", lim)
		if err == nil || err.Error() != "page larger than 100 bytes" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("page too large message", func(t *testing.T) {
		if got := (tooLargeError{"page", 10 << 20}).Error(); got != "page larger than 10 MiB" {
			t.Errorf("got %q", got)
		}
	})
}

func TestArchive_AssetContentTypes(t *testing.T) {
	srv, _ := site(t, map[string]route{
		"/p.html": htmlRoute(`<link rel=stylesheet href="/html.css"><link rel=stylesheet href="/plain.css"><p>` + longText + `</p>
<img src="/page.png"><img src="/sniffed"><img src="/text.png">`),
		"/html.css":  {ctype: "text/html", body: []byte(`<html><body>Not found</body></html>`)},
		"/plain.css": {ctype: "text/plain", body: []byte(`p{color:blue}`)},
		"/page.png":  {ctype: "text/html", body: []byte(`<!DOCTYPE html><html>error</html>`)},
		"/sniffed":   {ctype: "application/octet-stream", body: gif},
		"/text.png":  {ctype: "image/png", body: []byte("not really a png")},
	})
	res, err := archive(t, srv, "/p.html", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Body)
	// html.css and page.png are rejected; /sniffed is a GIF by content.
	if res.MissingAssets != 2 {
		t.Errorf("MissingAssets = %d, want 2", res.MissingAssets)
	}
	if !strings.Contains(out, "p{color:blue}") {
		t.Error("text/plain stylesheet that sniffs as text was dropped")
	}
	if strings.Contains(out, "Not found") {
		t.Error("an HTML error page was inlined as a stylesheet")
	}
	if !strings.Contains(out, gifURI) {
		t.Error("image detected by content was dropped")
	}
}

func TestArchive_PDF(t *testing.T) {
	pdf := []byte("%PDF-1.7\n1 0 obj<<>>endobj\n%%EOF")
	srv, _ := site(t, map[string]route{
		"/a.pdf":   {ctype: "application/pdf", body: pdf},
		"/noctype": {ctype: "application/octet-stream", body: pdf},
	})
	for _, p := range []string{"/a.pdf", "/noctype"} {
		res, err := archive(t, srv, p, Limits{})
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if res.ContentType != "application/pdf" || !bytes.Equal(res.Body, pdf) || res.Note != "" {
			t.Errorf("%s: result = %+v", p, res)
		}
	}
}

func TestArchive_Errors(t *testing.T) {
	srv, _ := site(t, map[string]route{
		"/forbidden": {ctype: "text/html", code: 403, body: []byte("<p>no</p>")},
		"/text":      {ctype: "text/plain", body: []byte("hello")},
		"/image":     {ctype: "image/png", body: gif},
		"/json":      {ctype: "application/json", body: []byte("{}")},
		"/err500":    {code: 500},
	})
	tests := []struct{ path, want string }{
		{"/missing", "HTTP 404"},
		{"/forbidden", "HTTP 403"},
		{"/err500", "HTTP 500"},
		{"/text", "content type text/plain is not archived"},
		{"/image", "content type image/png is not archived"},
		{"/json", "content type application/json is not archived"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			res, err := archive(t, srv, tt.path, Limits{})
			if err == nil || err.Error() != tt.want {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
			if res != nil {
				t.Errorf("result = %+v, want nil", res)
			}
		})
	}
	for _, bad := range []string{"ftp://example.com/x", "/relative", "", "http://"} {
		if _, err := Archive(context.Background(), srv.Client(), bad, Limits{}); err == nil || err.Error() != "not an http(s) link" {
			t.Errorf("Archive(%q) err = %v", bad, err)
		}
	}
}

func TestArchive_ClientError(t *testing.T) {
	srv, _ := site(t, nil)
	url := srv.URL
	srv.Close()
	_, err := Archive(context.Background(), http.DefaultClient, url, Limits{})
	if err == nil || strings.Contains(err.Error(), "\n") || strings.Contains(err.Error(), url) {
		t.Errorf("err = %v; want a short reason without the URL", err)
	}
}

func TestArchive_LittleTextNote(t *testing.T) {
	srv, _ := site(t, map[string]route{
		"/short.html": htmlRoute(`<html><head><title>x</title></head><body><p>Please accept cookies.</p><script>var a = "` + longText + `"</script><noscript>` + longText + `</noscript></body></html>`),
		"/long.html":  htmlRoute(`<p>` + longText + `</p>`),
	})
	res, err := archive(t, srv, "/short.html", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Note != "little text; may be a consent wall or paywall" || res.TextLength != len("Please accept cookies.") {
		t.Errorf("note=%q length=%d", res.Note, res.TextLength)
	}
	res, err = archive(t, srv, "/long.html", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Note != "" || res.TextLength < 500 {
		t.Errorf("note=%q length=%d", res.Note, res.TextLength)
	}
}

func TestArchive_Deadline(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	lim := DefaultLimits()
	lim.Deadline = 100 * time.Millisecond
	start := time.Now()
	_, err := Archive(context.Background(), srv.Client(), srv.URL+"/slow", lim)
	if err == nil || err.Error() != "timed out" {
		t.Errorf("err = %v, want timed out", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("deadline was not enforced")
	}
}

func TestArchive_DeadlineDuringAssets(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/p.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, `<p>%s</p><img src="/slow.gif">`, longText)
	})
	mux.HandleFunc("/slow.gif", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	lim := DefaultLimits()
	lim.Deadline = 200 * time.Millisecond
	res, err := Archive(context.Background(), srv.Client(), srv.URL+"/p.html", lim)
	if err != nil {
		t.Fatal(err)
	}
	if res.MissingAssets != 1 || strings.Contains(string(res.Body), "slow.gif") {
		t.Errorf("missing=%d, body mentions slow.gif=%v", res.MissingAssets, strings.Contains(string(res.Body), "slow.gif"))
	}
}

func TestArchive_ParentCanceled(t *testing.T) {
	srv, _ := site(t, map[string]route{"/p.html": htmlRoute(`<p>` + longText + `</p>`)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Archive(ctx, srv.Client(), srv.URL+"/p.html", Limits{}); err == nil || err.Error() != "canceled" {
		t.Errorf("err = %v, want canceled", err)
	}
}

func TestArchive_DeepNesting(t *testing.T) {
	body := strings.Repeat("<div>", 600)
	srv, _ := site(t, map[string]route{"/deep.html": htmlRoute(body)})
	if _, err := archive(t, srv, "/deep.html", Limits{}); err == nil || !strings.Contains(err.Error(), "nested too deeply") {
		t.Errorf("err = %v", err)
	}
}

func TestScanCSS(t *testing.T) {
	tests := []struct {
		name string
		css  string
		want []string // "<kind>:<url>[:font][:media]"
	}{
		{"plain", `a{background:url(a.png)}`, []string{"url:a.png"}},
		{"quoted", `a{background:URL( "a b.png" )}`, []string{"url:a b.png"}},
		{"single quoted", `a{background:url('a.png')}`, []string{"url:a.png"}},
		{"comment", `/* url(no.png) */ a{background:url(yes.png)}`, []string{"url:yes.png"}},
		{"string", `a{content:"url(no.png)"; background:url(yes.png)}`, []string{"url:yes.png"}},
		{"escaped quote in string", `a{content:"\"url(no.png)"; background:url(yes.png)}`, []string{"url:yes.png"}},
		{"escape in url", `a{background:url(a\29 .png)}`, []string{"url:a).png"}},
		{"not a url function", `a{background:myurl(no.png)}`, nil},
		{"import string", `@import "a.css";`, []string{"import:a.css"}},
		{"import url with media", `@import url(a.css) screen and (min-width: 1px);`, []string{"import:a.css:screen and (min-width: 1px)"}},
		{"import then rule", `@import 'a.css';a{color:red}`, []string{"import:a.css"}},
		{"font face", `@font-face{src:url(f.woff)} a{background:url(b.png)}`, []string{"url:f.woff:font", "url:b.png"}},
		{"nested font face", `@media print{@font-face{src:url(f.woff)}} a{background:url(b.png)}`, []string{"url:f.woff:font", "url:b.png"}},
		{"unterminated comment", `a{} /* url(x.png)`, nil},
		{"unterminated url", `a{background:url(x.png`, nil},
		{"unterminated string", "a{content:\"abc\n} b{background:url(y.png)}", []string{"url:y.png"}},
		{"bad unquoted url", `a{background:url(a"b)}`, nil},
		{"empty", ``, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, r := range scanCSS(tt.css) {
				s := "url:" + r.URL
				if r.Import {
					s = "import:" + r.URL
				}
				if r.Font {
					s += ":font"
				}
				if r.Media != "" {
					s += ":" + r.Media
				}
				got = append(got, s)
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("scanCSS(%q) = %q, want %q", tt.css, got, tt.want)
			}
		})
	}
}

func TestRewriteCSS_Spans(t *testing.T) {
	css := `@import "a.css"; a{b:url(x.png)} c{d:url( 'y.png' )}`
	got := rewriteCSS(css, scanCSS(css), func(r cssRef) string {
		if r.Import {
			return "/*imp*/"
		}
		return "url(" + strings.ToUpper(r.URL) + ")"
	})
	if want := `/*imp*/ a{b:url(X.PNG)} c{d:url(Y.PNG)}`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIsScriptURL(t *testing.T) {
	for in, want := range map[string]bool{
		"javascript:alert(1)":             true,
		" JavaScript:alert(1)":            true,
		"java\tscript:alert(1)":           true,
		"java\nscript:alert(1)":           true,
		"vbscript:x":                      true,
		"data:text/html,<b>":              true,
		"data:image/png;base64,AAAA":      false,
		"https://example.com/javascript:": false,
		"/javascript:foo":                 false,
		"":                                false,
	} {
		if got := isScriptURL(in); got != want {
			t.Errorf("isScriptURL(%q) = %v, want %v", in, got, want)
		}
	}
}
