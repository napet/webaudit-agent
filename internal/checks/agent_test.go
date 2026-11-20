package checks

import (
	"context"
	"strings"
	"testing"

	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/report"
	"github.com/napet/webaudit-agent/internal/safehttp"
	"github.com/napet/webaudit-agent/internal/testsite"
)

// agentsite builds the capture of the agentsite fixture as the baseline sees it (no
// contact or legal page found) plus the given agent pages.
func agentsite(t *testing.T, agent ...capture.Page) *capture.Result {
	return &capture.Result{Home: page("https://zoldkert.hu/", fixture(t, "agentsite/index.html")), Agent: agent}
}

func quotePage(t *testing.T, form capture.Form) capture.Page {
	p := page("https://zoldkert.hu/arkalkulator", fixture(t, "agentsite/arkalkulator.html"))
	p.Forms = []capture.Form{form}
	return p
}

func TestContactFormOnAgentPage(t *testing.T) {
	t.Run("form on an agent page", func(t *testing.T) {
		// Spec scenario: the rules found no contact form, and the quote page
		// the agent inspected sends to an action that returns 404.
		action := "https://zoldkert.hu/kuld.php"
		c := agentsite(t, quotePage(t, capture.Form{Action: "/kuld.php", ActionURL: action, Method: "post", Textarea: true}))
		p := validProbes()
		p.FormActions = map[string]HTTPProbe{action: {URL: action, Method: "HEAD", Status: 404}}
		r := contactForm(c, p)
		if r.Status != report.StatusFail || r.Flag != report.FlagBrokenContactForm {
			t.Fatalf("%+v", r)
		}
		if !strings.Contains(r.Explanation, "https://zoldkert.hu/arkalkulator") {
			t.Errorf("explanation should name the page: %q", r.Explanation)
		}
		forms := r.Evidence["forms"].([]map[string]any)
		if len(forms) != 1 || forms[0]["page"] != "https://zoldkert.hu/arkalkulator" {
			t.Errorf("form evidence = %v", forms)
		}
	})
	t.Run("working form found by the agent", func(t *testing.T) {
		c := agentsite(t, quotePage(t, capture.Form{Textarea: true, Email: true, HasHandler: true}))
		if r := contactForm(c, validProbes()); r.Status != report.StatusPass {
			t.Errorf("%+v", r)
		}
		if r := contactForm(agentsite(t), validProbes()); r.Status != report.StatusNotApplicable {
			t.Errorf("without the agent page: %+v", r)
		}
	})
	t.Run("one broken form on any page raises the flag", func(t *testing.T) {
		c := agentsite(t,
			quotePage(t, capture.Form{Textarea: true, HasHandler: true}),
			page("https://zoldkert.hu/visszahivas", "<form><textarea></textarea></form>"))
		c.Agent[1].Forms = []capture.Form{{Textarea: true}} // no action, no handler
		contact := page("https://zoldkert.hu/kapcsolat", "<html></html>")
		contact.Forms = []capture.Form{{Textarea: true, HasHandler: true}}
		c.Contact = &contact
		r := contactForm(c, validProbes())
		if r.Flag != report.FlagBrokenContactForm || !strings.Contains(r.Explanation, "/visszahivas") {
			t.Errorf("%+v", r)
		}
		if got := r.Evidence["agent_pages"].([]string); len(got) != 2 {
			t.Errorf("agent_pages = %v", got)
		}
	})
	t.Run("pages that did not load are ignored", func(t *testing.T) {
		missing := page("https://zoldkert.hu/referenciak", "<form><textarea></textarea></form>")
		missing.Status = 404
		missing.Forms = []capture.Form{{Textarea: true}}
		slow := quotePage(t, capture.Form{Textarea: true})
		slow.TimedOut = true
		if r := contactForm(agentsite(t, missing, slow), validProbes()); r.Status != report.StatusNotApplicable {
			t.Errorf("%+v", r)
		}
	})
	t.Run("default mode evidence unchanged", func(t *testing.T) {
		c := agentsite(t)
		c.Home.Forms = []capture.Form{{Textarea: true, HasHandler: true}}
		r := contactForm(c, validProbes())
		if _, ok := r.Evidence["agent_pages"]; ok {
			t.Errorf("agent_pages in default mode: %v", r.Evidence)
		}
		if _, ok := r.Evidence["forms"].([]map[string]any)[0]["page"]; ok {
			t.Errorf("per-form page in default mode: %v", r.Evidence)
		}
	})
}

func TestPageContactForm(t *testing.T) {
	p := quotePage(t, capture.Form{Textarea: true})
	r := PageContactForm(&p, nil)
	if r.Status != report.StatusFail || r.Evidence["page"] != p.FinalURL {
		t.Errorf("%+v", r)
	}
	search := page("https://zoldkert.hu/kereses", "")
	search.Forms = []capture.Form{{Search: true}}
	if r := PageContactForm(&search, nil); r.Status != report.StatusNotApplicable {
		t.Errorf("search page: %+v", r)
	}
}

func TestLegalChecksOnAgentPages(t *testing.T) {
	l := huLocale(t)
	ceginfo := page("https://zoldkert.hu/rolunk/ceginfo", fixture(t, "agentsite/rolunk/ceginfo.html"))
	quote := quotePage(t, capture.Form{Textarea: true, Email: true, HasHandler: true})

	before := Evaluate(Input{Capture: agentsite(t), Probes: validProbes(), Locale: l, Now: now})
	if r := find(t, before, "legal_provider_details"); r.Status != report.StatusFail {
		t.Fatalf("v1 view should miss the provider details: %+v", r)
	}

	// Spec scenario "Impresszum found by the agent".
	after := Evaluate(Input{Capture: agentsite(t, quote, ceginfo), Probes: validProbes(), Locale: l, Now: now})
	pd := find(t, after, "legal_provider_details")
	if pd.Status != report.StatusPass {
		t.Errorf("provider details with the agent's page: %+v", pd)
	}
	if ps := pd.Evidence["pages_searched"].([]string); len(ps) != 3 || ps[2] != ceginfo.FinalURL {
		t.Errorf("pages searched = %v", ps)
	}
	// The quote form brings the privacy check into play, and its page links the notice.
	pn := find(t, after, "legal_privacy_notice")
	if pn.Status != report.StatusPass || pn.Evidence["on_page"] != quote.FinalURL {
		t.Errorf("privacy notice: %+v", pn)
	}
	for _, g := range after.LegalGaps {
		if g.ID == "legal_provider_details" || g.ID == "legal_privacy_notice" {
			t.Errorf("unexpected gap %+v", g)
		}
	}

	single := PageProviderDetails(&ceginfo, l.Legal.ProviderDetails)
	if single.Status != report.StatusPass || len(single.Evidence["found"].([]string)) < 3 {
		t.Errorf("single page: %+v", single)
	}
	if got := PagePrivacyLink(&quote, l.Legal.PrivacyLinkKeywords); got != "/adatkezeles" {
		t.Errorf("privacy link = %q", got)
	}
}

func TestProbePage(t *testing.T) {
	site := testsite.Start("brokenform")
	defer site.Close()
	client := safehttp.New(safehttp.Options{})
	p := validProbes()

	// An agent page with a POST form whose action is gone, a script-sent
	// form, and a search form.
	quote := page(site.URL+"/ajanlat", "")
	quote.Forms = []capture.Form{
		{Action: "/contact.php", ActionURL: site.URL + "/contact.php", Method: "post", Textarea: true, Email: true},
		{Textarea: true, HasHandler: true},
		{Action: "/kereses", ActionURL: site.URL + "/kereses", Search: true},
	}
	p.ProbePage(context.Background(), client, &quote)
	p.ProbePage(context.Background(), client, &quote) // recorded URLs are not requested again

	if len(p.FormActions) != 1 || p.FormActions[site.URL+"/contact.php"].Status != 404 {
		t.Fatalf("form actions = %+v", p.FormActions)
	}
	if r := PageContactForm(&quote, p); r.Flag != report.FlagBrokenContactForm {
		t.Errorf("%+v", r)
	}
	reqs := site.Requests()
	if len(reqs) != 1 || reqs[0] != "HEAD /contact.php" {
		t.Errorf("requests = %v, want one HEAD of the action", reqs)
	}
	if posts := site.Posts(); len(posts) != 0 {
		t.Errorf("fixture received %v", posts)
	}
}
