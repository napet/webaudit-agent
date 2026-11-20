package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/testsite"
)

// fakeSession is a browser with canned pages, keyed by normalised URL.
// Unknown URLs render as a 404 page; a page with err set fails to render.
type fakeSession struct {
	pages    map[string]fakePage
	rendered []string
}

type fakePage struct {
	page capture.Page
	shot capture.Shot
	err  error
}

func (s *fakeSession) Render(_ context.Context, u string) (capture.Page, capture.Shot, error) {
	s.rendered = append(s.rendered, u)
	fp, ok := s.pages[Normalize(u)]
	if !ok {
		return capture.Page{RequestedURL: u, FinalURL: u, Status: 404, HTML: "<title>Not found</title>"}, capture.Shot{}, nil
	}
	if fp.err != nil {
		return capture.Page{}, capture.Shot{}, fp.err
	}
	p := fp.page
	p.RequestedURL = u
	return p, fp.shot, nil
}

func (s *fakeSession) Close() {}

const siteURL = "https://zoldkert.test"

// quoteForm is the agentsite quote form as the browser reports it: no
// action, sent by a click handler.
var quoteForm = capture.Form{Index: 0, Method: "get", HasHandler: true, Textarea: true, Email: true}

func fixtureHTML(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testsite.Dir(), "agentsite", filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// agentsite returns the baseline capture of the agentsite fixture (homepage only,
// as the baseline finds no contact or legal page) and a fake browser serving its
// other pages.
func agentsite(t *testing.T) (*capture.Result, *fakeSession) {
	t.Helper()
	page := func(path, file string, forms ...capture.Form) capture.Page {
		return capture.Page{RequestedURL: siteURL + path, FinalURL: siteURL + path, Status: 200, LoadMS: 800, HTML: fixtureHTML(t, file), Forms: forms}
	}
	c := &capture.Result{Home: page("/", "index.html")}
	s := &fakeSession{pages: map[string]fakePage{}}
	for path, file := range map[string]string{
		"/szolgaltatasok": "szolgaltatasok.html",
		"/arak":           "arak.html",
		"/rolunk":         "rolunk.html",
		"/rolunk/ceginfo": "rolunk/ceginfo.html",
		"/adatkezeles":    "adatkezeles.html",
	} {
		s.pages[Normalize(siteURL+path)] = fakePage{page: page(path, file), shot: capture.Shot{Desktop: []byte("desktop" + path), Mobile: []byte("mobile" + path)}}
	}
	s.pages[Normalize(siteURL+"/arkalkulator")] = fakePage{
		page: page("/arkalkulator", "arkalkulator.html", quoteForm),
		shot: capture.Shot{Desktop: []byte("desktop/arkalkulator"), Mobile: []byte("mobile/arkalkulator")},
	}
	return c, s
}

func huLocale(t *testing.T) *config.Locale {
	t.Helper()
	l, err := config.LoadLocale("../../config", "hu")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

var testBudgets = config.AgentBudgets{MaxPages: 3, MaxScreenshots: 2, MaxTurns: 6, MaxCostUSD: 0.10}

// newTestTools returns an executor over the agentsite fixture.
func newTestTools(t *testing.T) (*Tools, *fakeSession) {
	t.Helper()
	c, s := agentsite(t)
	return NewTools(ToolsConfig{Session: s, Capture: c, Locale: huLocale(t), Budgets: testBudgets, RubricVersion: "v1"}), s
}

// fakeModel replays scripted replies in order and records every request.
// Each scripted entry is a *anthropic.BetaMessage or an error. A call
// beyond the script fails the test at once, so a loop that never stops
// can't hang.
type fakeModel struct {
	t       *testing.T
	replies []any
	sent    []sentRequest
}

func (f *fakeModel) New(_ context.Context, body anthropic.BetaMessageNewParams, _ ...option.RequestOption) (*anthropic.BetaMessage, error) {
	f.t.Helper()
	// Recorded as JSON, so later changes to the loop's slices can't alter it.
	b, err := json.Marshal(body)
	if err != nil {
		f.t.Fatalf("request does not marshal: %v", err)
	}
	var s sentRequest
	if err := json.Unmarshal(b, &s); err != nil {
		f.t.Fatal(err)
	}
	s.JSON = string(b)
	f.sent = append(f.sent, s)
	i := len(f.sent) - 1
	if i >= len(f.replies) {
		f.t.Fatalf("model call %d, but only %d replies are scripted: the loop should have stopped", i+1, len(f.replies))
	}
	if err, ok := f.replies[i].(error); ok {
		return nil, err
	}
	return f.replies[i].(*anthropic.BetaMessage), nil
}

// sentRequest is the part of a request the loop tests look at.
type sentRequest struct {
	JSON     string        `json:"-"`
	Model    string        `json:"model"`
	System   []sentBlock   `json:"system"`
	Tools    []sentBlock   `json:"tools"`
	Messages []sentMessage `json:"messages"`
}

type sentMessage struct {
	Role    string      `json:"role"`
	Content []sentBlock `json:"content"`
}

type sentBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text"`
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	ToolUseID string      `json:"tool_use_id"`
	IsError   bool        `json:"is_error"`
	Content   []sentBlock `json:"content"`
}

// texts returns the text blocks of a message, including those inside tool
// results.
func (m sentMessage) texts() []string {
	var out []string
	for _, b := range m.Content {
		if b.Type == "text" {
			out = append(out, b.Text)
		}
		for _, c := range b.Content {
			if c.Type == "text" {
				out = append(out, c.Text)
			}
		}
	}
	return out
}
