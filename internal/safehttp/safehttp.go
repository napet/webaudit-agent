// Package safehttp provides the HTTP client every check uses to talk to an
// audited site. It refuses any method other than GET and HEAD, so a bug can
// never submit a form or POST to a business's server.
package safehttp

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// UserAgent identifies the tool to the sites it audits.
const UserAgent = "Mozilla/5.0 (compatible; webaudit/0.1; +manual small-business site audit)"

// ErrMethodNotAllowed is returned for any request that is not GET or HEAD.
type ErrMethodNotAllowed struct{ Method string }

func (e ErrMethodNotAllowed) Error() string {
	return fmt.Sprintf("safehttp: method %s refused; only GET and HEAD are allowed toward audited sites", e.Method)
}

// ErrRequestCap is returned once the per-audit request cap is reached.
var ErrRequestCap = fmt.Errorf("safehttp: per-audit request cap reached")

type guard struct {
	next  http.RoundTripper
	limit int64
	count atomic.Int64
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return nil, ErrMethodNotAllowed{Method: req.Method}
	}
	if g.limit > 0 && g.count.Add(1) > g.limit {
		return nil, ErrRequestCap
	}
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", UserAgent)
	}
	return g.next.RoundTrip(req)
}

// Options configures a client. Zero values give sensible defaults.
type Options struct {
	Timeout    time.Duration     // per request; default 15s
	MaxRequest int               // per client; 0 means 40
	Transport  http.RoundTripper // default http.DefaultTransport
}

// New returns a client that only sends GET and HEAD requests, sets the tool's
// user agent, and stops after a fixed number of requests.
func New(o Options) *http.Client {
	if o.Timeout == 0 {
		o.Timeout = 15 * time.Second
	}
	if o.MaxRequest == 0 {
		o.MaxRequest = 40
	}
	if o.Transport == nil {
		o.Transport = http.DefaultTransport
	}
	return &http.Client{
		Timeout:   o.Timeout,
		Transport: &guard{next: o.Transport, limit: int64(o.MaxRequest)},
	}
}
