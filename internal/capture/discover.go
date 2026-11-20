package capture

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/htmltext"
)

// LinkSelector matches ordinary links and elements that navigate from an
// onclick handler, such as <button onclick="window.location.href='/x'">.
const LinkSelector = "a[href], [onclick]"

var onclickNav = regexp.MustCompile(`location(?:\.href)?\s*=\s*['"]([^'"]+)['"]|location\.(?:assign|replace)\(\s*['"]([^'"]+)['"]`)

// LinkTarget returns where a LinkSelector match leads: the href of a link, or
// the URL an onclick handler navigates to. It returns "" for anything else.
func LinkTarget(s *goquery.Selection) string {
	if href, ok := s.Attr("href"); ok && goquery.NodeName(s) == "a" {
		return strings.TrimSpace(href)
	}
	if m := onclickNav.FindStringSubmatch(s.AttrOr("onclick", "")); m != nil {
		return strings.TrimSpace(m[1] + m[2])
	}
	return ""
}

// FindLink returns a same-site link on the page whose text, title or target
// matches one of the keywords (case-insensitive substring). Keywords are in
// order of preference: a link matching an earlier keyword wins over one that
// appears earlier on the page.
func FindLink(base, html string, keywords []string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	doc := htmltext.Parse(html)
	found, rank := "", len(keywords)
	doc.Find(LinkSelector).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		href := LinkTarget(s)
		if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") ||
			strings.HasPrefix(href, "tel:") || strings.HasPrefix(href, "javascript:") {
			return true
		}
		u, err := b.Parse(href)
		if err != nil || !sameSite(u.Host, b.Host) {
			return true
		}
		hay := Fold(htmltext.Squash(s.Text()) + " " + s.AttrOr("title", "") + " " + u.Path)
		for i, k := range keywords[:rank] {
			if strings.Contains(hay, Fold(k)) {
				u.Fragment = ""
				found, rank = u.String(), i
				break
			}
		}
		return rank > 0
	})
	return found
}

func sameSite(a, b string) bool {
	return strings.TrimPrefix(strings.ToLower(a), "www.") == strings.TrimPrefix(strings.ToLower(b), "www.")
}

// Discover finds a page by homepage link first, then by guessed paths that
// answer 200, then by a fallback link. It returns the URL, how it was found
// ("link", "path" or "fallback_link"), and the status of every guessed path
// it tried.
func Discover(ctx context.Context, client *http.Client, home *Page, rule config.PageRule) (string, string, map[string]int) {
	base := home.FinalURL
	if base == "" {
		base = home.RequestedURL
	}
	if u := FindLink(base, home.HTML, rule.LinkKeywords); u != "" {
		return u, "link", nil
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", "", nil
	}
	tried := map[string]int{}
	for _, p := range rule.Paths {
		u := b.ResolveReference(&url.URL{Path: p})
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			tried[p] = 0
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) // drain for reuse; only the status matters
		resp.Body.Close()
		tried[p] = resp.StatusCode
		// Many sites answer every unknown path by redirecting to the homepage;
		// that is not a contact page.
		if resp.StatusCode == http.StatusOK && strings.TrimRight(resp.Request.URL.Path, "/") != strings.TrimRight(b.Path, "/") {
			return u.String(), "path", tried
		}
	}
	if u := FindLink(base, home.HTML, rule.FallbackLinkKeywords); u != "" {
		return u, "fallback_link", tried
	}
	return "", "", tried
}
