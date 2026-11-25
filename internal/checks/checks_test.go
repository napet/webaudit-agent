package checks

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
	"github.com/napet/webaudit-agent/internal/safehttp"
	"github.com/napet/webaudit-agent/internal/testsite"
)

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(testsite.Dir() + "/" + rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func huLocale(t *testing.T) *config.Locale {
	t.Helper()
	l, err := config.LoadLocale("../../config", "hu")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func page(url, html string) capture.Page {
	return capture.Page{RequestedURL: url, FinalURL: url, Status: 200, LoadMS: 800, HTML: html, Doctype: "html"}
}

func find(t *testing.T, out Output, id string) report.CheckResult {
	t.Helper()
	for _, r := range out.Checks {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no check %q in %+v", id, out.Checks)
	return report.CheckResult{}
}

func flags(out Output) []string {
	var f []string
	for _, r := range out.Checks {
		if r.Flag != "" {
			f = append(f, r.Flag)
		}
	}
	return f
}

func validProbes() *Probes {
	return &Probes{
		TLS:       &TLSProbe{Valid: true, NotAfter: now.AddDate(0, 6, 0)},
		PlainHTTP: &HTTPProbe{Status: 301, Location: "https://example.hu/"},
	}
}

// 4.1
func TestErroredChecksRaiseNoFlags(t *testing.T) {
	c := &capture.Result{Home: page("https://example.hu/", fixture(t, "modern/index.html"))}
	out := Evaluate(Input{Capture: c, Probes: validProbes(), Now: now}) // no PSI, no axe
	for _, id := range []string{"pagespeed_performance", "accessibility_axe"} {
		r := find(t, out, id)
		if r.Status != report.StatusError || r.Flag != "" {
			t.Errorf("%s = %+v, want error without flag", id, r)
		}
	}
	if f := flags(out); len(f) != 0 {
		t.Errorf("flags = %v", f)
	}
	if r := result("x", report.CatMobile, report.StatusSuspicious, report.FlagNotMobileUsable, "", nil); r.Flag != "" {
		t.Error("only failing results may carry a flag")
	}
}

// 4.2
func TestHTTPSCheck(t *testing.T) {
	home := &capture.Result{Home: page("https://example.hu/", "<html></html>")}
	cases := map[string]struct {
		p        *Probes
		final    string
		wantFlag bool
		evKey    string
	}{
		"valid":               {validProbes(), "https://example.hu/", false, ""},
		"expired cert":        {&Probes{TLS: &TLSProbe{VerifyError: "x509: certificate has expired", NotAfter: now.AddDate(0, -2, 0)}}, "https://example.hu/", true, "certificate_expires"},
		"no tls at all":       {&Probes{TLS: &TLSProbe{DialError: "connection refused"}}, "http://example.hu/", true, "tls_error"},
		"http not redirected": {&Probes{TLS: &TLSProbe{Valid: true}, PlainHTTP: &HTTPProbe{Status: 200}}, "https://example.hu/", true, "http_status"},
		"redirect to http":    {&Probes{TLS: &TLSProbe{Valid: true}, PlainHTTP: &HTTPProbe{Status: 302, Location: "http://www.example.hu/"}}, "https://example.hu/", true, "http_location"},
		"served over http":    {&Probes{TLS: &TLSProbe{Valid: true}}, "http://example.hu/", true, ""},
	}
	for name, tc := range cases {
		home.Home.FinalURL = tc.final
		r := https(home, tc.p)
		if (r.Flag == report.FlagNoHTTPS) != tc.wantFlag {
			t.Errorf("%s: %+v", name, r)
		}
		if tc.evKey != "" && r.Evidence[tc.evKey] == nil {
			t.Errorf("%s: missing evidence %q in %v", name, tc.evKey, r.Evidence)
		}
	}
}

func selfSigned(t *testing.T, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fixture"},
		NotBefore:             notAfter.AddDate(-1, 0, 0),
		NotAfter:              notAfter,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

func tlsServer(t *testing.T, cert tls.Certificate) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	pool := x509.NewCertPool()
	pool.AddCert(cert.Leaf)
	return srv, pool
}

func TestProbeTLS(t *testing.T) {
	good, goodRoots := tlsServer(t, selfSigned(t, now.AddDate(1, 0, 0)))
	defer good.Close()
	p := Probe(context.Background(), ProbeOptions{HTTP: safehttp.New(safehttp.Options{}), Roots: goodRoots, Now: now}, good.URL, nil)
	if !p.TLS.Valid {
		t.Errorf("valid cert: %+v", p.TLS)
	}

	expiredCert := selfSigned(t, now.AddDate(0, -1, 0))
	bad, badRoots := tlsServer(t, expiredCert)
	defer bad.Close()
	p = Probe(context.Background(), ProbeOptions{HTTP: safehttp.New(safehttp.Options{}), Roots: badRoots, Now: now}, bad.URL, nil)
	if p.TLS.Valid || !strings.Contains(p.TLS.VerifyError, "expired") || !p.TLS.NotAfter.Equal(expiredCert.Leaf.NotAfter) {
		t.Errorf("expired cert: %+v", p.TLS)
	}
	r := https(&capture.Result{Home: page(bad.URL, "")}, p)
	if r.Flag != report.FlagNoHTTPS || r.Evidence["certificate_expires"] != expiredCert.Leaf.NotAfter.UTC().Format(time.DateOnly) {
		t.Errorf("https check on expired cert: %+v", r)
	}
}

// 4.3
func TestSiteErrorOrSlow(t *testing.T) {
	cases := map[string]struct {
		p    capture.Page
		flag bool
	}{
		"ok":          {capture.Page{Status: 200, LoadMS: 2000}, false},
		"slow":        {capture.Page{Status: 200, LoadMS: 12_500}, true},
		"timed out":   {capture.Page{Status: 200, LoadMS: 30_000, TimedOut: true}, true},
		"5xx":         {capture.Page{Status: 503, LoadMS: 300}, true},
		"unreachable": {capture.Page{LoadError: "net::ERR_NAME_NOT_RESOLVED"}, true},
	}
	for name, tc := range cases {
		r := siteErrorOrSlow(&capture.Result{Home: tc.p})
		if (r.Flag == report.FlagSiteErrorOrSlow) != tc.flag {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// An unreachable site skips every check that needs a page.
	out := Evaluate(Input{Capture: &capture.Result{Home: capture.Page{LoadError: "refused"}}, Probes: &Probes{TLS: &TLSProbe{DialError: "refused"}}, Now: now})
	if len(out.Checks) != 2 {
		t.Errorf("unreachable site ran %d checks: %+v", len(out.Checks), out.Checks)
	}
}

// 4.5
func TestMobileUsability(t *testing.T) {
	modern := &capture.Result{Home: page("https://example.hu/", fixture(t, "modern/index.html"))}
	if r := mobileUsability(modern, doc(modern.Home.HTML), nil); r.Status != report.StatusPass {
		t.Errorf("modern: %+v", r)
	}
	nv := &capture.Result{Home: page("https://example.hu/", fixture(t, "noviewport/index.html")),
		Mobile: capture.Mobile{Overflow: true, WidestRight: 1208, ViewportWidth: 390, Widest: "div"}}
	r := mobileUsability(nv, doc(nv.Home.HTML), nil)
	if r.Flag != report.FlagNotMobileUsable || len(r.Evidence["problems"].([]string)) != 2 {
		t.Errorf("noviewport: %+v", r)
	}
	// A few pixels of sideways scroll is noted, not flagged.
	slight := &capture.Result{Home: modern.Home, Mobile: capture.Mobile{WidestRight: 397, ViewportWidth: 390, Widest: "div.box"}}
	if r := mobileUsability(slight, doc(slight.Home.HTML), nil); r.Status != report.StatusSuspicious || r.Flag != "" {
		t.Errorf("397px on 390px: %+v", r)
	}
	slight.Mobile.WidestRight = 390 + slightOverflowPx + 1
	if r := mobileUsability(slight, doc(slight.Home.HTML), nil); r.Flag != report.FlagNotMobileUsable {
		t.Errorf("%dpx on 390px: %+v", slight.Mobile.WidestRight, r)
	}
	// Spec: Initial scale without device width; Fixed viewport width.
	for _, c := range []struct {
		content string // "" for no tag
		pass    bool
	}{
		{"initial-scale=1", true},
		{"initial-scale=1.0", true},
		{"width=device-width,initial-scale=1", true},
		{"Width = Device-Width, Initial-Scale = 1", true},
		{"width=980, initial-scale=1", false},
		{"initial-scale=0.5", false},
		{"", false},
	} {
		html := "<html><head></head><body><p>x</p></body></html>"
		if c.content != "" {
			html = `<html><head><meta name="viewport" content="` + c.content + `"></head><body><p>x</p></body></html>`
		}
		vc := &capture.Result{Home: page("https://example.hu/", html), Mobile: capture.Mobile{WidestRight: 390, ViewportWidth: 390}}
		r := mobileUsability(vc, doc(html), nil)
		if pass := r.Status == report.StatusPass; pass != c.pass || (!pass && r.Flag != report.FlagNotMobileUsable) {
			t.Errorf("viewport %q: %+v, want pass %v", c.content, r, c.pass)
		}
	}

	zero := 0.0
	r = mobileUsability(modern, doc(modern.Home.HTML), &report.PageSpeed{Audits: map[string]*float64{"font-size": &zero}})
	if r.Flag != report.FlagNotMobileUsable {
		t.Errorf("failing font-size audit: %+v", r)
	}
}

// 4.7
func TestStalenessSignals(t *testing.T) {
	old := &capture.Result{Home: page("http://szabo.hu/", fixture(t, "old2012/index.html"))}
	old.Home.Doctype = ""
	out := Evaluate(Input{Capture: old, Probes: validProbes(), Now: now})
	for _, id := range []string{"stale_copyright", "old_jquery", "table_layout", "obsolete_markup", "html5_doctype"} {
		if r := find(t, out, id); r.Status != report.StatusFail {
			t.Errorf("old2012 %s = %+v", id, r)
		}
	}
	if ev := find(t, out, "stale_copyright").Evidence; ev["year"] != 2012 {
		t.Errorf("copyright evidence = %v", ev)
	}
	if ev := find(t, out, "old_jquery").Evidence; ev["version"] != "1.8.3" {
		t.Errorf("jquery evidence = %v", ev)
	}

	modern := &capture.Result{Home: page("https://example.hu/", fixture(t, "modern/index.html"))}
	out = Evaluate(Input{Capture: modern, Probes: validProbes(), Now: now})
	want := map[string]report.Status{"stale_copyright": report.StatusPass, "old_jquery": report.StatusNotApplicable,
		"table_layout": report.StatusPass, "obsolete_markup": report.StatusPass, "html5_doctype": report.StatusPass}
	for id, st := range want {
		if r := find(t, out, id); r.Status != st {
			t.Errorf("modern %s = %+v, want %s", id, r, st)
		}
	}
}

func TestCopyrightRanges(t *testing.T) {
	for html, year := range map[string]any{
		`<footer>Copyright © 2009–2017 Kft.</footer>`: 2017,
		`<p>&copy; 2025</p>`:                          2025,
		`<p>(c) 2014 Minden jog fenntartva</p>`:       2014,
	} {
		r := copyright(doc(html), now)
		if r.Evidence["year"] != year {
			t.Errorf("%s: %+v", html, r)
		}
	}
	if r := copyright(doc(`<p>Alapítva 1998</p>`), now); r.Status != report.StatusNotApplicable {
		t.Errorf("no copyright: %+v", r)
	}
}

// 4.8
func contactCapture(site *testsite.Server, forms []capture.Form, console []string) *capture.Result {
	cp := page(site.URL+"/kapcsolat", "<html></html>")
	cp.Forms, cp.Console = forms, console
	return &capture.Result{Home: page(site.URL+"/", "<html></html>"), Contact: &cp}
}

func TestContactFormPassive(t *testing.T) {
	site := testsite.Start("brokenform")
	defer site.Close()
	// form.invalid fails as not found without asking a real resolver.
	opts := ProbeOptions{HTTP: safehttp.New(safehttp.Options{Transport: testsite.LocalOnlyTransport()}), Dialer: testsite.LocalOnly{}, Now: now}

	t.Run("action returns 404", func(t *testing.T) {
		c := contactCapture(site, []capture.Form{{Action: "/contact.php", ActionURL: site.URL + "/contact.php", Method: "post", Textarea: true, Email: true}}, nil)
		r := contactForm(c, Probe(context.Background(), opts, site.URL, c))
		if r.Flag != report.FlagBrokenContactForm || !strings.Contains(r.Explanation, "404") {
			t.Errorf("%+v", r)
		}
	})
	t.Run("unresolvable host", func(t *testing.T) {
		c := contactCapture(site, []capture.Form{{Action: "https://form.invalid/send", ActionURL: "https://form.invalid/send", Textarea: true}}, nil)
		r := contactForm(c, Probe(context.Background(), opts, site.URL, c))
		if r.Flag != report.FlagBrokenContactForm {
			t.Errorf("%+v", r)
		}
	})
	t.Run("no action and no handler", func(t *testing.T) {
		c := contactCapture(site, []capture.Form{{Textarea: true}}, nil)
		if r := contactForm(c, Probe(context.Background(), opts, site.URL, c)); r.Flag != report.FlagBrokenContactForm {
			t.Errorf("%+v", r)
		}
	})
	t.Run("no action but script handler", func(t *testing.T) {
		c := contactCapture(site, []capture.Form{{Textarea: true, HasHandler: true}}, nil)
		if r := contactForm(c, Probe(context.Background(), opts, site.URL, c)); r.Status != report.StatusPass {
			t.Errorf("%+v", r)
		}
	})
	t.Run("suspicious only", func(t *testing.T) {
		c := contactCapture(site, []capture.Form{{Action: "/kapcsolat", ActionURL: site.URL + "/kapcsolat", Textarea: true}},
			[]string{"exception: TypeError: x is undefined"})
		r := contactForm(c, Probe(context.Background(), opts, site.URL, c))
		if r.Status != report.StatusSuspicious || r.Flag != "" {
			t.Errorf("%+v", r)
		}
	})
	t.Run("reCAPTCHA v1 is suspicious", func(t *testing.T) {
		c := contactCapture(site, []capture.Form{{Action: "/kapcsolat", ActionURL: site.URL + "/kapcsolat", Textarea: true}}, nil)
		c.Contact.Requests = []capture.Request{{URL: "https://www.google.com/recaptcha/api/challenge?k=abc", Type: "Script"}}
		r := contactForm(c, Probe(context.Background(), opts, site.URL, c))
		if r.Status != report.StatusSuspicious || r.Flag != "" || !strings.Contains(r.Explanation, "reCAPTCHA v1") {
			t.Errorf("%+v", r)
		}
	})
	t.Run("search forms are not contact forms", func(t *testing.T) {
		c := contactCapture(site, []capture.Form{{Search: true, Email: true}}, nil)
		if r := contactForm(c, nil); r.Status != report.StatusNotApplicable {
			t.Errorf("%+v", r)
		}
	})
	if p := site.Posts(); len(p) != 0 {
		t.Fatalf("fixture received non-GET/HEAD requests: %v", p)
	}
	for _, r := range site.Requests() {
		if strings.Contains(r, "contact.php") && !strings.HasPrefix(r, "HEAD ") && !strings.HasPrefix(r, "GET ") {
			t.Errorf("unexpected request %q", r)
		}
	}
}

// 4.9
func TestLegalGaps(t *testing.T) {
	l := huLocale(t)
	contact := page("https://kovacs.hu/kapcsolat", fixture(t, "modern/kapcsolat.html"))
	contact.Forms = []capture.Form{{Action: "/send", Textarea: true, Email: true}}
	legalPage := page("https://kovacs.hu/impresszum", fixture(t, "modern/impresszum.html"))
	modern := &capture.Result{Home: page("https://kovacs.hu/", fixture(t, "modern/index.html")), Contact: &contact, Legal: &legalPage}
	out := Evaluate(Input{Capture: modern, Probes: validProbes(), Locale: l, Now: now})
	if len(out.LegalGaps) != 0 {
		t.Errorf("modern legal gaps: %+v", out.LegalGaps)
	}

	oldContact := page("http://szabo.hu/elerhetoseg", fixture(t, "old2012/elerhetoseg.html"))
	oldContact.Forms = []capture.Form{{Action: "/kuld.php", Textarea: true}}
	old := &capture.Result{Home: page("http://szabo.hu/", fixture(t, "old2012/index.html")), Contact: &oldContact}
	old.Home.Cookies = []capture.Cookie{{Name: "_ga"}, {Name: "_ga_ABC123"}, {Name: "PHPSESSID"}}
	old.Home.Requests = []capture.Request{{URL: "https://www.google-analytics.com/g/collect?v=2", Type: "Ping"}}
	out = Evaluate(Input{Capture: old, Probes: validProbes(), Locale: l, Now: now})
	got := map[string]report.CheckResult{}
	for _, g := range out.LegalGaps {
		got[g.ID] = g
		if g.Flag != "" {
			t.Errorf("legal gap raised a flag: %+v", g)
		}
		if !strings.HasPrefix(g.Explanation, "Could not find") && g.ID != "legal_tracking_before_consent" {
			t.Errorf("legal gap not worded as 'could not find': %q", g.Explanation)
		}
	}
	for _, id := range []string{"legal_provider_details", "legal_privacy_notice", "legal_tracking_before_consent"} {
		if _, ok := got[id]; !ok {
			t.Errorf("missing gap %s in %+v", id, out.LegalGaps)
		}
	}
	if ck := got["legal_tracking_before_consent"].Evidence["cookies"].([]string); len(ck) != 2 {
		t.Errorf("tracker cookies = %v", ck)
	}
	if rq := got["legal_tracking_before_consent"].Evidence["requests"].([]string); !slices.Equal(rq, []string{"www.google-analytics.com/g/collect"}) {
		t.Errorf("tracker requests = %v, want host and path only", rq)
	}
	if ps := got["legal_provider_details"].Evidence["pages_searched"].([]string); len(ps) != 2 {
		t.Errorf("pages searched = %v", ps)
	}

	// A privacy notice linked from a button counts as linked.
	btn := page("https://aliz.hu/jelentkezes", `<form><textarea></textarea></form>`+
		`<button onclick="window.location.href='/adatkezelesi-tajekoztato'">Adatkezelés</button>`)
	btn.Forms = []capture.Form{{Textarea: true}}
	out = Evaluate(Input{Capture: &capture.Result{Home: page("https://aliz.hu/", "<p></p>"), Contact: &btn}, Probes: validProbes(), Locale: l, Now: now})
	seen := false
	for _, c := range append(out.Checks, out.LegalGaps...) {
		if c.ID == "legal_privacy_notice" {
			seen = true
			if c.Status != report.StatusPass || c.Evidence["link"] != "/adatkezelesi-tajekoztato" {
				t.Errorf("button-linked privacy notice: %+v", c)
			}
		}
	}
	if !seen {
		t.Error("legal_privacy_notice missing")
	}
}

// 4.6, plus the staleness and legal checks over a real Chrome capture.
func TestChecksOnRealCapture(t *testing.T) {
	if testing.Short() {
		t.Skip("needs Chrome")
	}
	if _, err := exec.LookPath("chrome"); err != nil {
		if _, err := os.Stat(`C:\Program Files\Google\Chrome\Application\chrome.exe`); err != nil {
			t.Skip("Chrome not found")
		}
	}
	site := testsite.Start("old2012")
	defer site.Close()
	l := huLocale(t)
	client := safehttp.New(safehttp.Options{})
	c, err := (&capture.Chrome{Locale: l, HTTP: client}).Capture(context.Background(), site.URL)
	if err != nil {
		t.Fatal(err)
	}
	p := Probe(context.Background(), ProbeOptions{HTTP: client, Now: now}, site.URL, c)
	out := Evaluate(Input{Capture: c, Probes: p, Locale: l, Now: now})

	if out.Accessibility == nil || len(out.Accessibility.Violations) == 0 {
		t.Fatalf("axe found nothing: %+v", find(t, out, "accessibility_axe"))
	}
	rules := map[string]bool{}
	for _, v := range out.Accessibility.Violations {
		rules[v.Rule] = true
	}
	if !rules["html-has-lang"] {
		t.Errorf("expected html-has-lang violation, got %v", rules)
	}
	for _, id := range []string{"stale_copyright", "old_jquery", "table_layout", "html5_doctype", "mobile_usability"} {
		if r := find(t, out, id); r.Status != report.StatusFail {
			t.Errorf("%s = %+v", id, r)
		}
	}
	if r := find(t, out, "https"); r.Flag != report.FlagNoHTTPS {
		t.Errorf("plain-HTTP fixture should get no_https: %+v", r)
	}
	if p := site.Posts(); len(p) != 0 {
		t.Errorf("site received %v", p)
	}
}
