// Package browserclone renders a page through a headless Chrome instance
// (JS executed, CSS applied) so a cloned landing page actually looks like
// its target, then runs it through internal/clone (inline stylesheets,
// then sanitize - in that order, since sanitizing strips remote href/src
// before inlining would get a chance to use it). netguard validates the
// URL before and after navigation, but the real backstop against a
// rendering page's own JS sub-requests is network isolation: the
// baitbet-browser container has no route to anything but the public
// internet and this service's CDP connection.
package browserclone

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
	"golang.org/x/net/html"

	"github.com/rudra184/baitbet/internal/clone"
	"github.com/rudra184/baitbet/internal/netguard"
)

const renderTimeout = 25 * time.Second
const settleDelay = 2500 * time.Millisecond // lets hydration-style frameworks finish painting

// viewportWidth/Height force a desktop layout - without this a
// responsive page (e.g. github.com/login) clones as its narrow,
// below-breakpoint layout even with every stylesheet correctly inlined.
const (
	viewportWidth  = 1440
	viewportHeight = 1024
)

// FetchRendered navigates to pageURL via the headless Chrome at wsURL and
// returns sanitized, mostly self-contained HTML plus whether a form was found.
func FetchRendered(ctx context.Context, wsURL, pageURL string) (resultHTML string, formFound bool, err error) {
	if err := netguard.ValidateURL(pageURL); err != nil {
		return "", false, fmt.Errorf("browserclone: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, renderTimeout)
	defer cancel()

	allocCtx, cancelAlloc := remote.NewAllocator(ctx, wsURL)
	defer cancelAlloc()
	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	defer cancelTab()

	if err := chromedp.Do(tabCtx,
		chromedp.EmulateViewport(viewportWidth, viewportHeight),
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body"),
		waitForSettle(),
	); err != nil {
		return "", false, fmt.Errorf("browserclone: render %q: %w", pageURL, err)
	}

	finalURL, err := chromedp.Run(tabCtx, chromedp.Location())
	if err != nil {
		return "", false, fmt.Errorf("browserclone: read final location: %w", err)
	}
	if err := netguard.ValidateURL(finalURL); err != nil {
		return "", false, fmt.Errorf("browserclone: page navigated to an unsafe address: %w", err)
	}

	rawHTML, err := chromedp.Run(tabCtx, chromedp.OuterHTML("html"))
	if err != nil {
		return "", false, fmt.Errorf("browserclone: capture rendered html: %w", err)
	}

	runtimeCSS, err := collectRuntimeCSS(tabCtx)
	if err != nil {
		runtimeCSS = nil // best-effort; falls back to fetching
	}

	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return "", false, fmt.Errorf("browserclone: parse rendered html: %w", err)
	}

	// Must inline before sanitizing - sanitizing strips remote href/src.
	if err := clone.InlineStylesheetsWithRuntimeCSS(doc, finalURL, runtimeCSS); err != nil {
		return "", false, fmt.Errorf("browserclone: inline stylesheets: %w", err)
	}
	found := clone.SanitizeNodeWithFormMarker(doc)

	var buf strings.Builder
	if err := html.Render(&buf, doc); err != nil {
		return "", false, fmt.Errorf("browserclone: render final html: %w", err)
	}
	return buf.String(), found, nil
}

// extractStylesheetCSSJS reads every loaded stylesheet via the live CSSOM
// (document.styleSheets), keyed by sheet.href - which keeps pointing at
// the real fetch URL even if a page later mutates its <link>'s href.
// Inline <style> elements are skipped (already in the captured outerHTML).
// A cross-origin sheet without permissive CORS throws on cssRules access;
// it's just left out, and the caller falls back to fetching it directly.
const extractStylesheetCSSJS = `(() => {
	const out = {};
	for (const sheet of document.styleSheets) {
		if (!sheet.href) continue;
		try {
			let text = '';
			for (const rule of sheet.cssRules) text += rule.cssText + '\n';
			out[sheet.href] = text;
		} catch (e) {
			// cross-origin without CORS, or another access error - leave unset.
		}
	}
	return JSON.stringify(out);
})()`

// collectRuntimeCSS runs extractStylesheetCSSJS in tabCtx and decodes its result.
func collectRuntimeCSS(tabCtx context.Context) (map[string]string, error) {
	raw, err := chromedp.Run(tabCtx, chromedp.Evaluate[string](extractStylesheetCSSJS))
	if err != nil {
		return nil, fmt.Errorf("browserclone: extract live stylesheets: %w", err)
	}
	var css map[string]string
	if err := json.Unmarshal([]byte(raw), &css); err != nil {
		return nil, fmt.Errorf("browserclone: decode live stylesheets: %w", err)
	}
	return css, nil
}

// waitForSettle is a simple heuristic, not exhaustive readiness detection.
func waitForSettle() chromedp.Action[chromedp.Void] {
	return func(ctx context.Context, t *chromedp.Target) (chromedp.Void, error) {
		_, _ = chromedp.Evaluate[chromedp.Void](
			`document.fonts && document.fonts.ready ? document.fonts.ready.then(() => {}) : Promise.resolve()`,
			chromedp.EvalAwaitPromise,
		)(ctx, t)
		return chromedp.Sleep(settleDelay)(ctx, t)
	}
}
