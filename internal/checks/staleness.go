package checks

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/htmltext"
	"github.com/napet/webaudit-agent/internal/report"
)

var (
	copyrightRe = regexp.MustCompile(`(?i)(?:©|&copy;|\(c\)|copyright)[^0-9]{0,40}?((?:19|20)\d{2})(?:\s*[-–—]\s*((?:19|20)\d{2}))?`)
	jqueryRes   = []*regexp.Regexp{
		regexp.MustCompile(`(?i)jquery[.-]?(\d+)\.(\d+)(?:\.(\d+))?(?:\.slim)?(?:\.min)?\.js`),
		regexp.MustCompile(`(?i)jquery(?:\.slim)?(?:\.min)?\.js\?ver=(\d+)\.(\d+)(?:\.(\d+))?`),
		regexp.MustCompile(`(?i)/jquery/(\d+)\.(\d+)(?:\.(\d+))?/`),
	}
)

func staleness(c *capture.Result, home *goquery.Document, now time.Time) []report.CheckResult {
	return []report.CheckResult{
		copyright(home, now),
		oldJQuery(c, home),
		tableLayout(home),
		obsoleteMarkup(home),
		doctype(c),
	}
}

func copyright(home *goquery.Document, now time.Time) report.CheckResult {
	const id = "stale_copyright"
	text := home.Find("body").Text()
	best, match := 0, ""
	for _, m := range copyrightRe.FindAllStringSubmatch(text, -1) {
		for _, g := range m[1:] {
			if y, err := strconv.Atoi(g); err == nil && y > best && y <= now.Year()+1 {
				best, match = y, htmltext.Squash(m[0])
			}
		}
	}
	if best == 0 {
		return result(id, report.CatStaleness, report.StatusNotApplicable, "", "No copyright year found.", nil)
	}
	ev := map[string]any{"year": best, "text": match, "current_year": now.Year()}
	if best <= now.Year()-2 {
		return result(id, report.CatStaleness, report.StatusFail, "",
			fmt.Sprintf("The copyright notice still says %d, which suggests the site has not been updated since.", best), ev)
	}
	return result(id, report.CatStaleness, report.StatusPass, "", "The copyright year is current.", ev)
}

func oldJQuery(c *capture.Result, home *goquery.Document) report.CheckResult {
	const id = "old_jquery"
	var urls []string
	home.Find("script[src]").Each(func(_ int, s *goquery.Selection) { urls = append(urls, s.AttrOr("src", "")) })
	for _, r := range c.Home.Requests {
		if r.Type == "Script" {
			urls = append(urls, r.URL)
		}
	}
	for _, u := range urls {
		for _, re := range jqueryRes {
			m := re.FindStringSubmatch(u)
			if m == nil {
				continue
			}
			major, _ := strconv.Atoi(m[1])
			version := m[1] + "." + m[2]
			if m[3] != "" {
				version += "." + m[3]
			}
			ev := map[string]any{"script": u, "version": version}
			if major < 3 {
				return result(id, report.CatStaleness, report.StatusFail, "",
					fmt.Sprintf("The site loads jQuery %s, a version from before 2016 that no longer gets security fixes.", version), ev)
			}
			return result(id, report.CatStaleness, report.StatusPass, "", "The site's jQuery version is current enough.", ev)
		}
	}
	return result(id, report.CatStaleness, report.StatusNotApplicable, "", "The site does not load jQuery.", nil)
}

func tableLayout(home *goquery.Document) report.CheckResult {
	const id = "table_layout"
	nested := home.Find("table table").Length()
	top := home.Find("body > table").Length()
	ev := map[string]any{"nested_tables": nested, "top_level_tables": top}
	if nested > 0 || top > 0 {
		return result(id, report.CatStaleness, report.StatusFail, "",
			"The page layout is built from HTML tables, a technique from the early 2000s that does not adapt to phones.", ev)
	}
	return result(id, report.CatStaleness, report.StatusPass, "", "The layout does not use tables.", ev)
}

func obsoleteMarkup(home *goquery.Document) report.CheckResult {
	const id = "obsolete_markup"
	found := map[string]int{}
	count := func(name, sel string) {
		if n := home.Find(sel).Length(); n > 0 {
			found[name] = n
		}
	}
	count("flash", `object[type*="shockwave"], embed[type*="shockwave"], embed[src$=".swf"], object[data$=".swf"], param[value$=".swf"]`)
	count("applet", "applet")
	count("marquee", "marquee")
	count("blink", "blink")
	count("font", "font")
	count("frameset", "frameset, frame")
	count("center", "center")
	if len(found) > 0 {
		var names []string
		for _, n := range []string{"flash", "applet", "frameset", "marquee", "blink", "font", "center"} {
			if found[n] > 0 {
				names = append(names, n)
			}
		}
		return result(id, report.CatStaleness, report.StatusFail, "",
			"The page uses obsolete HTML ("+strings.Join(names, ", ")+").", map[string]any{"elements": found})
	}
	return result(id, report.CatStaleness, report.StatusPass, "", "No obsolete HTML elements found.", nil)
}

func doctype(c *capture.Result) report.CheckResult {
	const id = "html5_doctype"
	dt := c.Home.Doctype
	ev := map[string]any{"doctype": dt}
	switch {
	case dt == "":
		return result(id, report.CatStaleness, report.StatusFail, "", "The page has no doctype, so browsers render it in legacy quirks mode.", ev)
	case strings.EqualFold(dt, "html"):
		return result(id, report.CatStaleness, report.StatusPass, "", "The page uses the HTML5 doctype.", ev)
	}
	return result(id, report.CatStaleness, report.StatusFail, "", "The page declares a pre-HTML5 doctype ("+dt+").", ev)
}
