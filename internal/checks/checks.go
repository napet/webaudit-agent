package checks

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/htmltext"
	"github.com/napet/webaudit-agent/internal/report"
)

// SlowLoadMS is the load time above which a site gets the slow flag.
const SlowLoadMS = 10_000

// Input is everything Evaluate looks at. All of it is recorded in snapshots.
type Input struct {
	Capture   *capture.Result
	Probes    *Probes
	PageSpeed *report.PageSpeed // nil when PageSpeed Insights failed
	Locale    *config.Locale
	Now       time.Time // the audit time, so replays judge "stale" the same way
}

// Output is the result of every check.
type Output struct {
	Checks        []report.CheckResult // all results, legal ones included
	LegalGaps     []report.CheckResult // legal results that found a gap
	Accessibility *report.Accessibility
}

// Evaluate runs every check over recorded observations. It makes no network
// requests and is deterministic for a given Input.
func Evaluate(in Input) Output {
	var out Output
	add := func(r report.CheckResult) {
		out.Checks = append(out.Checks, r)
		if r.Category == report.CatLegal && r.Status == report.StatusFail {
			out.LegalGaps = append(out.LegalGaps, r)
		}
	}
	add(siteErrorOrSlow(in.Capture))
	add(https(in.Capture, in.Probes))
	if in.Capture == nil || !in.Capture.Home.Loaded() {
		return out
	}
	home := doc(in.Capture.Home.HTML)
	add(mobileUsability(in.Capture, home, in.PageSpeed))
	add(performance(in.PageSpeed))
	var axe report.CheckResult
	out.Accessibility, axe = accessibility(in.Capture)
	add(axe)
	for _, r := range staleness(in.Capture, home, in.Now) {
		add(r)
	}
	add(contactForm(in.Capture, in.Probes))
	if in.Locale != nil && in.Locale.Legal != nil {
		for _, r := range legal(in.Capture, in.Locale.Legal) {
			add(r)
		}
	}
	return out
}

func doc(html string) *goquery.Document { return htmltext.Parse(html) }

func result(id, cat string, st report.Status, flag, expl string, ev map[string]any) report.CheckResult {
	if st != report.StatusFail {
		flag = ""
	}
	return report.CheckResult{ID: id, Category: cat, Status: st, Flag: flag, Explanation: expl, Evidence: ev}
}

func errResult(id, cat, msg string) report.CheckResult {
	return report.CheckResult{ID: id, Category: cat, Status: report.StatusError, Explanation: "The check could not run.", Error: msg}
}

func siteErrorOrSlow(c *capture.Result) report.CheckResult {
	const id = "site_error_or_slow"
	if c == nil {
		return errResult(id, report.CatPerformance, "no capture")
	}
	h := c.Home
	ev := map[string]any{"status": h.Status, "load_ms": h.LoadMS}
	switch {
	case h.LoadError != "":
		ev["error"] = h.LoadError
		return result(id, report.CatPerformance, report.StatusFail, report.FlagSiteErrorOrSlow,
			"The homepage could not be loaded at all.", ev)
	case h.Status >= 500 || h.Status == 0:
		return result(id, report.CatPerformance, report.StatusFail, report.FlagSiteErrorOrSlow,
			fmt.Sprintf("The homepage answered with a server error (HTTP %d).", h.Status), ev)
	case h.TimedOut:
		ev["timed_out"] = true
		return result(id, report.CatPerformance, report.StatusFail, report.FlagSiteErrorOrSlow,
			"The homepage never finished loading.", ev)
	case h.LoadMS > SlowLoadMS:
		return result(id, report.CatPerformance, report.StatusFail, report.FlagSiteErrorOrSlow,
			fmt.Sprintf("The homepage took %.1f seconds to load.", float64(h.LoadMS)/1000), ev)
	}
	return result(id, report.CatPerformance, report.StatusPass, "", "The homepage loads without errors in reasonable time.", ev)
}

func https(c *capture.Result, p *Probes) report.CheckResult {
	const id = "https"
	if p == nil || p.TLS == nil {
		return errResult(id, report.CatSecurity, "no probe data")
	}
	ev := map[string]any{}
	finalHTTPS := c != nil && strings.HasPrefix(c.Home.FinalURL, "https://")
	if c != nil {
		ev["final_url"] = c.Home.FinalURL
	}
	if p.TLS.DialError != "" {
		ev["tls_error"] = p.TLS.DialError
		return result(id, report.CatSecurity, report.StatusFail, report.FlagNoHTTPS,
			"The site is not available over HTTPS.", ev)
	}
	if !p.TLS.NotAfter.IsZero() {
		ev["certificate_expires"] = p.TLS.NotAfter.UTC().Format(time.DateOnly)
		ev["issuer"] = p.TLS.Issuer
	}
	if !p.TLS.Valid {
		ev["certificate_error"] = p.TLS.VerifyError
		return result(id, report.CatSecurity, report.StatusFail, report.FlagNoHTTPS,
			"The site's security certificate is not valid, so browsers show a warning.", ev)
	}
	if hp := p.PlainHTTP; hp != nil && hp.Error == "" {
		ev["http_status"], ev["http_location"] = hp.Status, hp.Location
		if hp.Status < 300 || hp.Status >= 400 || !strings.HasPrefix(strings.ToLower(hp.Location), "https://") {
			return result(id, report.CatSecurity, report.StatusFail, report.FlagNoHTTPS,
				"Visitors who type the address without https:// get an insecure page instead of being redirected.", ev)
		}
	}
	if c != nil && c.Home.FinalURL != "" && !finalHTTPS {
		return result(id, report.CatSecurity, report.StatusFail, report.FlagNoHTTPS,
			"The homepage is served over plain HTTP.", ev)
	}
	return result(id, report.CatSecurity, report.StatusPass, "", "The site uses HTTPS with a valid certificate.", ev)
}

// responsiveViewport reports whether a viewport tag's content lets the page
// follow the phone's width: width=device-width, or an initial scale of 1 with
// no width set (browsers then use the device width). A fixed width, such as
// width=980, is not responsive whatever the scale; neither is a missing tag.
func responsiveViewport(content string) bool {
	kv := map[string]string{}
	for _, part := range strings.FieldsFunc(strings.ToLower(content), func(r rune) bool { return r == ',' || r == ';' }) {
		k, v, _ := strings.Cut(strings.ReplaceAll(part, " ", ""), "=")
		kv[k] = v
	}
	if w, ok := kv["width"]; ok {
		return w == "device-width"
	}
	scale, err := strconv.ParseFloat(kv["initial-scale"], 64)
	return err == nil && scale == 1
}

func mobileUsability(c *capture.Result, home *goquery.Document, ps *report.PageSpeed) report.CheckResult {
	const id = "mobile_usability"
	ev := map[string]any{}
	var problems []string
	vp := home.Find(`meta[name="viewport"]`).AttrOr("content", "")
	ev["viewport_meta"] = vp
	if !responsiveViewport(vp) {
		problems = append(problems, "no responsive viewport setting")
	}
	var slight string
	if over := c.Mobile.WidestRight - c.Mobile.ViewportWidth; c.Mobile.ViewportWidth > 0 && over > 1 {
		ev["mobile_content_width"], ev["widest_element"] = c.Mobile.WidestRight, c.Mobile.Widest
		msg := fmt.Sprintf("content is %dpx wide on a %dpx phone screen", c.Mobile.WidestRight, c.Mobile.ViewportWidth)
		if over > slightOverflowPx {
			problems = append(problems, msg)
		} else {
			slight = msg
		}
	}
	if ps != nil {
		for _, a := range []string{"font-size", "tap-targets"} {
			if s, ok := ps.Audits[a]; ok && s != nil && *s == 0 {
				ev["lighthouse_"+a] = 0
				problems = append(problems, "Lighthouse "+a+" audit fails")
			}
		}
	}
	if len(problems) > 0 {
		ev["problems"] = problems
		return result(id, report.CatMobile, report.StatusFail, report.FlagNotMobileUsable,
			"The site is hard to use on a phone: "+strings.Join(problems, "; ")+".", ev)
	}
	if slight != "" {
		return result(id, report.CatMobile, report.StatusSuspicious, "",
			"The site adapts to phone screens, but scrolls sideways slightly: "+slight+".", ev)
	}
	return result(id, report.CatMobile, report.StatusPass, "", "The site adapts to phone screens.", ev)
}

// slightOverflowPx is how far content may reach past the phone screen (about
// 5% of its width) before sideways scrolling counts as a usability failure
// rather than a cosmetic glitch.
const slightOverflowPx = 20

func performance(ps *report.PageSpeed) report.CheckResult {
	const id = "pagespeed_performance"
	if ps == nil {
		return errResult(id, report.CatPerformance, "PageSpeed Insights unavailable")
	}
	score, ok := ps.Scores["performance"]
	if !ok {
		return errResult(id, report.CatPerformance, "no performance score in PageSpeed result")
	}
	ev := map[string]any{"lighthouse_performance": score}
	if lcp, ok := ps.Field["LARGEST_CONTENTFUL_PAINT_MS"]; ok {
		ev["field_lcp_ms"], ev["field_lcp_category"] = lcp.Percentile, lcp.Category
	}
	if score < 0.5 {
		return result(id, report.CatPerformance, report.StatusFail, "",
			fmt.Sprintf("Google's PageSpeed test rates mobile performance %d/100.", int(score*100+0.5)), ev)
	}
	return result(id, report.CatPerformance, report.StatusPass, "",
		fmt.Sprintf("Google's PageSpeed test rates mobile performance %d/100.", int(score*100+0.5)), ev)
}

func accessibility(c *capture.Result) (*report.Accessibility, report.CheckResult) {
	const id = "accessibility_axe"
	if len(c.Axe) == 0 {
		return nil, errResult(id, report.CatAccessibility, c.AxeError)
	}
	var raw struct {
		Version    string                `json:"version"`
		Violations []report.AxeViolation `json:"violations"`
	}
	if err := json.Unmarshal(c.Axe, &raw); err != nil {
		return nil, errResult(id, report.CatAccessibility, "parse axe results: "+err.Error())
	}
	a := &report.Accessibility{AxeVersion: raw.Version, ByImpact: map[string]int{}, Violations: raw.Violations}
	for _, v := range raw.Violations {
		a.ByImpact[v.Impact] += v.Nodes
	}
	serious := a.ByImpact["critical"] + a.ByImpact["serious"]
	ev := map[string]any{"by_impact": a.ByImpact}
	if serious > 0 {
		return a, result(id, report.CatAccessibility, report.StatusFail, "",
			fmt.Sprintf("The automated accessibility scan found %d serious or critical problems.", serious), ev)
	}
	return a, result(id, report.CatAccessibility, report.StatusPass, "",
		"The automated accessibility scan found no serious problems.", ev)
}
