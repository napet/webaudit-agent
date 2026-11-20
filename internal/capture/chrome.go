package capture

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/domdebugger"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/napet/webaudit-agent/internal/config"
)

//go:embed axe/axe.min.js
var axeJS string

// AxeVersion is the pinned axe-core release embedded in the binary.
const AxeVersion = "4.10.2"

// User agents look like current Chrome, so sites serve their normal page, but
// still name the tool.
const (
	desktopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 webaudit/0.1"
	mobileUA  = "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36 webaudit/0.1"
)

// Viewports for the two above-the-fold screenshots.
const (
	DesktopWidth  = 1280
	DesktopHeight = 800
	MobileWidth   = 390
	MobileHeight  = 844
)

// DesktopSize and MobileSize name the two viewports in text shown to the model.
var (
	DesktopSize = fmt.Sprintf("%d×%d", DesktopWidth, DesktopHeight)
	MobileSize  = fmt.Sprintf("%d×%d", MobileWidth, MobileHeight)
)

// Chrome captures sites with a local headless Chrome.
type Chrome struct {
	Locale      *config.Locale
	HTTP        *http.Client  // GET/HEAD-only client for discovery probes
	LoadTimeout time.Duration // per page; default 30s
	SettleMax   time.Duration // network-idle wait after load; default 3s
	ExecPath    string        // optional Chrome binary
	// InsecureTLS makes Chrome accept any certificate. Tests only: fixture
	// sites use self-signed certificates. The HTTPS check judges certificates
	// separately, so this never hides a certificate problem in a report.
	InsecureTLS bool
}

func (c *Chrome) defaults() {
	if c.LoadTimeout == 0 {
		c.LoadTimeout = 30 * time.Second
	}
	if c.SettleMax == 0 {
		c.SettleMax = 3 * time.Second
	}
}

type renderOpts struct {
	mobile     bool
	screenshot bool
	axe        bool
	forms      bool
}

type rendered struct {
	page   Page
	png    []byte
	axe    json.RawMessage
	axeErr string
	mobile Mobile
}

// Capture renders the homepage on desktop and mobile, then the contact and
// legal-information pages it can find. A site that fails to load is not an
// error: the returned Home page records the failure. Errors are reserved for
// problems on our side, such as Chrome failing to start.
func (c *Chrome) Capture(ctx context.Context, rawURL string) (*Result, error) {
	browserCtx, stop, err := c.start(ctx)
	if err != nil {
		return nil, err
	}
	defer stop()

	res := &Result{AxeVersion: AxeVersion}
	home := c.render(browserCtx, rawURL, renderOpts{screenshot: true, axe: true, forms: true})
	res.Home, res.DesktopPNG, res.Axe, res.AxeError = home.page, home.png, home.axe, home.axeErr
	if !res.Home.Loaded() {
		return res, nil
	}
	res.Summary = Summarize(res.Home.HTML, SummaryLimit)

	mob := c.render(browserCtx, res.Home.FinalURL, renderOpts{mobile: true, screenshot: true})
	res.MobilePNG, res.Mobile = mob.png, mob.mobile

	if c.Locale == nil || c.HTTP == nil {
		return res, nil
	}
	res.GuessedPaths = map[string]int{}
	var tried map[string]int
	res.ContactURL, res.ContactSource, tried = Discover(ctx, c.HTTP, &res.Home, c.Locale.Contact)
	mergeInto(res.GuessedPaths, tried)
	if res.ContactURL != "" {
		p := c.render(browserCtx, res.ContactURL, renderOpts{forms: true}).page
		res.Contact = &p
	}
	res.LegalURL, res.LegalSource, tried = Discover(ctx, c.HTTP, &res.Home, c.Locale.LegalPage)
	mergeInto(res.GuessedPaths, tried)
	switch res.LegalURL {
	case "":
	case res.ContactURL:
		res.Legal = res.Contact
	default:
		p := c.render(browserCtx, res.LegalURL, renderOpts{}).page
		res.Legal = &p
	}
	return res, nil
}

// start launches Chrome. The returned func closes it.
func (c *Chrome) start(ctx context.Context) (context.Context, func(), error) {
	c.defaults()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(DesktopWidth, DesktopHeight),
		chromedp.UserAgent(desktopUA),
	)
	if c.ExecPath != "" {
		opts = append(opts, chromedp.ExecPath(c.ExecPath))
	}
	if c.InsecureTLS {
		opts = append(opts, chromedp.Flag("ignore-certificate-errors", true))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	stop := func() { cancelBrowser(); cancelAlloc() }
	if err := chromedp.Do(browserCtx); err != nil {
		stop()
		return nil, nil, fmt.Errorf("start chrome: %w", err)
	}
	return browserCtx, stop, nil
}

// ChromeSession keeps one browser open across single-page renders, for the
// agent's page inspections.
type ChromeSession struct {
	c          *Chrome
	browserCtx context.Context
	stop       func()
}

// Open starts a browser for a session. Close it when the audit ends.
func (c *Chrome) Open(ctx context.Context) (*ChromeSession, error) {
	browserCtx, stop, err := c.start(ctx)
	if err != nil {
		return nil, err
	}
	return &ChromeSession{c: c, browserCtx: browserCtx, stop: stop}, nil
}

// Render loads one page on desktop with its forms and screenshot, then takes
// the phone screenshot, so nothing needs a second visit later. A page that
// fails to load is not an error: the Page records the failure. Errors mean
// the browser itself is gone.
func (s *ChromeSession) Render(ctx context.Context, u string) (Page, Shot, error) {
	if err := s.browserCtx.Err(); err != nil {
		return Page{}, Shot{}, fmt.Errorf("browser closed: %w", err)
	}
	// The browser lives in the session's context; ctx bounds this render.
	renderCtx, cancel := context.WithCancel(s.browserCtx)
	defer cancel()
	stopWatch := context.AfterFunc(ctx, cancel)
	defer stopWatch()

	d := s.c.render(renderCtx, u, renderOpts{screenshot: true, forms: true})
	shot := Shot{Desktop: d.png}
	// A 404 page or a timeout is no page to look at on a phone either.
	if d.page.Loaded() && d.page.Status < 400 && !d.page.TimedOut {
		shot.Mobile = s.c.render(renderCtx, d.page.FinalURL, renderOpts{mobile: true, screenshot: true}).png
	}
	if err := ctx.Err(); err != nil {
		return d.page, shot, err
	}
	return d.page, shot, nil
}

// Close shuts the browser down.
func (s *ChromeSession) Close() { s.stop() }

var _ Session = (*ChromeSession)(nil)

func mergeInto(dst, src map[string]int) {
	for k, v := range src {
		dst[k] = v
	}
}

// collector gathers page events while a tab loads.
type collector struct {
	mu        sync.Mutex
	mainFrame cdp.FrameID
	reqs      map[network.RequestID]*Request
	order     []network.RequestID
	inflight  int
	lastNet   time.Time
	docStatus int
	docURL    string
	console   []string
}

func (col *collector) touch() { col.lastNet = time.Now() }

func (col *collector) idleFor(d time.Duration) bool {
	col.mu.Lock()
	defer col.mu.Unlock()
	return col.inflight <= 0 && time.Since(col.lastNet) >= d
}

// listen subscribes to the events of the tab and feeds the collector until
// ctx ends. Subscriptions are made before it returns, so no event is missed.
func listen(ctx context.Context, col *collector) {
	consume(chromedp.Events(ctx, network.RequestWillBeSent), col.onRequest)
	consume(chromedp.Events(ctx, network.ResponseReceived), col.onResponse)
	consume(chromedp.Events(ctx, network.LoadingFinished), col.onFinished)
	consume(chromedp.Events(ctx, network.LoadingFailed), col.onFailed)
	consume(chromedp.Events(ctx, runtime.ConsoleAPICalled), col.onConsole)
	consume(chromedp.Events(ctx, runtime.ExceptionThrown), col.onException)
}

// consume hands each event to handle on its own goroutine until the stream
// ends or fails.
func consume[E any](events iter.Seq2[E, error], handle func(E)) {
	go func() {
		for ev, err := range events {
			if err != nil {
				return
			}
			handle(ev)
		}
	}()
}

func (col *collector) onRequest(ev network.EventRequestWillBeSent) {
	col.mu.Lock()
	defer col.mu.Unlock()
	if r, ok := col.reqs[ev.RequestID]; ok { // redirect: same request, new URL
		r.URL = ev.Request.URL
	} else {
		col.reqs[ev.RequestID] = &Request{URL: ev.Request.URL, Method: ev.Request.Method, Type: string(ev.Type)}
		col.order = append(col.order, ev.RequestID)
		col.inflight++
	}
	col.touch()
}

func (col *collector) onResponse(ev network.EventResponseReceived) {
	col.mu.Lock()
	defer col.mu.Unlock()
	if r, ok := col.reqs[ev.RequestID]; ok {
		r.Status = int(ev.Response.Status)
	}
	if ev.Type == network.ResourceTypeDocument && (col.mainFrame == "" || ev.FrameID == col.mainFrame) {
		col.docStatus, col.docURL = int(ev.Response.Status), ev.Response.URL
	}
	col.touch()
}

func (col *collector) onFinished(ev network.EventLoadingFinished) {
	col.mu.Lock()
	defer col.mu.Unlock()
	if _, ok := col.reqs[ev.RequestID]; ok {
		col.inflight--
	}
	col.touch()
}

func (col *collector) onFailed(ev network.EventLoadingFailed) {
	col.mu.Lock()
	defer col.mu.Unlock()
	if r, ok := col.reqs[ev.RequestID]; ok {
		r.Failed, r.Error = true, ev.ErrorText
		col.inflight--
	}
	col.touch()
}

func (col *collector) onConsole(ev runtime.EventConsoleAPICalled) {
	if ev.Type != runtime.ConsoleAPICalledTypeError {
		return
	}
	var parts []string
	for _, a := range ev.Args {
		switch {
		case a.Description != "":
			parts = append(parts, a.Description)
		case len(a.Value) > 0:
			parts = append(parts, strings.Trim(string(a.Value), `"`))
		}
	}
	col.mu.Lock()
	col.console = append(col.console, "console.error: "+strings.Join(parts, " "))
	col.mu.Unlock()
}

func (col *collector) onException(ev runtime.EventExceptionThrown) {
	d := ev.ExceptionDetails
	msg := d.Text
	if d.Exception != nil && d.Exception.Description != "" {
		msg = d.Exception.Description
	}
	col.mu.Lock()
	col.console = append(col.console, "exception: "+firstLine(msg))
	col.mu.Unlock()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// render loads one URL in a fresh, isolated tab (its own cookie jar) and
// records what happened. It never fails: problems end up in Page.LoadError.
func (c *Chrome) render(browserCtx context.Context, u string, o renderOpts) rendered {
	var out rendered
	out.page.RequestedURL = u

	tabCtx, cancel := chromedp.NewContext(browserCtx, chromedp.WithNewBrowserContext())
	defer cancel()

	col := &collector{reqs: map[network.RequestID]*Request{}}
	if err := chromedp.Do(tabCtx, setupTab(col, o.mobile)); err != nil {
		out.page.LoadError = "tab setup: " + err.Error()
		return out
	}

	evCtx, stopEvents := context.WithCancel(tabCtx)
	defer stopEvents()
	listen(evCtx, col)

	c.navigate(tabCtx, u, &out.page)
	if !out.page.TimedOut {
		c.settle(col)
	}

	col.mu.Lock()
	out.page.Status, out.page.FinalURL = col.docStatus, col.docURL
	col.mu.Unlock()
	if out.page.LoadError == "" {
		collect(tabCtx, o, &out)
	}
	c.finish(&out, col)
	return out
}

// setupTab enables the event domains, remembers the main frame and sets the
// desktop or phone viewport and user agent.
func setupTab(col *collector, mobile bool) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		if _, err := cdp.Call(ctx, t, network.Enable, network.EnableParams{}); err != nil {
			return err
		}
		if _, err := cdp.Call(ctx, t, runtime.Enable, cdp.Empty{}); err != nil {
			return err
		}
		tree, err := cdp.Call(ctx, t, page.GetFrameTree, cdp.Empty{})
		if err != nil {
			return err
		}
		col.mainFrame = tree.FrameTree.Frame.ID
		w, h, ua := int64(DesktopWidth), int64(DesktopHeight), desktopUA
		if mobile {
			w, h, ua = MobileWidth, MobileHeight, mobileUA
		}
		if _, err := cdp.Call(ctx, t, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
			Width: w, Height: h, DeviceScaleFactor: 1, Mobile: mobile,
		}); err != nil {
			return err
		}
		if _, err := cdp.Call(ctx, t, emulation.SetUserAgentOverride, emulation.SetUserAgentOverrideParams{UserAgent: ua}); err != nil {
			return err
		}
		if mobile {
			if _, err := cdp.Call(ctx, t, emulation.SetTouchEmulationEnabled, emulation.SetTouchEmulationEnabledParams{Enabled: true}); err != nil {
				return err
			}
		}
		return nil
	})
}

// navigate loads u within the load timeout and records the load time and any
// timeout or load error on p.
func (c *Chrome) navigate(tabCtx context.Context, u string, p *Page) {
	start := time.Now()
	navCtx, cancelNav := context.WithTimeout(tabCtx, c.LoadTimeout)
	err := chromedp.Do(navCtx, chromedp.Navigate(u))
	cancelNav()
	p.LoadMS = time.Since(start).Milliseconds()
	var loadErr *chromedp.LoadError
	switch {
	case err == nil:
	case errors.Is(err, context.DeadlineExceeded):
		p.TimedOut = true
	case errors.As(err, &loadErr):
		p.LoadError = loadErr.ErrorText
	default:
		p.LoadError = err.Error()
	}
}

// settle waits, up to SettleMax, for the network to be idle for half a second.
func (c *Chrome) settle(col *collector) {
	deadline := time.Now().Add(c.SettleMax)
	for time.Now().Before(deadline) && !col.idleFor(500*time.Millisecond) {
		time.Sleep(100 * time.Millisecond)
	}
}

// collect reads the loaded page: location, HTML, doctype, cookies and, as o
// asks, forms, phone layout, screenshot and axe results. Each read gets its
// own short deadline: a page that never finished loading still answers script
// evaluation. A failed read leaves its field empty.
func collect(tabCtx context.Context, o renderOpts, out *rendered) {
	if loc, err := runValue(tabCtx, chromedp.Location()); err == nil && loc != "" {
		out.page.FinalURL = loc
	}
	if html, err := runValue(tabCtx, chromedp.Evaluate[string](`document.documentElement ? document.documentElement.outerHTML : ""`)); err == nil {
		out.page.HTML = html
	}
	if dt, err := runValue(tabCtx, chromedp.Evaluate[string](`document.doctype ? (document.doctype.name + " " + document.doctype.publicId).trim() : ""`)); err == nil {
		out.page.Doctype = dt
	}
	if cookies, err := runValue(tabCtx, readCookies); err == nil {
		out.page.Cookies = cookies
	}
	if o.forms {
		if forms, err := probeForms(tabCtx); err == nil {
			out.page.Forms = forms
		} else {
			out.page.Console = append(out.page.Console, "webaudit: form probe failed: "+err.Error())
		}
	}
	if o.mobile {
		if m, err := runValue(tabCtx, chromedp.Evaluate[Mobile](mobileJS)); err == nil {
			// Without a responsive viewport the browser widens the layout
			// viewport to fit the page, so compare against the device width.
			// scrollWidth alone reports phantom overflow of a few pixels on
			// pages that look fine, so judge by where visible content ends.
			// The checks decide how much overflow matters.
			m.ViewportWidth = MobileWidth
			m.Overflow = m.WidestRight > MobileWidth+1
			out.mobile = m
		}
	}
	if o.screenshot {
		if png, err := runValue(tabCtx, chromedp.CaptureScreenshot()); err == nil {
			out.png = png
		}
	}
	if o.axe {
		out.axe, out.axeErr = runAxe(tabCtx)
	}
}

// readCookies lists the names and domains of the tab's cookies.
var readCookies chromedp.Action[[]Cookie] = func(ctx context.Context, t *chromedp.Target) ([]Cookie, error) {
	r, err := cdp.Call(ctx, t, network.GetCookies, network.GetCookiesParams{})
	if err != nil {
		return nil, err
	}
	var out []Cookie
	for _, ck := range r.Cookies {
		out = append(out, Cookie{Name: ck.Name, Domain: ck.Domain})
	}
	return out, nil
}

func (c *Chrome) finish(out *rendered, col *collector) {
	col.mu.Lock()
	defer col.mu.Unlock()
	for _, id := range col.order {
		out.page.Requests = append(out.page.Requests, *col.reqs[id])
	}
	out.page.Console = append(out.page.Console, col.console...)
}

func runValue[T any](tabCtx context.Context, a chromedp.Action[T]) (T, error) {
	ctx, cancel := context.WithTimeout(tabCtx, 15*time.Second)
	defer cancel()
	return chromedp.Run(ctx, a)
}

// mobileJS measures the page width on the phone and names the element that
// reaches furthest right, as evidence for a horizontal-scroll finding.
// Content clipped by an ancestor with overflow other than visible (carousel
// tracks, off-canvas menus) cannot be scrolled to, so it is cut at that
// ancestor's right edge.
const mobileJS = `(() => {
  const clips = new Map();
  const clipOf = el => {
    if (!el || el.nodeType !== 1) return Infinity;
    if (clips.has(el)) return clips.get(el);
    let c = clipOf(el.parentElement);
    const ox = getComputedStyle(el).overflowX;
    if (ox !== 'visible') c = Math.min(c, el.getBoundingClientRect().right);
    clips.set(el, c);
    return c;
  };
  let widest = null, right = 0;
  for (const el of document.body ? document.body.querySelectorAll('*') : []) {
    const r = el.getBoundingClientRect();
    const vis = Math.min(r.right, clipOf(el.parentElement));
    if (r.width > 0 && vis > right) { right = vis; widest = el; }
  }
  // Text that spills out of its box does not widen the box, so measure text too.
  if (document.body) {
    const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    const range = document.createRange();
    for (let n = walk.nextNode(), i = 0; n && i < 5000; n = walk.nextNode(), i++) {
      if (!n.textContent.trim()) continue;
      range.selectNodeContents(n);
      const r = range.getBoundingClientRect();
      const vis = Math.min(r.right, clipOf(n.parentElement));
      if (r.width > 0 && vis > right) { right = vis; widest = n.parentElement; }
    }
  }
  let name = '';
  if (widest) {
    name = widest.tagName.toLowerCase() + (widest.id ? '#' + widest.id : '');
    const cls = widest.getAttribute('class');
    if (cls) name += '.' + cls.trim().split(/\s+/).join('.');
  }
  return {scroll_width: document.documentElement.scrollWidth, widest: name, widest_right: Math.round(right)};
})()`

const axeRun = `axe.run(document, {resultTypes: ['violations']}).then(r => ({
  version: r.testEngine.version,
  violations: r.violations.map(v => ({
    rule: v.id, impact: v.impact || 'unknown', nodes: v.nodes.length,
    examples: v.nodes.slice(0, 3).map(n => n.target.join(' '))
  }))
}))`

func runAxe(tabCtx context.Context) (json.RawMessage, string) {
	ctx, cancel := context.WithTimeout(tabCtx, 30*time.Second)
	defer cancel()
	if _, err := chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](axeJS)); err != nil {
		return nil, "inject axe: " + err.Error()
	}
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[[]byte](axeRun, chromedp.EvalAwaitPromise))
	if err != nil {
		return nil, "run axe: " + err.Error()
	}
	return raw, ""
}

// formsJS lists the forms and stashes, per form, the elements whose event
// listeners decide whether submitting does anything: the form, its ancestors
// up to window (delegated handlers), then its submit buttons.
const formsJS = `(() => {
  window.__waTargets = [];
  return Array.from(document.forms).slice(0, 10).map((f, i) => {
    const q = s => f.querySelector(s);
    const t = [];
    for (let n = f; n; n = n.parentNode) t.push(n);
    t.push(window);
    const anc = t.length;
    // Plain buttons count too: many forms send through a click handler on
    // <input type=button onclick="send()"> and never fire a submit event.
    Array.from(f.querySelectorAll('button, input[type=submit], input[type=image], input[type=button], [onclick]')).slice(0, 20).forEach(b => t.push(b));
    window.__waTargets.push(t);
    const attr = f.getAttribute('action') || '';
    let resolved = '';
    try { resolved = new URL(attr || location.href, document.baseURI).href; } catch (e) {}
    const label = [f.getAttribute('role'), f.id, f.getAttribute('class'), attr].join(' ');
    return {
      index: i, action: attr, action_url: resolved,
      method: (f.getAttribute('method') || 'get').toLowerCase(),
      textarea: !!q('textarea'),
      email: !!q('input[type=email], input[name*=mail i]'),
      password: !!q('input[type=password]'),
      // A form with a text area is a message form, whatever it is named.
      // "keres" must start a word, so "ajanlatkeres" (quote request) is not a search.
      search: !q('textarea') && (f.getAttribute('role') === 'search' || !!q('input[type=search]') || /search|(^|[^a-z])keres/i.test(label)),
      anc: anc, total: t.length
    };
  });
})()`

type formProbe struct {
	Form
	Anc   int `json:"anc"`
	Total int `json:"total"`
}

func probeForms(tabCtx context.Context) ([]Form, error) {
	probes, err := runValue(tabCtx, chromedp.Evaluate[[]formProbe](formsJS))
	if err != nil {
		return nil, err
	}
	forms := make([]Form, len(probes))
	for i, p := range probes {
		forms[i] = p.Form
		for j := 0; j < p.Total && !forms[i].HasHandler; j++ {
			want := "submit"
			if j >= p.Anc {
				want = "click"
			}
			types, err := listenerTypes(tabCtx, fmt.Sprintf("window.__waTargets[%d][%d]", i, j))
			if err != nil {
				return nil, err
			}
			for _, ty := range types {
				if ty == want {
					forms[i].HasHandler = true
				}
			}
		}
	}
	return forms, nil
}

func listenerTypes(tabCtx context.Context, expr string) ([]string, error) {
	return runValue(tabCtx, func(ctx context.Context, t *chromedp.Target) ([]string, error) {
		obj, err := chromedp.Evaluate[*runtime.RemoteObject](expr)(ctx, t)
		if err != nil {
			return nil, err
		}
		if obj == nil || obj.ObjectID == "" {
			return nil, nil
		}
		depth := int64(0)
		r, err := cdp.Call(ctx, t, domdebugger.GetEventListeners, domdebugger.GetEventListenersParams{ObjectID: obj.ObjectID, Depth: &depth})
		if err != nil {
			return nil, err
		}
		var types []string
		for _, l := range r.Listeners {
			types = append(types, l.Type)
		}
		return types, nil
	})
}
