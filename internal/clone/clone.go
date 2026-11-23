// Package clone sanitizes a fetched or browser-rendered page's HTML for
// storage/serving as a landing page: scripts, event handlers and remote
// assets stripped, forms neutralized or rewritten into this service's own
// capture form. See internal/browserclone for the JS-rendering fetch path.
package clone

import (
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rudra184/baitbet/internal/netguard"
)

const maxFetchBytes = 2 << 20 // 2 MiB, caps memory use on a malicious/oversized page
const maxRedirects = 3        // each hop re-validated by netguard

var httpClient = &http.Client{
	Timeout:       15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Fetch retrieves url (SSRF-guarded, see internal/netguard) and returns
// sanitized HTML.
func Fetch(url string) (string, error) {
	body, _, err := doFetch(url)
	if err != nil {
		return "", err
	}
	return Sanitize(string(body))
}

// doFetch is a var, not a direct call, so tests can point it at an
// httptest server (loopback, which netguard would otherwise reject).
var doFetch = fetchFollowingRedirects

// fetchFollowingRedirects does an SSRF-guarded GET, following up to
// maxRedirects hops and re-validating each Location header.
func fetchFollowingRedirects(startURL string) ([]byte, string, error) {
	current := startURL
	for i := 0; i <= maxRedirects; i++ {
		if err := netguard.ValidateURL(current); err != nil {
			return nil, "", fmt.Errorf("clone: %w", err)
		}
		req, err := http.NewRequest(http.MethodGet, current, nil)
		if err != nil {
			return nil, "", fmt.Errorf("clone: build request: %w", err)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; baitbet-clone/1.0)")

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("clone: fetch %q: %w", current, err)
		}
		switch resp.StatusCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
			http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			if loc == "" {
				return nil, "", fmt.Errorf("clone: redirect from %q had no Location header", current)
			}
			next, err := resolveRedirect(current, loc)
			if err != nil {
				return nil, "", fmt.Errorf("clone: %w", err)
			}
			current = next
			continue
		case http.StatusOK:
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
			resp.Body.Close()
			if err != nil {
				return nil, "", fmt.Errorf("clone: read body: %w", err)
			}
			return body, current, nil
		default:
			resp.Body.Close()
			return nil, "", fmt.Errorf("clone: fetch %q: unexpected status %d", current, resp.StatusCode)
		}
	}
	return nil, "", fmt.Errorf("clone: too many redirects fetching %q", startURL)
}

func resolveRedirect(base, location string) (string, error) {
	baseURL, err := neturl.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	next, err := baseURL.Parse(location)
	if err != nil {
		return "", fmt.Errorf("parse redirect location %q: %w", location, err)
	}
	return next.String(), nil
}

// Sanitize strips <script>, <form>, <iframe>, <object>/<embed>, event
// handlers, and remote src/href attributes. Pure tree rewriting; nothing
// is executed or fetched.
func Sanitize(rawHTML string) (string, error) {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return "", fmt.Errorf("clone: parse html: %w", err)
	}
	stripNode(doc)

	var buf strings.Builder
	if err := html.Render(&buf, doc); err != nil {
		return "", fmt.Errorf("clone: render html: %w", err)
	}
	return buf.String(), nil
}

var droppedTags = map[atom.Atom]bool{
	atom.Script: true,
	atom.Form:   true,
	atom.Iframe: true,
	atom.Object: true,
	atom.Embed:  true,
	atom.Applet: true,
}

func stripNode(n *html.Node) {
	child := n.FirstChild
	for child != nil {
		next := child.NextSibling
		if child.Type == html.ElementNode && droppedTags[child.DataAtom] {
			n.RemoveChild(child)
			child = next
			continue
		}
		if child.Type == html.ElementNode {
			stripAttrs(child)
		}
		stripNode(child)
		child = next
	}
}

func stripAttrs(n *html.Node) {
	var kept []html.Attribute
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		if strings.HasPrefix(key, "on") {
			continue
		}
		if (key == "src" || key == "href") && isRemoteHTTP(a.Val) {
			continue
		}
		kept = append(kept, a)
	}
	n.Attr = kept
}

func isRemoteHTTP(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	return strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "//")
}

// FormMarkerID is left in place of the first <form> when no password
// field was found to preserve instead; internal/render replaces it with
// the real capture form at serve time.
const FormMarkerID = "__baitbet_form__"

// Sentinel values tagFormForCapture writes onto a preserved sign-in form
// for internal/render.LandingFromClone to replace with the real submit
// action and field names - keeping the real form's own classes/markup is
// what makes the served inputs/button match the cloned site's styling.
const (
	UsernameFieldSentinel = "__baitbet_username__"
	PasswordFieldSentinel = "__baitbet_password__"
	SubmitActionSentinel  = "__baitbet_submit__"
)

// SanitizeWithFormMarker is Sanitize, except the first <form> is kept (via
// tagFormForCapture) if it has a password field, or replaced with
// FormMarkerID otherwise; later forms are demoted to <div> (OAuth buttons
// are often their own <form>, and demoting keeps their look without the
// ability to submit anywhere). Returns whether a form was found.
//
// Callers needing InlineStylesheetsWithRuntimeCSS (browserclone) must run
// it first, on the same *html.Node, via SanitizeNodeWithFormMarker -
// stripAttrs here strips every remote href, stylesheet links included.
func SanitizeWithFormMarker(rawHTML string) (string, bool, error) {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return "", false, fmt.Errorf("clone: parse html: %w", err)
	}
	found := SanitizeNodeWithFormMarker(doc)

	var buf strings.Builder
	if err := html.Render(&buf, doc); err != nil {
		return "", false, fmt.Errorf("clone: render html: %w", err)
	}
	return buf.String(), found, nil
}

// SanitizeNodeWithFormMarker is SanitizeWithFormMarker on an already-parsed
// node, for callers (browserclone) that run InlineStylesheetsWithRuntimeCSS
// first on the same node.
func SanitizeNodeWithFormMarker(doc *html.Node) bool {
	found := false
	stripNodeMarkingForm(doc, &found)
	return found
}

func stripNodeMarkingForm(n *html.Node, found *bool) {
	child := n.FirstChild
	for child != nil {
		next := child.NextSibling
		if child.Type == html.ElementNode && child.DataAtom == atom.Form {
			if !*found {
				if tagFormForCapture(child) {
					stripAttrs(child)
					stripNodeMarkingForm(child, found)
					*found = true
					child = next
					continue
				}
				n.InsertBefore(formMarkerNode(), child)
				n.RemoveChild(child)
				*found = true
				child = next
				continue
			}
			demoteFormToDiv(child)
			stripAttrs(child)
			stripNodeMarkingForm(child, found)
			child = next
			continue
		}
		if child.Type == html.ElementNode && droppedTags[child.DataAtom] {
			n.RemoveChild(child)
			child = next
			continue
		}
		if child.Type == html.ElementNode {
			stripAttrs(child)
		}
		stripNodeMarkingForm(child, found)
		child = next
	}
}

// demoteFormToDiv turns a non-primary <form> into a <div>, dropping only
// action/method/enctype - the attributes that let it submit anywhere.
func demoteFormToDiv(n *html.Node) {
	n.Data = "div"
	n.DataAtom = atom.Div
	var kept []html.Attribute
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "action", "method", "enctype":
			continue
		}
		kept = append(kept, a)
	}
	n.Attr = kept
}

// tagFormForCapture rewrites form's action/password/username fields to
// the sentinel values if it finds a password input, reporting true; a
// form with no password input (e.g. a search form) reports false.
func tagFormForCapture(form *html.Node) bool {
	var inputs []*html.Node
	var collect func(n *html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Input {
			inputs = append(inputs, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(form)

	var passwordInput, usernameInput *html.Node
	for _, in := range inputs {
		if strings.EqualFold(attrVal(in, "type"), "password") {
			passwordInput = in
			break
		}
	}
	if passwordInput == nil {
		return false
	}
	for _, in := range inputs {
		if in == passwordInput {
			continue
		}
		t := strings.ToLower(attrVal(in, "type"))
		ac := strings.ToLower(attrVal(in, "autocomplete"))
		if t == "" || t == "text" || t == "email" || strings.Contains(ac, "username") || strings.Contains(ac, "email") {
			usernameInput = in
			break
		}
	}

	setAttr(form, "action", SubmitActionSentinel)
	setAttr(form, "method", "post")
	removeAttr(form, "enctype")
	setAttr(passwordInput, "name", PasswordFieldSentinel)
	if usernameInput != nil {
		setAttr(usernameInput, "name", UsernameFieldSentinel)
	}
	return true
}

func formMarkerNode() *html.Node {
	const frag = `<div id="` + FormMarkerID + `" style="border:2px dashed #6366f1;padding:16px;text-align:center;color:#6366f1;font:14px sans-serif;">Sign-in form (rendered here for recipients)</div>`
	nodes, err := html.ParseFragment(strings.NewReader(frag), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil || len(nodes) == 0 {
		return &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	}
	return nodes[0]
}

// InlineStylesheets walks doc for <link rel="stylesheet"> elements,
// fetches each (SSRF-guarded) and replaces the <link> with an equivalent
// <style> holding the CSS verbatim. An unreachable stylesheet is skipped
// rather than failing the whole clone.
//
// Deliberate exception: background-image/font url(...) inside that CSS is
// rewritten to an absolute URL, not inlined as data: - it still loads
// live from the original host. See ARCHITECTURE.md.
func InlineStylesheets(doc *html.Node, baseURL string) error {
	return InlineStylesheetsWithRuntimeCSS(doc, baseURL, nil)
}

// InlineStylesheetsWithRuntimeCSS is InlineStylesheets, but checks
// runtimeCSS (CSS already captured live from document.styleSheets, see
// internal/browserclone) before fetching - some pages (e.g. GitHub's
// login) rewrite or remove a stylesheet <link>'s href after loading it,
// leaving nothing left to fetch by the time this runs; the href lookup
// also tries data-href for the same reason.
func InlineStylesheetsWithRuntimeCSS(doc *html.Node, baseURL string, runtimeCSS map[string]string) error {
	base, err := neturl.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("clone: parse base url: %w", err)
	}
	var links []*html.Node
	collectStylesheetLinks(doc, &links)
	for _, link := range links {
		if link.Parent == nil {
			continue
		}
		href := attrVal(link, "href")
		if href == "" {
			href = attrVal(link, "data-href")
		}
		if href == "" {
			continue
		}
		abs, err := base.Parse(href)
		if err != nil {
			continue
		}

		if css, ok := runtimeCSS[abs.String()]; ok {
			replaceLinkWithStyle(link, rewriteCSSURLs(css, abs))
			continue
		}

		body, finalURL, err := doFetch(abs.String())
		if err != nil {
			continue
		}
		cssBase, err := neturl.Parse(finalURL)
		if err != nil {
			continue
		}
		replaceLinkWithStyle(link, rewriteCSSURLs(string(body), cssBase))
	}
	return nil
}

func replaceLinkWithStyle(link *html.Node, css string) {
	style := &html.Node{Type: html.ElementNode, Data: "style", DataAtom: atom.Style}
	style.AppendChild(&html.Node{Type: html.TextNode, Data: css})
	link.Parent.InsertBefore(style, link)
	link.Parent.RemoveChild(link)
}

func collectStylesheetLinks(n *html.Node, out *[]*html.Node) {
	if n.Type == html.ElementNode && n.DataAtom == atom.Link &&
		strings.Contains(strings.ToLower(attrVal(n, "rel")), "stylesheet") {
		*out = append(*out, n)
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectStylesheetLinks(c, out)
	}
}

func attrVal(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func removeAttr(n *html.Node, key string) {
	var kept []html.Attribute
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			continue
		}
		kept = append(kept, a)
	}
	n.Attr = kept
}

// cssURLPattern matches a CSS url(...) reference. RE2 (Go's regexp) has no
// backreferences, so it can't require the closing quote to match the
// opening one - the replace function below strips a trailing quote itself
// when the content carries one.
var cssURLPattern = regexp.MustCompile(`url\(\s*(['"]?)([^)]*?)\s*\)`)

func rewriteCSSURLs(css string, base *neturl.URL) string {
	return cssURLPattern.ReplaceAllStringFunc(css, func(m string) string {
		sub := cssURLPattern.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		quote, ref := sub[1], sub[2]
		ref = strings.TrimSuffix(ref, quote)
		if ref == "" || strings.HasPrefix(ref, "data:") {
			return m
		}
		abs, err := base.Parse(ref)
		if err != nil {
			return m
		}
		return "url(" + quote + abs.String() + quote + ")"
	})
}
