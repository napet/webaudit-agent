package capture

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/safehttp"
	"github.com/napet/webaudit-agent/internal/testsite"
)

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

func locale(t *testing.T) *config.Locale {
	t.Helper()
	l, err := config.LoadLocale("../../config", "hu")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func pngSize(t *testing.T, b []byte) (int, int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func TestCaptureModernSite(t *testing.T) {
	needChrome(t)
	site := testsite.Start("modern")
	defer site.Close()

	c := &Chrome{Locale: locale(t), HTTP: safehttp.New(safehttp.Options{})}
	res, err := c.Capture(context.Background(), site.URL)
	if err != nil {
		t.Fatal(err)
	}
	h := res.Home
	if h.Status != 200 || !strings.HasPrefix(h.FinalURL, site.URL) || h.LoadError != "" || h.TimedOut {
		t.Fatalf("home: status=%d final=%q err=%q timedOut=%v", h.Status, h.FinalURL, h.LoadError, h.TimedOut)
	}
	if !strings.Contains(h.HTML, "Mosolyát ránk bízhatja") {
		t.Error("rendered HTML missing hero text")
	}
	if len(h.Requests) == 0 || h.LoadMS <= 0 {
		t.Errorf("requests=%d loadMS=%d", len(h.Requests), h.LoadMS)
	}
	if !containsSub(h.Console, "deliberate console error") {
		t.Errorf("console errors not captured: %v", h.Console)
	}

	// 3.2: viewport-only screenshots at the two sizes.
	if w, hh := pngSize(t, res.DesktopPNG); w != DesktopWidth || hh != DesktopHeight {
		t.Errorf("desktop screenshot %dx%d", w, hh)
	}
	if w, hh := pngSize(t, res.MobilePNG); w != MobileWidth || hh != MobileHeight {
		t.Errorf("mobile screenshot %dx%d", w, hh)
	}
	if res.Mobile.Overflow || res.Mobile.ViewportWidth != MobileWidth {
		t.Errorf("mobile = %+v", res.Mobile)
	}

	// 3.3 summary, 3.4 discovery by link.
	if !strings.Contains(res.Summary, "Title: Kovács Fogászat") {
		t.Errorf("summary: %s", res.Summary)
	}
	if res.ContactSource != "link" || !strings.HasSuffix(res.ContactURL, "/kapcsolat") || res.Contact == nil {
		t.Fatalf("contact = %q via %q", res.ContactURL, res.ContactSource)
	}
	if len(res.Contact.Forms) != 1 || res.Contact.Forms[0].Method != "post" || !res.Contact.Forms[0].Textarea {
		t.Errorf("contact forms = %+v", res.Contact.Forms)
	}
	if res.Legal == nil || !strings.Contains(res.Legal.HTML, "12345678-2-41") {
		t.Error("legal page not captured")
	}
	if len(res.Axe) == 0 {
		t.Errorf("axe produced nothing: %s", res.AxeError)
	}
	if p := site.Posts(); len(p) != 0 {
		t.Errorf("site received non-GET requests: %v", p)
	}
}

func TestCaptureFormHandlers(t *testing.T) {
	needChrome(t)
	for _, tc := range []struct {
		site    string
		handler []bool
	}{
		{"brokenform", []bool{false, false}},
		{"tracking", []bool{true}},
	} {
		site := testsite.Start(tc.site)
		c := &Chrome{Locale: locale(t), HTTP: safehttp.New(safehttp.Options{})}
		res, err := c.Capture(context.Background(), site.URL)
		site.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.Contact == nil || len(res.Contact.Forms) != len(tc.handler) {
			t.Fatalf("%s: contact forms = %+v", tc.site, res.Contact)
		}
		for i, want := range tc.handler {
			if got := res.Contact.Forms[i].HasHandler; got != want {
				t.Errorf("%s form %d: HasHandler=%v want %v", tc.site, i, got, want)
			}
		}
		if tc.site == "tracking" && !hasCookie(res.Home.Cookies, "_ga") {
			t.Errorf("tracking cookies not recorded: %+v", res.Home.Cookies)
		}
	}
}

func TestCaptureNeverSettles(t *testing.T) {
	needChrome(t)
	site := testsite.Start("hang")
	defer site.Close()
	c := &Chrome{LoadTimeout: 2 * time.Second}
	start := time.Now()
	res, err := c.Capture(context.Background(), site.URL)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 60*time.Second {
		t.Fatalf("capture took %v", took)
	}
	if !res.Home.TimedOut || !strings.Contains(res.Home.HTML, "sosem tölt be") {
		t.Errorf("timedOut=%v html=%d bytes", res.Home.TimedOut, len(res.Home.HTML))
	}
}

func TestCaptureMobileOverflow(t *testing.T) {
	needChrome(t)
	site := testsite.Start("noviewport")
	defer site.Close()
	res, err := (&Chrome{}).Capture(context.Background(), site.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Mobile.Overflow {
		t.Errorf("mobile = %+v, want overflow", res.Mobile)
	}
}

// A carousel's hidden slides are not overflow; a quote form sent from a plain
// button is a contact form with a handler; a privacy notice stands in for a
// missing impresszum.
func TestCaptureCarouselSite(t *testing.T) {
	needChrome(t)
	site := testsite.Start("carousel")
	defer site.Close()
	res, err := (&Chrome{Locale: locale(t), HTTP: safehttp.New(safehttp.Options{})}).Capture(context.Background(), site.URL)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mobile.WidestRight > MobileWidth+1 {
		t.Errorf("mobile = %+v, want no overflow from hidden slides", res.Mobile)
	}
	if !strings.HasSuffix(res.ContactURL, "/ajanlat.html") || res.Contact == nil || len(res.Contact.Forms) != 1 {
		t.Fatalf("contact %q: %+v", res.ContactURL, res.Contact)
	}
	if f := res.Contact.Forms[0]; f.Search || !f.HasHandler || !f.Textarea {
		t.Errorf("quote form = %+v, want a non-search form with a handler", f)
	}
	if !strings.HasSuffix(res.LegalURL, "/adatkezelesi-tajekoztato") || res.LegalSource != "fallback_link" {
		t.Errorf("legal page %q via %q", res.LegalURL, res.LegalSource)
	}
}

// The banner's buttons set a cookie when clicked; the tool must leave it alone.
func TestCaptureLeavesConsentBanner(t *testing.T) {
	needChrome(t)
	site := testsite.Start("consent")
	defer site.Close()
	res, err := (&Chrome{Locale: locale(t), HTTP: safehttp.New(safehttp.Options{})}).Capture(context.Background(), site.URL)
	if err != nil {
		t.Fatal(err)
	}
	if hasCookie(res.Home.Cookies, "consent") {
		t.Errorf("consent cookie set, so the banner was clicked: %+v", res.Home.Cookies)
	}
	if !strings.Contains(res.Home.HTML, `id="cookie-banner"`) {
		t.Error("banner missing from the rendered page")
	}
}

func TestCaptureUnreachable(t *testing.T) {
	needChrome(t)
	res, err := (&Chrome{}).Capture(context.Background(), "http://127.0.0.1:1/")
	if err != nil {
		t.Fatal(err)
	}
	if res.Home.Loaded() || res.Home.LoadError == "" {
		t.Errorf("home = %+v", res.Home)
	}
}

func TestSummaryLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("<html><head><title>Big</title></head><body><nav>")
	for i := 0; i < 500; i++ {
		b.WriteString(`<a href="/p">Menüpont hosszú szöveggel ` + strings.Repeat("x", i%20) + `</a>`)
	}
	b.WriteString("</nav><footer>" + strings.Repeat("lábléc ", 2000) + "</footer>")
	for i := 0; i < 200; i++ {
		b.WriteString("<h2>Címsor</h2>")
	}
	b.WriteString("</body></html>")
	s := Summarize(b.String(), SummaryLimit)
	if len(s) > SummaryLimit+len("…") {
		t.Errorf("summary is %d bytes", len(s))
	}
	if !strings.HasPrefix(s, "Title: Big") || !strings.Contains(s, "Navigation:") {
		t.Errorf("summary missing parts: %.200s", s)
	}
}

func TestDiscover(t *testing.T) {
	l := locale(t)
	client := safehttp.New(safehttp.Options{})

	t.Run("link on homepage", func(t *testing.T) {
		home := &Page{FinalURL: "https://example.hu/", HTML: `<a href="/rolunk">Rólunk</a><a href="/kapcsolat-oldal">Kapcsolat</a>`}
		u, src, _ := Discover(context.Background(), client, home, l.Contact)
		if u != "https://example.hu/kapcsolat-oldal" || src != "link" {
			t.Errorf("got %q via %q", u, src)
		}
	})
	t.Run("button navigation", func(t *testing.T) {
		home := &Page{FinalURL: "https://example.hu/", HTML: `<button onclick="closeDialog()">x</button>` +
			`<button onclick="window.location.href='/rolam'">Rólam</button>` +
			`<button class="btn" onclick="location.assign('/jelentkezes')">Jelentkezés</button>`}
		u, src, _ := Discover(context.Background(), client, home, l.Contact)
		if u != "https://example.hu/jelentkezes" || src != "link" {
			t.Errorf("got %q via %q", u, src)
		}
		legal := &Page{FinalURL: "https://example.hu/", HTML: `<button onclick="window.location.href = 'https://www.example.hu/impresszum'">Impresszum</button>`}
		if u, _, _ := Discover(context.Background(), client, legal, l.LegalPage); u != "https://www.example.hu/impresszum" {
			t.Errorf("legal page: got %q", u)
		}
	})
	t.Run("guessed path", func(t *testing.T) {
		site := testsite.Start("guess")
		defer site.Close()
		home := &Page{FinalURL: site.URL + "/", HTML: readFixture(t, "guess/index.html")}
		u, src, tried := Discover(context.Background(), client, home, l.Contact)
		if !strings.HasSuffix(u, "/elerhetoseg") || src != "path" {
			t.Errorf("got %q via %q (tried %v)", u, src, tried)
		}
		if tried["/kapcsolat"] != http.StatusNotFound {
			t.Errorf("tried = %v", tried)
		}
	})
	t.Run("not found", func(t *testing.T) {
		site := testsite.Start("hang") // has none of the paths
		defer site.Close()
		home := &Page{FinalURL: site.URL + "/", HTML: "<p>nothing</p>"}
		u, _, _ := Discover(context.Background(), client, home, l.Contact)
		if u != "" {
			t.Errorf("got %q, want none", u)
		}
	})
	t.Run("keyword order wins over page order", func(t *testing.T) {
		html := `<a href="/adatkezeles">Adatkezelés</a><a href="/aszf">ÁSZF</a><a href="/impresszum">Impresszum</a>`
		if u := FindLink("https://example.hu/", html, l.LegalPage.LinkKeywords); u != "https://example.hu/impresszum" {
			t.Errorf("legal link: got %q", u)
		}
		if u := FindLink("https://example.hu/", html, l.LegalPage.FallbackLinkKeywords); u != "https://example.hu/aszf" {
			t.Errorf("fallback link: got %q", u)
		}
	})
	t.Run("other site ignored", func(t *testing.T) {
		home := &Page{FinalURL: "https://example.hu/", HTML: `<a href="https://facebook.com/kapcsolat">Kapcsolat</a>`}
		if u := FindLink(home.FinalURL, home.HTML, l.Contact.LinkKeywords); u != "" {
			t.Errorf("got %q", u)
		}
	})
}

func readFixture(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(testsite.Dir() + "/" + rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func containsSub(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func hasCookie(cs []Cookie, name string) bool {
	for _, c := range cs {
		if c.Name == name {
			return true
		}
	}
	return false
}

func TestSessionRendersPagesInOneBrowser(t *testing.T) {
	needChrome(t)
	site := testsite.Start("brokenform")
	defer site.Close()

	c := &Chrome{}
	s, err := c.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	home, homeShot, err := s.Render(context.Background(), site.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	contact, shot, err := s.Render(context.Background(), site.URL+"/kapcsolat")
	if err != nil {
		t.Fatal(err)
	}
	if home.Status != 200 || len(homeShot.Desktop) == 0 {
		t.Errorf("home: status=%d desktop=%d bytes", home.Status, len(homeShot.Desktop))
	}
	if contact.Status != 200 || len(contact.Forms) != 2 || contact.Forms[0].Action != "/contact.php" {
		t.Fatalf("contact: status=%d forms=%+v", contact.Status, contact.Forms)
	}
	if w, h := pngSize(t, shot.Desktop); w != DesktopWidth || h != DesktopHeight {
		t.Errorf("desktop screenshot %dx%d", w, h)
	}
	if w, h := pngSize(t, shot.Mobile); w != MobileWidth || h != MobileHeight {
		t.Errorf("mobile screenshot %dx%d", w, h)
	}
	if posts := site.Posts(); len(posts) > 0 {
		t.Errorf("session sent non-GET requests: %v", posts)
	}

	missing, missingShot, err := s.Render(context.Background(), site.URL+"/nincs-ilyen")
	if err != nil {
		t.Fatal(err)
	}
	if missing.Status != 404 || len(missingShot.Mobile) != 0 {
		t.Errorf("missing page: status=%d mobile=%d bytes (no phone render for a failed page)", missing.Status, len(missingShot.Mobile))
	}
}

// The agentsite fixture hides its quote form and company details behind
// links no baseline rule matches; the agent tests rely on the baseline missing both.
func TestDiscoverMissesAgentsitePages(t *testing.T) {
	l := locale(t)
	site := testsite.Start("agentsite")
	defer site.Close()
	client := safehttp.New(safehttp.Options{})
	home := &Page{FinalURL: site.URL + "/", HTML: readFixture(t, "agentsite/index.html")}

	if u, src, tried := Discover(context.Background(), client, home, l.Contact); u != "" {
		t.Errorf("contact: found %q via %q (tried %v), want none", u, src, tried)
	}
	if u, src, tried := Discover(context.Background(), client, home, l.LegalPage); u != "" {
		t.Errorf("legal page: found %q via %q (tried %v), want none", u, src, tried)
	}
	// Both pages exist and are linked, so an agent can reach them.
	for _, path := range []string{"/arkalkulator", "/rolunk/ceginfo"} {
		resp, err := client.Get(site.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(home.HTML, `href="`+path+`"`) {
			t.Errorf("%s: status %d, linked from home: %v", path, resp.StatusCode, strings.Contains(home.HTML, `href="`+path+`"`))
		}
	}
}

func TestAgentsiteQuoteFormHasHandler(t *testing.T) {
	needChrome(t)
	site := testsite.Start("agentsite")
	defer site.Close()
	s, err := (&Chrome{}).Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _, err := s.Render(context.Background(), site.URL+"/arkalkulator")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Forms) != 1 {
		t.Fatalf("forms = %+v", p.Forms)
	}
	if f := p.Forms[0]; f.Action != "" || !f.HasHandler || !f.Textarea || f.Search {
		t.Errorf("quote form = %+v, want a script-sent message form", f)
	}
}
