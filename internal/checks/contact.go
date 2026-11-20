package checks

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/report"
)

// ContactForms returns the forms that look like contact forms: on the
// contact page when one was found, otherwise on the homepage. Search and
// login forms are left out. Agent pages are not included; see PageContactForms.
func ContactForms(c *capture.Result) []capture.Form {
	return PageContactForms(contactPage(c))
}

// PageContactForms returns the forms on one page that look like contact forms.
func PageContactForms(page *capture.Page) []capture.Form {
	var out []capture.Form
	for _, f := range page.Forms {
		if !f.Search && !f.Password && (f.Textarea || f.Email) {
			out = append(out, f)
		}
	}
	return out
}

func contactPage(c *capture.Result) *capture.Page {
	if c.Contact != nil && c.Contact.Loaded() {
		return c.Contact
	}
	return &c.Home
}

// AgentPages returns the pages the agent inspected that loaded properly, in
// order, leaving out any that ended on a page already in the capture.
func AgentPages(c *capture.Result) []*capture.Page {
	seen := map[string]bool{c.Home.FinalURL: true}
	for _, p := range []*capture.Page{c.Contact, c.Legal} {
		if p != nil {
			seen[p.FinalURL] = true
		}
	}
	var out []*capture.Page
	for i := range c.Agent {
		p := &c.Agent[i]
		if !p.Loaded() || p.Status >= 400 || p.TimedOut || seen[p.FinalURL] {
			continue
		}
		seen[p.FinalURL] = true
		out = append(out, p)
	}
	return out
}

type formVerdict struct {
	broken     bool
	suspicious []string
	reason     string
	evidence   map[string]any
}

// contactForm classifies the contact forms from passive evidence only. Only
// an action URL that is gone (404/410, unknown host) or a form with nowhere
// to submit counts as definitely broken; softer signals are suspicious.
// In agent mode the forms on the agent's pages are judged too.
func contactForm(c *capture.Result, p *Probes) report.CheckResult {
	pages := append([]*capture.Page{contactPage(c)}, AgentPages(c)...)
	return judgeContactForms(pages, p)
}

// PageContactForm is the contact-form check over a single page, as the
// agent's page inspection reports it.
func PageContactForm(page *capture.Page, p *Probes) report.CheckResult {
	return judgeContactForms([]*capture.Page{page}, p)
}

// judgeContactForms judges the contact forms on pages; pages[0] is the main
// one. With more than one page, each form's evidence names its page.
func judgeContactForms(pages []*capture.Page, p *Probes) report.CheckResult {
	const id = "contact_form"
	multi := len(pages) > 1
	var verdicts []formVerdict
	for _, page := range pages {
		pageSignals := suspiciousPageSignals(page)
		for _, f := range PageContactForms(page) {
			v := judgeForm(f, p, pageSignals)
			if multi {
				v.evidence["page"] = page.FinalURL
				if v.broken {
					v.reason += " (Page: " + page.FinalURL + ")"
				}
			}
			verdicts = append(verdicts, v)
		}
	}
	ev := map[string]any{"page": pages[0].FinalURL}
	if multi {
		ev["agent_pages"] = urls(pages[1:])
	}
	if len(verdicts) == 0 {
		return result(id, report.CatContact, report.StatusNotApplicable, "", "No contact form found.", ev)
	}
	formsEv := make([]map[string]any, len(verdicts))
	for i, v := range verdicts {
		formsEv[i] = v.evidence
	}
	ev["forms"] = formsEv
	for _, v := range verdicts {
		if v.broken {
			return result(id, report.CatContact, report.StatusFail, report.FlagBrokenContactForm, v.reason, ev)
		}
	}
	var sus []string
	for _, v := range verdicts {
		sus = append(sus, v.suspicious...)
	}
	if len(sus) > 0 {
		return result(id, report.CatContact, report.StatusSuspicious, "",
			"The contact form may not work: "+strings.Join(dedupe(sus), "; ")+".", ev)
	}
	return result(id, report.CatContact, report.StatusPass, "", "The contact form looks like it works (it was not submitted).", ev)
}

func judgeForm(f capture.Form, p *Probes, pageSignals []string) formVerdict {
	v := formVerdict{evidence: map[string]any{"index": f.Index, "action": f.Action, "method": f.Method, "has_handler": f.HasHandler}}
	switch {
	case f.Action == "" && !f.HasHandler:
		v.broken = true
		v.reason = "The contact form has no address to send to and no script that handles it, so pressing Send does nothing useful."
		return v
	case strings.HasPrefix(strings.ToLower(f.Action), "mailto:"):
		v.suspicious = append(v.suspicious, "it opens the visitor's email program instead of sending")
	}
	if hp, ok := p.lookup(f.ActionURL); ok && f.Action != "" {
		v.evidence["action_url"], v.evidence["action_status"] = hp.URL, hp.Status
		if hp.Error != "" {
			v.evidence["action_error"] = hp.Error
		}
		switch {
		case hp.DNSError:
			v.broken = true
			v.reason = fmt.Sprintf("The contact form sends to %s, a server that no longer exists.", hp.URL)
			return v
		case hp.Status == http.StatusNotFound || hp.Status == http.StatusGone:
			v.broken = true
			v.reason = fmt.Sprintf("The contact form sends to %s, which returns \"page not found\" (HTTP %d).", hp.URL, hp.Status)
			return v
		case hp.Error != "":
			v.suspicious = append(v.suspicious, "its address could not be reached")
		case hp.Status >= 500:
			v.suspicious = append(v.suspicious, fmt.Sprintf("its address answers with a server error (HTTP %d)", hp.Status))
		}
	}
	v.suspicious = append(v.suspicious, pageSignals...)
	return v
}

func (p *Probes) lookup(u string) (HTTPProbe, bool) {
	if p == nil {
		return HTTPProbe{}, false
	}
	hp, ok := p.FormActions[u]
	return hp, ok
}

func suspiciousPageSignals(page *capture.Page) []string {
	var out []string
	if n := len(page.Console); n > 0 {
		out = append(out, fmt.Sprintf("the page logs %d script errors", n))
	}
	for _, r := range page.Requests {
		if r.Type == "Script" && (r.Failed || r.Status >= 400) {
			out = append(out, "a script on the page fails to load")
			break
		}
	}
	low := strings.ToLower(page.HTML)
	for _, r := range page.Requests {
		low += " " + strings.ToLower(r.URL)
	}
	if strings.Contains(low, "recaptcha/api/challenge") || strings.Contains(low, "recaptcha_ajax.js") {
		out = append(out, "it uses reCAPTCHA v1, which Google shut down in 2018")
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
