package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/napet/webaudit-agent/internal/assess"
	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/checks"
	"github.com/napet/webaudit-agent/internal/safehttp"
	"github.com/napet/webaudit-agent/internal/testsite"
)

func inspect(t *testing.T, tools *Tools, url string) ToolOutcome {
	t.Helper()
	in, _ := json.Marshal(map[string]string{"url": url, "reason": "test"})
	return tools.Execute(context.Background(), ToolInspectPage, in)
}

func screenshot(t *testing.T, tools *Tools, url, device string) ToolOutcome {
	t.Helper()
	in, _ := json.Marshal(map[string]string{"url": url, "device": device, "reason": "test"})
	return tools.Execute(context.Background(), ToolViewScreenshot, in)
}

func wantText(t *testing.T, out ToolOutcome, isError bool, parts ...string) {
	t.Helper()
	if out.IsError != isError {
		t.Errorf("IsError = %v, want %v; text:\n%s", out.IsError, isError, out.Text)
	}
	if len(out.Content) == 0 || out.Content[0].OfText == nil || out.Content[0].OfText.Text != out.Text {
		t.Errorf("first content block should be the result text")
	}
	for _, p := range append(parts, "Budget left:") {
		if !strings.Contains(out.Text, p) {
			t.Errorf("result does not contain %q; text:\n%s", p, out.Text)
		}
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"https://Zoldkert.TEST/arak/#top": "https://zoldkert.test/arak",
		"https://zoldkert.test":           "https://zoldkert.test/",
		"https://zoldkert.test/":          "https://zoldkert.test/",
		"http://zoldkert.test/a/?q=1":     "http://zoldkert.test/a?q=1",
		"/arak":                           "",
		"mailto:info@zoldkert.test":       "",
		"tel:+3630":                       "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPageLinks(t *testing.T) {
	c, _ := agentsite(t)
	var got []string
	for _, l := range PageLinks(&c.Home) {
		got = append(got, l.Text+" "+l.URL)
	}
	want := []string{
		"Szolgáltatások " + siteURL + "/szolgaltatasok",
		"Árak " + siteURL + "/arak",
		"Rólunk " + siteURL + "/rolunk",
		"Kérjen árat " + siteURL + "/arkalkulator",
		"Céginformáció " + siteURL + "/rolunk/ceginfo",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("links:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Blocks: each district link sits in its own span, so its block is the
	// div around all 40; the footer links share another block.
	blocks := map[string]string{}
	for _, l := range PageLinks(linkblocksHome(t)) {
		blocks[strings.TrimPrefix(l.URL, linkblocksURL)] = l.Block
	}
	district, footer := blocks["/01-kerulet"], blocks["/kapcsolat"]
	if district == "" || footer == "" || district == footer {
		t.Fatalf("district block %q, footer block %q; want two different blocks", district, footer)
	}
	for path, want := range map[string]string{"/23-kerulet": district, "/telepules-q": district, "/feltetelek": footer, "/adatkezeles": footer} {
		if blocks[path] != want {
			t.Errorf("block of %s = %q, want %q", path, blocks[path], want)
		}
	}
}

const linkblocksURL = "https://gyorskontener.test"

// linkblocksHome is the homepage of the linkblocks fixture: 40 area links in
// one block, then a small block with the useful links.
func linkblocksHome(t *testing.T) *capture.Page {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testsite.Dir(), "linkblocks", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	return &capture.Page{RequestedURL: linkblocksURL + "/", FinalURL: linkblocksURL + "/", Status: 200, HTML: string(b)}
}

func TestKeywordLink(t *testing.T) {
	k := newKeywords(huLocale(t))
	for _, c := range []struct {
		text, path string
		want       bool
	}{
		{"Kapcsolat", "/x", true},                        // link text
		{"ÍRJON NEKÜNK!", "/x", true},                    // text, any case
		{"Adatvédelmi Irányelvek", "/adatkezeles", true}, // path slug of "adatkezelés"
		{"", "/elerhetoseg", true},                       // slug of "elérhetőség"
		{"Írjon", "/irjon-nekunk", true},                 // slug with a hyphen for the space
		{"Feltételek", "/feltetelek", false},
		{"3. kerület", "/03-kerulet", false},
	} {
		l := Link{URL: linkblocksURL + c.path, Text: c.text}
		if got := k.match(l); got != c.want {
			t.Errorf("match(%q, %s) = %v, want %v", c.text, c.path, got, c.want)
		}
	}
	if (keywords{}).match(Link{URL: linkblocksURL + "/kapcsolat", Text: "Kapcsolat"}) {
		t.Error("no locale should match nothing")
	}
}

// Spec: Useful links behind a long link block, Shortened link still allowed.
func TestLinkOrder(t *testing.T) {
	district := linkblocksURL + "/04-kerulet"
	s := &fakeSession{pages: map[string]fakePage{Normalize(district): {page: capture.Page{
		FinalURL: district, Status: 200, HTML: `<title>4. kerület</title><div><a href="/">Kezdőlap</a> <a href="/kapcsolat">Kapcsolat</a></div>`}}}}
	tools := NewTools(ToolsConfig{Session: s, Capture: &capture.Result{Home: *linkblocksHome(t)}, Locale: huLocale(t), Budgets: testBudgets})
	content := FirstMessage(assess.Input{}, tools).Content
	text := content[len(content)-1].OfText.Text // the closing text with the link list
	want := "Links on these pages not inspected yet:\n" +
		"- Kapcsolat: " + linkblocksURL + "/kapcsolat\n" +
		"- Adatvédelmi Irányelvek: " + linkblocksURL + "/adatkezeles\n" +
		"- 1. kerület: " + linkblocksURL + "/01-kerulet\n" +
		"- 2. kerület: " + linkblocksURL + "/02-kerulet\n" +
		"- 3. kerület: " + linkblocksURL + "/03-kerulet\n" +
		"  (and 37 more links like these)\n" +
		"- Oldaltérkép: " + linkblocksURL + "/oldalterkep\n" +
		"- Feltételek: " + linkblocksURL + "/feltetelek\n" +
		"\nBudget:"
	if !strings.Contains(text, want) {
		t.Errorf("first message links:\n%s\nwant:\n%s", text, want)
	}
	if strings.Contains(text, "more, listed when") {
		t.Error("nothing should be left out by the cap")
	}

	// A shortened district page can still be opened, and costs a page as usual.
	out := inspect(t, tools, district)
	wantText(t, out, false, "Inspection 1: "+district, "2 of 3 pages")
}

// Spec: Site-wide links behind many small blocks.
func TestRepeatedLinks(t *testing.T) {
	var menu, articles strings.Builder
	menu.WriteString("<div>")
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&menu, `<a href="/menu-%d">Menü %d</a> `, i, i)
	}
	menu.WriteString("</div>")
	for i := 1; i <= 35; i++ { // a block of its own each: title and "more" link
		fmt.Fprintf(&articles, `<div><a href="/cikk-%d">Cikk %d</a> <a href="/cikk-%d">tovább</a></div>`, i, i, i)
	}
	footer := `<div><a href="/feltetelek">Feltételek</a> <a href="/adatkezeles">Adatvédelmi Irányelvek</a></div>`
	home := capture.Page{FinalURL: linkblocksURL + "/", Status: 200, HTML: menu.String() + articles.String() + footer}
	contact := &capture.Page{FinalURL: linkblocksURL + "/kapcsolat", Status: 200, HTML: menu.String() + "<p>Írjon nekünk</p>" + footer}
	tools := NewTools(ToolsConfig{Capture: &capture.Result{Home: home, Contact: contact}, Locale: huLocale(t), Budgets: testBudgets})
	content := FirstMessage(assess.Input{}, tools).Content
	text := content[len(content)-1].OfText.Text
	want := "Links on these pages not inspected yet:\n" +
		"- Adatvédelmi Irányelvek: " + linkblocksURL + "/adatkezeles\n" +
		"- Menü 1: " + linkblocksURL + "/menu-1\n" +
		"- Menü 2: " + linkblocksURL + "/menu-2\n" +
		"- Menü 3: " + linkblocksURL + "/menu-3\n" +
		"  (and 7 more links like these)\n" +
		"- Feltételek: " + linkblocksURL + "/feltetelek\n" +
		"- Cikk 1: " + linkblocksURL + "/cikk-1\n"
	if !strings.Contains(text, want) {
		t.Errorf("first message links:\n%s\nwant it to contain:\n%s", text, want)
	}
	// 5 links above (the count line takes no slot) and 25 articles fill the 30.
	if !strings.Contains(text, "- Cikk 25: ") || strings.Contains(text, "- Cikk 26: ") || !strings.Contains(text, "(and 10 more, listed when") {
		t.Errorf("want articles 1-25 listed and 10 left out by the cap:\n%s", text)
	}
}

// Spec: Invented URL.
func TestInspectInventedURL(t *testing.T) {
	tools, s := newTestTools(t)
	out := inspect(t, tools, "/impresszum")
	wantText(t, out, true, "not a link on any page inspected so far", "3 of 3 pages")
	if len(s.rendered) != 0 || len(tools.Visits()) != 0 {
		t.Errorf("rendered %v, visits %d; want no request and no budget used", s.rendered, len(tools.Visits()))
	}
}

// Spec: Link to another domain.
func TestInspectOtherDomain(t *testing.T) {
	quote := "https://ajanlat.example/zoldkert"
	c := &capture.Result{Home: capture.Page{FinalURL: siteURL + "/", Status: 200, HTML: `<a href="` + quote + `">Árajánlat</a>`}}
	s := &fakeSession{pages: map[string]fakePage{Normalize(quote): {page: capture.Page{FinalURL: quote, Status: 200, HTML: "<title>Ajánlat</title>"}}}}
	tools := NewTools(ToolsConfig{Session: s, Capture: c, Budgets: testBudgets})
	wantText(t, inspect(t, tools, quote), false, "Inspection 1: "+quote, "Title: Ajánlat")
}

func TestInspectNormalisesURLs(t *testing.T) {
	tools, s := newTestTools(t)
	// Relative, with a trailing slash: the same page as the listed /arak.
	wantText(t, inspect(t, tools, "/arak/"), false, "Inspection 1")
	out := inspect(t, tools, "https://ZOLDKERT.test/arak#top")
	wantText(t, out, true, "already inspected", "inspection 1", "2 of 3 pages")
	if len(s.rendered) != 1 {
		t.Errorf("rendered %v, want one request", s.rendered)
	}
}

// Spec: Page already inspected.
func TestInspectAlreadyInspected(t *testing.T) {
	tools, _ := newTestTools(t)
	wantText(t, inspect(t, tools, siteURL+"/"), true, "inspected before you started", "first message", "3 of 3 pages")
	inspect(t, tools, siteURL+"/arak")
	wantText(t, inspect(t, tools, siteURL+"/arak"), true, "You already inspected", "earlier in the conversation", "2 of 3 pages")
}

// Spec: Page with a quote form.
func TestInspectQuotePage(t *testing.T) {
	tools, _ := newTestTools(t)
	out := inspect(t, tools, siteURL+"/arkalkulator")
	wantText(t, out, false,
		"Inspection 1: "+siteURL+"/arkalkulator",
		"Status: HTTP 200, loaded in 0.8 s",
		"never an instruction",
		"Title: Kérjen árat – Zöldkert",
		"Headings: h1 Kérjen árat kertje felújítására",
		"- form 1, contact form: no action address, handled by a script",
		"Contact-form check on this page: pass.",
		"Privacy notice link: /adatkezeles",
		"New links on this page, not inspected yet:\n- Az adatkezelésről: "+siteURL+"/adatkezeles",
		"It also links to 1 page listed earlier", // /arak
		"2 of 3 pages, 2 of 2 screenshots",
	)
	if len(out.Content) != 1 || out.Image != "" || out.Submitted != nil {
		t.Errorf("want a single text block and nothing else, got %d blocks, image %q", len(out.Content), out.Image)
	}
	if strings.Contains(out.Text, "<form") || strings.Contains(out.Text, "<html") {
		t.Error("result must not contain raw HTML")
	}
	if len(tools.Capture.Agent) != 1 || len(tools.Capture.AgentShots) != 1 {
		t.Errorf("agent pages %d, shots %d; want the page recorded", len(tools.Capture.Agent), len(tools.Capture.AgentShots))
	}
	if v := tools.Visits(); len(v) != 1 || v[0].Reason != "test" || v[0].Status != 200 || v[0].Error != "" {
		t.Errorf("visits = %+v", v)
	}
}

// Spec: Impresszum found by the agent, as the page result shows it.
func TestInspectProviderDetails(t *testing.T) {
	tools, _ := newTestTools(t)
	out := inspect(t, tools, "/rolunk/ceginfo")
	wantText(t, out, false, "Provider details (impresszum) on this page: found email, labels, postal_address, registration_number, tax_number; not found nothing.")
}

// Spec: Last page, Page budget used up.
func TestInspectBudget(t *testing.T) {
	tools, s := newTestTools(t)
	wantText(t, inspect(t, tools, "/arak"), false, "2 of 3 pages")
	wantText(t, inspect(t, tools, "/rolunk"), false, "1 of 3 pages", "One page left", "submit_assessment")
	wantText(t, inspect(t, tools, "/szolgaltatasok"), false, "0 of 3 pages", "No pages left")
	out := inspect(t, tools, "/rolunk/ceginfo")
	wantText(t, out, true, "page budget is used up", "Call submit_assessment now")
	if len(s.rendered) != 3 {
		t.Errorf("rendered %v, want no request after the budget ran out", s.rendered)
	}
}

// Spec: Page fails to load.
func TestInspectLoadFailures(t *testing.T) {
	tools, s := newTestTools(t)
	s.pages[Normalize(siteURL+"/arak")] = fakePage{page: capture.Page{FinalURL: siteURL + "/arak", Status: 200, TimedOut: true}}
	s.pages[Normalize(siteURL+"/rolunk")] = fakePage{err: errors.New("browser closed")}

	wantText(t, inspect(t, tools, "/arak"), true, "did not finish loading in time", "One page of budget was used", "Choose another page", "2 of 3 pages")
	wantText(t, inspect(t, tools, "/rolunk"), true, "could not be opened: browser closed", "1 of 3 pages")
	// Neither page counts as inspected for screenshots, and neither is tried again.
	wantText(t, screenshot(t, tools, "/arak", "desktop"), true, "did not load", "2 of 2 screenshots")
	wantText(t, inspect(t, tools, "/rolunk"), true, "You already inspected")

	if len(tools.Capture.Agent) != 1 {
		t.Errorf("agent pages = %d; the timed-out page is recorded, the render error has none", len(tools.Capture.Agent))
	}
	if v := tools.Visits(); len(v) != 2 || v[0].Error == "" || v[1].Error == "" || v[1].Agent != -1 {
		t.Errorf("visits = %+v", v)
	}
}

func TestInspectNotFound(t *testing.T) {
	tools, _ := newTestTools(t)
	inspect(t, tools, "/rolunk")
	wantText(t, inspect(t, tools, "/referenciak"), true, "answered with HTTP 404", "1 of 3 pages")
}

func TestInspectRedirectToKnownPage(t *testing.T) {
	tools, s := newTestTools(t)
	s.pages[Normalize(siteURL+"/arak")] = fakePage{page: capture.Page{FinalURL: siteURL + "/", Status: 200, HTML: fixtureHTML(t, "index.html")}}
	wantText(t, inspect(t, tools, "/arak"), true, "redirected to "+siteURL+"/", "in the first message", "2 of 3 pages")
}

// Spec: Form on an inspected page. The probe uses HEAD/GET only.
func TestInspectProbesFormActions(t *testing.T) {
	site := testsite.Start("agentsite")
	defer site.Close()
	quote := site.URL + "/quote"
	c := &capture.Result{Home: capture.Page{FinalURL: site.URL + "/", Status: 200, HTML: `<a href="/quote">Ajánlat</a>`}}
	form := capture.Form{Index: 0, Action: "/send.php", ActionURL: site.URL + "/send.php", Method: "post", Textarea: true}
	s := &fakeSession{pages: map[string]fakePage{Normalize(quote): {page: capture.Page{FinalURL: quote, Status: 200, HTML: "<form></form>", Forms: []capture.Form{form}}}}}
	probes := &checks.Probes{}
	tools := NewTools(ToolsConfig{Session: s, Capture: c, Probes: probes, HTTP: safehttp.New(safehttp.Options{}), Budgets: testBudgets})

	wantText(t, inspect(t, tools, "/quote"), false, "- form 1, contact form: POST to /send.php", "Contact-form check on this page: fail.", "page not found")
	if hp := probes.FormActions[site.URL+"/send.php"]; hp.Status != 404 {
		t.Errorf("probe = %+v, want a recorded 404", hp)
	}
	if got := site.Requests(); len(got) != 1 || got[0] != "HEAD /send.php" {
		t.Errorf("requests = %v, want only HEAD /send.php", got)
	}
}

// Spec: Screenshot before inspection, plus the screenshot budget.
func TestViewScreenshot(t *testing.T) {
	tools, _ := newTestTools(t)
	wantText(t, screenshot(t, tools, "/arkalkulator", "desktop"), true, "have not inspected", "inspect_page", "2 of 2 screenshots")
	wantText(t, screenshot(t, tools, "/", "phone"), true, "homepage screenshots are in the first message", "2 of 2 screenshots")
	wantText(t, screenshot(t, tools, "/arkalkulator", "tablet"), true, "device must be desktop or phone")

	inspect(t, tools, "/arkalkulator")
	out := screenshot(t, tools, "/arkalkulator", "desktop")
	wantText(t, out, false, "Desktop screenshot of "+siteURL+"/arkalkulator (1280×800", "1 of 2 screenshots")
	if len(out.Content) != 2 || out.Content[1].OfImage == nil {
		t.Fatalf("want text then image, got %d blocks", len(out.Content))
	}
	if got := out.Content[1].OfImage.Source.OfBase64.Data; got != base64.StdEncoding.EncodeToString([]byte("desktop/arkalkulator")) {
		t.Errorf("image data = %q", got)
	}
	if out.Image != "pages/01/desktop.png" {
		t.Errorf("Image = %q", out.Image)
	}

	wantText(t, screenshot(t, tools, siteURL+"/arkalkulator/", "desktop"), true, "already seen this screenshot", "1 of 2 screenshots")
	out = screenshot(t, tools, "/arkalkulator", "phone")
	wantText(t, out, false, "Phone screenshot", "390×844", "0 of 2 screenshots")
	if out.Image != "pages/01/mobile.png" {
		t.Errorf("Image = %q", out.Image)
	}

	inspect(t, tools, "/arak")
	wantText(t, screenshot(t, tools, "/arak", "desktop"), true, "screenshot budget is used up")
	if tools.ScreenshotsUsed() != 2 {
		t.Errorf("screenshots used = %d", tools.ScreenshotsUsed())
	}
}

// Spec: Invalid assessment, Assessment submitted.
func TestSubmit(t *testing.T) {
	tools, _ := newTestTools(t)
	bad := json.RawMessage(`{"rating":7,"confidence":"high","reasons":[{"subject":"hero","detail":"Old."}]}`)
	out := tools.Execute(context.Background(), ToolSubmit, bad)
	wantText(t, out, true, "The assessment is invalid: rating 7 out of range", "call submit_assessment again")
	if out.Submitted != nil {
		t.Error("an invalid assessment must not be submitted")
	}

	// The placeholder a live run submitted gets an error the model can act on.
	placeholder := json.RawMessage(`{"rating":2,"confidence":"medium","reasons":[{"subject":"x","detail":"placeholder"}]}`)
	out = tools.Execute(context.Background(), ToolSubmit, placeholder)
	wantText(t, out, true, "The assessment is invalid: 1 reason(s) given, at least 2 needed", `names a visible element`, "call submit_assessment again")
	if out.Submitted != nil {
		t.Error("a placeholder assessment must not be submitted")
	}

	good := json.RawMessage(`{"rating":4,"confidence":"medium","reasons":[{"subject":"hero","detail":"Stock photo and bevelled buttons."},{"subject":"typography","detail":"Default serif body text."}]}`)
	out = tools.Execute(context.Background(), ToolSubmit, good)
	wantText(t, out, false, "Assessment received")
	if a := out.Submitted; a == nil || a.Rating != 4 || a.Confidence != "medium" || len(a.Reasons) != 2 || a.RubricVersion != "v1" {
		t.Errorf("submitted = %+v", a)
	}
}

func TestBadToolInput(t *testing.T) {
	tools, _ := newTestTools(t)
	wantText(t, tools.Execute(context.Background(), "click", json.RawMessage(`{}`)), true, `no tool named "click"`, "inspect_page")
	wantText(t, tools.Execute(context.Background(), ToolInspectPage, json.RawMessage(`{"reason":"x"}`)), true, "needs a url")
	wantText(t, tools.Execute(context.Background(), ToolViewScreenshot, json.RawMessage(`[]`)), true, "needs a url")
}

func needChrome(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Chrome test in -short mode")
	}
	for _, name := range []string{"chrome", "google-chrome", "chromium", "chromium-browser"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	for _, p := range []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	} {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}
	t.Skip("Chrome not found")
}

// The tools over a real browser and the agentsite fixture: the quote form
// is found and judged, its screenshot is real, and nothing is posted.
func TestToolsWithChrome(t *testing.T) {
	needChrome(t)
	site := testsite.Start("agentsite")
	defer site.Close()
	s, err := (&capture.Chrome{}).Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	home, _, err := s.Render(context.Background(), site.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	tools := NewTools(ToolsConfig{
		Session: s, Capture: &capture.Result{Home: home}, Probes: &checks.Probes{},
		HTTP: safehttp.New(safehttp.Options{}), Locale: huLocale(t), Budgets: testBudgets,
	})

	wantText(t, inspect(t, tools, "/arkalkulator"), false,
		"contact form: no action address, handled by a script", "Contact-form check on this page: pass.", "Privacy notice link: /adatkezeles")
	wantText(t, inspect(t, tools, "/rolunk/ceginfo"), false, "found email, labels, postal_address, registration_number, tax_number")
	wantText(t, screenshot(t, tools, "/arkalkulator", "phone"), false, "Phone screenshot")
	if posts := site.Posts(); len(posts) != 0 {
		t.Errorf("POST requests: %v", posts)
	}
}

// Spec: Re-run agent offline, at the tool. A page the snapshot did not
// record is refused without using budget, and can be asked about again.
func TestInspectRecordedSession(t *testing.T) {
	c, live := agentsite(t)
	quote := live.pages[Normalize(siteURL+"/arkalkulator")]
	s := NewRecordedSession([]capture.Page{quote.page}, []capture.Shot{quote.shot})
	tools := NewTools(ToolsConfig{Session: s, Capture: c, Locale: huLocale(t), Budgets: testBudgets})

	wantText(t, inspect(t, tools, "/arak"), true, "not available in this replay", "No page budget was used", "3 of 3 pages")
	wantText(t, inspect(t, tools, "/arkalkulator"), false, "Inspection 1: "+siteURL+"/arkalkulator", "2 of 3 pages")
	wantText(t, screenshot(t, tools, "/arkalkulator", "desktop"), false, "Desktop screenshot")
	wantText(t, inspect(t, tools, "/arak"), true, "not available in this replay")
	if len(tools.Visits()) != 1 {
		t.Errorf("visits = %d, want only the recorded page", len(tools.Visits()))
	}
}
