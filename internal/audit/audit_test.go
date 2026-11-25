package audit

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/napet/webaudit-agent/internal/assess"
	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
	"github.com/napet/webaudit-agent/internal/safehttp"
	"github.com/napet/webaudit-agent/internal/snapshot"
	"github.com/napet/webaudit-agent/internal/testsite"
)

var fixedNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// --- fakes -----------------------------------------------------------------

// meeting lets fakes prove they ran at the same time: each arrival waits
// until all have arrived, or gives up after a while. Unlike a wall-clock
// limit, it doesn't depend on how fast the machine or its network is.
type meeting struct {
	n       int32
	arrived atomic.Int32
	all     chan struct{}
	met     atomic.Int32
}

func newMeeting(n int32) *meeting { return &meeting{n: n, all: make(chan struct{})} }

func (m *meeting) arrive() {
	if m == nil {
		return
	}
	if m.arrived.Add(1) == m.n {
		close(m.all)
	}
	select {
	case <-m.all:
		m.met.Add(1)
	case <-time.After(5 * time.Second):
	}
}

type fakePSI struct {
	raw   []byte
	err   error
	meet  *meeting
	calls atomic.Int32
}

func (f *fakePSI) Run(ctx context.Context, _ string) (json.RawMessage, error) {
	f.calls.Add(1)
	f.meet.arrive()
	return f.raw, f.err
}

type fakeAssessor struct {
	rating int
	err    error
	calls  atomic.Int32
}

func (f *fakeAssessor) Assess(context.Context, assess.Input) (*report.Assessment, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return &report.Assessment{Rating: f.rating, Confidence: "high", RubricVersion: "v1", Model: "fake",
		Reasons: []report.Reason{{Subject: "fixture", Detail: "fake"}}, CostUSD: 0.01}, nil
}

type fakeCapturer struct {
	res   *capture.Result
	meet  *meeting
	calls atomic.Int32
}

func (f *fakeCapturer) Capture(context.Context, string) (*capture.Result, error) {
	f.calls.Add(1)
	f.meet.arrive()
	if f.res == nil {
		return nil, errors.New("fake capturer: must not be called")
	}
	return f.res, nil
}

// failTransport fails the test on any HTTP request.
type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("unexpected network request: %s %s", r.Method, r.URL)
	return nil, errors.New("network disabled in this test")
}

// --- helpers ---------------------------------------------------------------

func loadConfig(t *testing.T) (*config.Locale, *config.Scoring) {
	t.Helper()
	l, err := config.LoadLocale("../../config", "hu")
	if err != nil {
		t.Fatal(err)
	}
	s, err := config.LoadScoring("../../config")
	if err != nil {
		t.Fatal(err)
	}
	return l, s
}

func psiFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../psi/testdata/lab_only.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func needChrome(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("needs Chrome")
	}
	if _, err := exec.LookPath("chrome"); err == nil {
		return
	}
	if _, err := os.Stat(`C:\Program Files\Google\Chrome\Application\chrome.exe`); err == nil {
		return
	}
	t.Skip("Chrome not found")
}

func staticCapture() *capture.Result {
	return &capture.Result{
		Home: capture.Page{RequestedURL: "https://kis-ceg.hu/", FinalURL: "https://kis-ceg.hu/", Status: 200, LoadMS: 900, Doctype: "html",
			HTML: `<html lang="hu"><head><meta name="viewport" content="width=device-width"></head><body><footer>© 2026</footer></body></html>`},
		DesktopPNG: []byte("d"), MobilePNG: []byte("m"),
	}
}

// --- 4.10 --------------------------------------------------------------------

func TestPSIRunsConcurrentlyAndFailureIsContained(t *testing.T) {
	l, s := loadConfig(t)
	meet := newMeeting(2)
	p := &Pipeline{
		Capturer:    &fakeCapturer{res: staticCapture(), meet: meet},
		PSI:         &fakePSI{err: errors.New("pagespeed: HTTP 500: backend error"), meet: meet},
		Assessor:    &fakeAssessor{rating: 2},
		HTTP:        safehttp.New(safehttp.Options{Timeout: time.Second, Transport: testsite.LocalOnlyTransport()}),
		ProbeDialer: testsite.LocalOnly{},
		Locale:      l, Scoring: s, Now: func() time.Time { return fixedNow },
	}
	rep, err := p.Run(context.Background(), "https://kis-ceg.hu/")
	if err != nil {
		t.Fatal(err)
	}
	if n := meet.met.Load(); n != 2 {
		t.Errorf("%d of 2 fakes saw the other running; PSI and capture should overlap", n)
	}
	if c := rep.Components["pagespeed"]; c.Status != "failed" || c.Error == "" {
		t.Errorf("pagespeed component = %+v", c)
	}
	if rep.PageSpeed != nil || len(rep.Checks) < 8 || rep.Assessment == nil {
		t.Errorf("other results should be intact: psi=%v checks=%d assessment=%v", rep.PageSpeed, len(rep.Checks), rep.Assessment)
	}
	if !contains(rep.MissingSubScores, "performance") {
		t.Errorf("performance should be missing: %v", rep.MissingSubScores)
	}
}

func TestAssessmentFailureIsContained(t *testing.T) {
	l, s := loadConfig(t)
	p := &Pipeline{
		Capturer: &fakeCapturer{res: staticCapture()}, PSI: &fakePSI{raw: psiFixture(t)},
		Assessor: &fakeAssessor{err: &assess.CallError{Err: errors.New("assessment refused (category cyber)"),
			Usage: report.Usage{Model: "fake", InputTokens: 3000, OutputTokens: 1, CostUSD: 0.012}}},
		HTTP: safehttp.New(safehttp.Options{Timeout: time.Second, Transport: testsite.LocalOnlyTransport()}), ProbeDialer: testsite.LocalOnly{}, Locale: l, Scoring: s,
		SnapshotRoot: t.TempDir(), Now: func() time.Time { return fixedNow },
	}
	rep, err := p.Run(context.Background(), "https://kis-ceg.hu/")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Components["assessment"].Status != "failed" || !contains(rep.MissingSubScores, "datedness") || rep.PageSpeed == nil {
		t.Errorf("report = %+v", rep.Components)
	}
	// The failed call was billed, and a replay keeps showing what it cost.
	replayed, err := p.Replay(context.Background(), rep.SnapshotPath, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*report.Report{rep, replayed} {
		if u := r.Components["assessment"].Usage; r.CostUSD != 0.012 || u == nil || u.InputTokens != 3000 {
			t.Errorf("cost=%v usage=%+v, want the failed call's cost", r.CostUSD, u)
		}
	}
}

// --- 7.x and 8.1 ---------------------------------------------------------------

type fixtureCase struct {
	site   string
	rating int
	tier   int
	flags  []string
}

var fixtureCases = []fixtureCase{
	{"modern", 1, 2, nil},
	{"tracking", 2, 2, nil},
	{"brokenform", 2, 1, []string{report.FlagBrokenContactForm}},
	{"old2012", 5, 1, []string{report.FlagNotMobileUsable}},
	{"noviewport", 4, 1, []string{report.FlagNotMobileUsable}},
	{"hang", 3, 1, []string{report.FlagSiteErrorOrSlow}},
	// Hidden carousel slides and a form sent from a plain button raise no flags.
	{"carousel", 2, 2, nil},
}

func tlsPipeline(t *testing.T, site *testsite.Server, root string, a *fakeAssessor) *Pipeline {
	l, s := loadConfig(t)
	pool := x509.NewCertPool()
	pool.AddCert(site.Certificate())
	// The probe client must trust the fixture's certificate like Chrome does.
	tr := &http.Transport{TLSClientConfig: site.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
	client := safehttp.New(safehttp.Options{Transport: tr, Timeout: 5 * time.Second})
	return &Pipeline{
		Capturer:     &capture.Chrome{Locale: l, HTTP: client, InsecureTLS: true, LoadTimeout: 3 * time.Second},
		PSI:          &fakePSI{raw: psiFixture(t)},
		Assessor:     a,
		HTTP:         client,
		Locale:       l,
		Scoring:      s,
		SnapshotRoot: root,
		ProbeRoots:   pool,
		Now:          func() time.Time { return fixedNow },
	}
}

func TestEndToEndFixtures(t *testing.T) {
	needChrome(t)
	root := t.TempDir()
	for _, tc := range fixtureCases {
		t.Run(tc.site, func(t *testing.T) {
			site := testsite.StartTLS(tc.site)
			defer site.Close()
			rep, err := tlsPipeline(t, site, root, &fakeAssessor{rating: tc.rating}).Run(context.Background(), site.URL+"/")
			if err != nil {
				t.Fatal(err)
			}
			flags := rep.Flags
			if len(flags) == 0 {
				flags = nil
			}
			if rep.Tier != tc.tier || !reflect.DeepEqual(flags, tc.flags) {
				t.Errorf("tier=%d flags=%v, want tier=%d flags=%v\nchecks: %s", rep.Tier, rep.Flags, tc.tier, tc.flags, brief(rep.Checks))
			}
			if p := site.Posts(); len(p) != 0 {
				t.Errorf("site received non-GET/HEAD requests: %v", p)
			}
			if tc.site == "tracking" && len(rep.LegalGaps) == 0 {
				t.Error("tracking fixture should report a legal gap")
			}
			if tc.site != "hang" {
				checkSnapshotFiles(t, rep.SnapshotPath)
			}
		})
	}
}

func checkSnapshotFiles(t *testing.T, dir string) {
	t.Helper()
	if dir == "" {
		t.Fatal("report has no snapshot path")
	}
	for _, f := range []string{"meta.json", "capture.json", "page.html", "desktop.png", "mobile.png", "probes.json", "psi.json", "checks.json", "assessment.json", "report.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("snapshot missing %s: %v", f, err)
		}
	}
}

func TestReplay(t *testing.T) {
	needChrome(t)
	root := t.TempDir()
	site := testsite.StartTLS("brokenform")
	live := &fakeAssessor{rating: 3}
	orig, err := tlsPipeline(t, site, root, live).Run(context.Background(), site.URL+"/")
	site.Close() // the site is gone; replay must not need it
	if err != nil {
		t.Fatal(err)
	}

	l, s := loadConfig(t)
	offline := func(a *fakeAssessor, sc *config.Scoring) *Pipeline {
		return &Pipeline{
			Capturer: &fakeCapturer{}, PSI: &fakePSI{err: errors.New("must not be called")}, Assessor: a,
			HTTP:   &http.Client{Transport: failTransport{t}},
			Locale: l, Scoring: sc,
		}
	}

	// 7.2: replay reuses everything and touches no network.
	a := &fakeAssessor{rating: 5}
	p := offline(a, s)
	first, err := p.Replay(context.Background(), orig.SnapshotPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if a.calls.Load() != 0 || p.PSI.(*fakePSI).calls.Load() != 0 || p.Capturer.(*fakeCapturer).calls.Load() != 0 {
		t.Errorf("replay called assessor=%d psi=%d capture=%d", a.calls.Load(), p.PSI.(*fakePSI).calls.Load(), p.Capturer.(*fakeCapturer).calls.Load())
	}
	if first.Tier != orig.Tier || first.Score != orig.Score || !reflect.DeepEqual(first.SubScores, orig.SubScores) || first.Assessment.Rating != 3 {
		t.Errorf("replay differs from live run:\n live   %d %v %v\n replay %d %v %v", orig.Tier, orig.Score, orig.SubScores, first.Tier, first.Score, first.SubScores)
	}

	// Determinism: a second replay is identical.
	second, _ := p.Replay(context.Background(), orig.SnapshotPath, false)
	if second.Tier != first.Tier || second.Score != first.Score || !reflect.DeepEqual(second.SubScores, first.SubScores) {
		t.Error("two replays differ")
	}

	// --reassess makes exactly one new LLM call on the cached inputs.
	p = offline(a, s)
	re, err := p.Replay(context.Background(), orig.SnapshotPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if a.calls.Load() != 1 || re.Assessment.Rating != 5 || re.SubScores["datedness"] != 100 {
		t.Errorf("reassess: calls=%d assessment=%+v", a.calls.Load(), re.Assessment)
	}

	// 7.3: new weights change the score without an LLM call.
	weighted := *s
	weighted.Weights = map[string]float64{"datedness": 1}
	a2 := &fakeAssessor{rating: 1}
	rw, err := offline(a2, &weighted).Replay(context.Background(), orig.SnapshotPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if a2.calls.Load() != 0 || rw.Score != 50 || rw.Score == first.Score {
		t.Errorf("reweighted score=%v (was %v), llm calls=%d", rw.Score, first.Score, a2.calls.Load())
	}

	// The snapshot itself is never modified by replays.
	snap, err := snapshot.Read(orig.SnapshotPath)
	if err != nil || snap.Assessment.Rating != 3 {
		t.Errorf("snapshot changed: %v %+v", err, snap.Assessment)
	}
}

func brief(cs []report.CheckResult) string {
	var b []byte
	for _, c := range cs {
		b = append(b, (c.ID + "=" + string(c.Status) + " ")...)
	}
	return string(b)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
