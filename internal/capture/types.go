// Package capture loads an audited site in headless Chrome the way a visitor
// sees it, and records the raw observations the checks and the datedness
// assessment work from. Capture never clicks, types or submits anything.
package capture

import (
	"context"
	"encoding/json"
)

// Cookie is a cookie present after the page loaded, before any interaction.
type Cookie struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
}

// Request is one network request the page made while loading.
type Request struct {
	URL    string `json:"url"`
	Method string `json:"method"`
	Type   string `json:"type"`
	Status int    `json:"status,omitempty"`
	Failed bool   `json:"failed,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Form describes one form on a page and whether any script handles its
// submission.
type Form struct {
	Index      int    `json:"index"`
	Action     string `json:"action"`      // attribute as written
	ActionURL  string `json:"action_url"`  // resolved by the browser
	Method     string `json:"method"`      // lowercased
	HasHandler bool   `json:"has_handler"` // submit/click listener on the form, a submit button or an ancestor
	Textarea   bool   `json:"textarea"`    // has a textarea
	Email      bool   `json:"email"`       // has an email-like input
	Password   bool   `json:"password"`    // has a password input
	Search     bool   `json:"search"`      // looks like a search form
}

// Page is one rendered page.
type Page struct {
	RequestedURL string    `json:"requested_url"`
	FinalURL     string    `json:"final_url"`
	Status       int       `json:"status"`
	LoadMS       int64     `json:"load_ms"`
	TimedOut     bool      `json:"timed_out,omitempty"`
	LoadError    string    `json:"load_error,omitempty"`
	HTML         string    `json:"html"`
	Doctype      string    `json:"doctype"` // "" when missing; "html" for HTML5; else name plus public ID
	Console      []string  `json:"console_errors,omitempty"`
	Cookies      []Cookie  `json:"cookies,omitempty"`
	Requests     []Request `json:"requests,omitempty"`
	Forms        []Form    `json:"forms,omitempty"`
}

// Loaded reports whether the page produced a document worth checking.
func (p *Page) Loaded() bool {
	return p != nil && p.LoadError == "" && p.Status > 0 && p.Status < 500
}

// Mobile holds what only the mobile render shows.
type Mobile struct {
	ScrollWidth   int    `json:"scroll_width"`
	ViewportWidth int    `json:"viewport_width"`
	Overflow      bool   `json:"overflow"` // page scrolls horizontally on a phone
	Widest        string `json:"widest,omitempty"`
	WidestRight   int    `json:"widest_right,omitempty"`
}

// Result is everything capture observed for one site. The PNGs are stored
// separately in snapshots, so they are not part of the JSON.
type Result struct {
	Home       Page            `json:"home"`
	Mobile     Mobile          `json:"mobile"`
	Summary    string          `json:"summary"`
	Axe        json.RawMessage `json:"axe,omitempty"` // raw axe.run() results
	AxeError   string          `json:"axe_error,omitempty"`
	AxeVersion string          `json:"axe_version"`

	ContactURL    string `json:"contact_url,omitempty"`
	ContactSource string `json:"contact_source,omitempty"` // "link" or "path"
	Contact       *Page  `json:"contact,omitempty"`
	LegalURL      string `json:"legal_url,omitempty"`
	LegalSource   string `json:"legal_source,omitempty"`
	Legal         *Page  `json:"legal,omitempty"`
	// Status of each guessed path tried during discovery.
	GuessedPaths map[string]int `json:"guessed_paths,omitempty"`

	// Agent holds the pages the agent inspected, in order, and AgentShots
	// their screenshots at the same index. Empty outside agent mode.
	// Snapshots store them in agent/pages/, not in capture.json.
	Agent      []Page `json:"-"`
	AgentShots []Shot `json:"-"`

	DesktopPNG []byte `json:"-"`
	MobilePNG  []byte `json:"-"`
}

// Shot holds a page's first-screen screenshots; either may be empty.
type Shot struct {
	Desktop []byte
	Mobile  []byte
}

// Session renders single pages on demand in one browser. The agent's
// page inspections use it; replays implement it from recorded pages.
type Session interface {
	Render(ctx context.Context, url string) (Page, Shot, error)
	Close()
}

// Capturer renders a site. The pipeline depends on this, so tests can swap in
// a fake.
type Capturer interface {
	Capture(ctx context.Context, url string) (*Result, error)
}
