package agent

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/htmltext"
)

// Normalize returns the form of a URL used to compare links: fragment
// dropped, scheme and host lowercased, and the trailing slash unified ("/a/"
// and "/a" are the same page; an empty path is "/"). It returns "" for
// anything that is not an absolute http or https URL.
func Normalize(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment, u.RawFragment = "", ""
	if p := strings.TrimRight(u.Path, "/"); p != "" {
		u.Path, u.RawPath = p, ""
	} else {
		u.Path, u.RawPath = "/", ""
	}
	return u.String()
}

// Link is one link found on an inspected page.
type Link struct {
	URL  string // absolute, fragment dropped
	Text string // the link text, squashed and shortened
	// Block names the nearest element around the link that holds more than
	// one link, such as a menu or a list of area pages: the page URL and a
	// number in page order. It is "" when no ancestor holds another link.
	Block string
}

// PageLinks returns the links on a page that lead to another http(s) page,
// on any host, in page order and without duplicates. It uses the same link
// rules as the baseline's page discovery.
func PageLinks(page *capture.Page) []Link {
	base, err := url.Parse(pageURL(page))
	if err != nil {
		return nil
	}
	doc := htmltext.Parse(page.HTML)
	var out []Link
	seen := map[string]bool{Normalize(base.String()): true}
	blocks := map[*html.Node]string{}
	linkCount := map[*html.Node]int{} // links inside an element, counted once
	blockOf := func(s *goquery.Selection) string {
		for p := s.Parent(); p.Length() > 0; p = p.Parent() {
			n := p.Get(0)
			c, ok := linkCount[n]
			if !ok {
				c = p.Find(capture.LinkSelector).Length()
				linkCount[n] = c
			}
			if c < 2 {
				continue
			}
			if _, ok := blocks[n]; !ok {
				blocks[n] = fmt.Sprintf("%s#%d", base, len(blocks)+1)
			}
			return blocks[n]
		}
		return ""
	}
	doc.Find(capture.LinkSelector).Each(func(_ int, s *goquery.Selection) {
		href := capture.LinkTarget(s)
		if href == "" || strings.HasPrefix(href, "#") {
			return
		}
		u, err := base.Parse(href)
		if err != nil {
			return
		}
		u.Fragment, u.RawFragment = "", ""
		n := Normalize(u.String())
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		text := htmltext.Squash(s.Text())
		if text == "" {
			text = strings.TrimSpace(s.AttrOr("title", ""))
		}
		text = htmltext.Shorten(text, 60)
		out = append(out, Link{URL: u.String(), Text: text, Block: blockOf(s)})
	})
	return out
}

// keywords decides which links name a contact, legal-information or privacy
// page, from the locale's keyword lists: the link text contains a keyword
// (case-insensitive), or the URL path contains its slug ("adatkezelés" →
// "adatkezeles", "írjon nekünk" → "irjon-nekunk").
type keywords struct {
	text, slug []string
}

func newKeywords(l *config.Locale) keywords {
	var k keywords
	if l == nil {
		return k
	}
	lists := [][]string{l.Contact.LinkKeywords, l.LegalPage.LinkKeywords, l.LegalPage.FallbackLinkKeywords}
	if l.Legal != nil {
		lists = append(lists, l.Legal.PrivacyLinkKeywords)
	}
	for _, list := range lists {
		for _, w := range list {
			if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
				k.text = append(k.text, w)
				k.slug = append(k.slug, strings.ReplaceAll(foldAccents(w), " ", "-"))
			}
		}
	}
	return k
}

func (k keywords) match(l Link) bool {
	text := strings.ToLower(l.Text)
	path := ""
	if u, err := url.Parse(l.URL); err == nil {
		path = foldAccents(strings.ToLower(u.Path))
	}
	for i, w := range k.text {
		if strings.Contains(text, w) || (path != "" && strings.Contains(path, k.slug[i])) {
			return true
		}
	}
	return false
}

// foldAccents drops the Hungarian accents from lowercase text, as URL slugs do.
func foldAccents(s string) string {
	return accentFolder.Replace(s)
}

var accentFolder = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ö", "o", "ő", "o", "ú", "u", "ü", "u", "ű", "u")

func pageURL(p *capture.Page) string {
	if p.FinalURL != "" {
		return p.FinalURL
	}
	return p.RequestedURL
}

// registry is the agent's action space for inspect_page: the links seen on
// inspected pages, and which pages were inspected. The model cannot open a
// URL it was not shown, however it is prompted.
type registry struct {
	links     map[string]Link // normalised URL → first sighting
	listed    map[string]bool // already shown to the model in a result
	inspected map[string]int  // normalised URL → inspection number; 0 for baseline pages
	pages     map[string]int  // normalised URL → inspected pages it was found on
}

func newRegistry() *registry {
	return &registry{links: map[string]Link{}, listed: map[string]bool{}, inspected: map[string]int{}, pages: map[string]int{}}
}

// add records the links of inspected pages and returns those not yet
// inspected. Each page's links come without duplicates (PageLinks), so the
// count of a link is the number of pages it was found on.
func (r *registry) add(links []Link) []Link {
	var out []Link
	for _, l := range links {
		n := Normalize(l.URL)
		if _, ok := r.links[n]; !ok {
			r.links[n] = l
		}
		r.pages[n]++
		if _, done := r.inspected[n]; !done {
			out = append(out, l)
		}
	}
	return out
}

// repeated reports whether a link was found on more than one inspected page,
// as the site's own menu and footer links are.
func (r *registry) repeated(u string) bool { return r.pages[Normalize(u)] > 1 }

// allowed reports whether a URL appeared as a link on an inspected page.
func (r *registry) allowed(u string) bool {
	_, ok := r.links[Normalize(u)]
	return ok
}

// markInspected records a page under every URL it is known by (the requested
// URL and where it ended up).
func (r *registry) markInspected(n int, urls ...string) {
	for _, u := range urls {
		if k := Normalize(u); k != "" {
			r.inspected[k] = n
		}
	}
}

// inspection returns the inspection number of a page and whether it was
// inspected.
func (r *registry) inspection(u string) (int, bool) {
	n, ok := r.inspected[Normalize(u)]
	return n, ok
}
