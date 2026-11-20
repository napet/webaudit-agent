package checks

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
)

// Cookies and request hosts that indicate analytics or advertising tracking.
var (
	trackerCookies = []string{"_ga", "_gid", "_gat", "_fbp", "_fbc", "_gcl_au", "_hjsession", "_hjid", "_clck", "_clsk", "_uetvid", "_uetsid", "ide", "_pin_unauth", "_tt_enable_cookie"}
	trackerHosts   = []string{"google-analytics.com", "analytics.google.com", "doubleclick.net", "facebook.com/tr", "connect.facebook.net", "hotjar.com", "clarity.ms", "bat.bing.com", "analytics.tiktok.com"}
)

// legal reports possible legal gaps, always phrased as what could not be
// found. They never raise hard flags: finding things can fail for ordinary
// reasons (details in a PDF or an image, a page not visited).
func legal(c *capture.Result, rules *config.LegalRules) []report.CheckResult {
	pages := legalPages(c)
	return []report.CheckResult{
		providerDetails(pages, rules.ProviderDetails),
		privacyNotice(c, pages, rules.PrivacyLinkKeywords),
		trackingBeforeConsent(&c.Home),
	}
}

func legalPages(c *capture.Result) []*capture.Page {
	pages := []*capture.Page{&c.Home}
	for _, p := range []*capture.Page{c.Contact, c.Legal} {
		if p != nil && p.Loaded() && !containsPage(pages, p) {
			pages = append(pages, p)
		}
	}
	return append(pages, AgentPages(c)...)
}

// PageProviderDetails is the provider-details search over a single page, as
// the agent's page inspection reports it.
func PageProviderDetails(page *capture.Page, pd config.ProviderDetails) report.CheckResult {
	return providerDetails([]*capture.Page{page}, pd)
}

// PagePrivacyLink returns the privacy-notice link on a page, or "".
func PagePrivacyLink(page *capture.Page, keywords []string) string {
	return privacyLink(page, keywords)
}

func privacyLink(p *capture.Page, keywords []string) string {
	var hit string
	doc(p.HTML).Find(capture.LinkSelector).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		target := capture.LinkTarget(s)
		if target == "" {
			return true
		}
		hay := capture.Fold(s.Text() + " " + target)
		for _, k := range keywords {
			if strings.Contains(hay, capture.Fold(k)) {
				hit = target
				return false
			}
		}
		return true
	})
	return hit
}

func containsPage(ps []*capture.Page, p *capture.Page) bool {
	for _, q := range ps {
		if q == p || q.FinalURL == p.FinalURL {
			return true
		}
	}
	return false
}

func urls(ps []*capture.Page) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.FinalURL)
	}
	return out
}

func providerDetails(pages []*capture.Page, pd config.ProviderDetails) report.CheckResult {
	const id = "legal_provider_details"
	var text strings.Builder
	for _, p := range pages {
		text.WriteString(doc(p.HTML).Text())
		text.WriteByte('\n')
	}
	var found, missing []string
	for name, re := range pd.Compiled {
		if re.MatchString(text.String()) {
			found = append(found, name)
		} else {
			missing = append(missing, name)
		}
	}
	sort.Strings(found)
	sort.Strings(missing)
	ev := map[string]any{"pages_searched": urls(pages), "found": found, "not_found": missing, "min_matches": pd.MinMatches}
	if len(found) < pd.MinMatches {
		return result(id, report.CatLegal, report.StatusFail, "",
			fmt.Sprintf("Could not find the business's provider details (impresszum): only %d of the expected items appear on the pages checked.", len(found)), ev)
	}
	return result(id, report.CatLegal, report.StatusPass, "", "Provider details (impresszum) were found.", ev)
}

func privacyNotice(c *capture.Result, pages []*capture.Page, keywords []string) report.CheckResult {
	const id = "legal_privacy_notice"
	hasForm := false
	for _, p := range pages {
		for _, f := range p.Forms {
			if !f.Search && (f.Textarea || f.Email) {
				hasForm = true
			}
		}
	}
	if !hasForm {
		return result(id, report.CatLegal, report.StatusNotApplicable, "",
			"No form collects personal data, so this was not checked.", nil)
	}
	for _, p := range pages {
		if hit := privacyLink(p, keywords); hit != "" {
			return result(id, report.CatLegal, report.StatusPass, "", "A privacy notice is linked.",
				map[string]any{"link": hit, "on_page": p.FinalURL})
		}
	}
	return result(id, report.CatLegal, report.StatusFail, "",
		"Could not find a privacy notice (adatkezelési tájékoztató), although the site has a form that collects personal data.",
		map[string]any{"pages_searched": urls(pages)})
}

func trackingBeforeConsent(home *capture.Page) report.CheckResult {
	const id = "legal_tracking_before_consent"
	var cookies, reqs []string
	for _, ck := range home.Cookies {
		n := strings.ToLower(ck.Name)
		for _, t := range trackerCookies {
			if n == t || strings.HasPrefix(n, t+"_") || (strings.HasSuffix(t, "session") && strings.HasPrefix(n, t)) {
				cookies = append(cookies, ck.Name)
				break
			}
		}
	}
	for _, r := range home.Requests {
		u, err := url.Parse(r.URL)
		if err != nil {
			continue
		}
		hostPath := strings.ToLower(u.Host + u.Path)
		for _, h := range trackerHosts {
			// The query string of a tracker hit is kilobytes of IDs that tell
			// a reader (or the model) nothing; host and path name the tracker.
			if strings.Contains(hostPath, h) && !slices.Contains(reqs, u.Host+u.Path) {
				reqs = append(reqs, u.Host+u.Path)
				break
			}
		}
		if len(reqs) >= 5 {
			break
		}
	}
	if len(cookies) == 0 && len(reqs) == 0 {
		return result(id, report.CatLegal, report.StatusPass, "",
			"No tracking was seen before the visitor made a consent choice.", nil)
	}
	ev := map[string]any{}
	if len(cookies) > 0 {
		ev["cookies"] = cookies
	}
	if len(reqs) > 0 {
		ev["requests"] = reqs
	}
	return result(id, report.CatLegal, report.StatusFail, "",
		"Analytics or advertising tracking starts as soon as the page loads, before the visitor has agreed to it.", ev)
}
