// Package checks holds the deterministic (non-LLM) checks. It is split in
// two: Probe makes the few network requests the checks need and records
// them, and Evaluate is pure logic over recorded observations. Snapshot
// replay reruns Evaluate alone, so it never touches the audited site.
package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/napet/webaudit-agent/internal/capture"
)

// TLSProbe records the certificate the site presented.
type TLSProbe struct {
	Address     string    `json:"address"`
	DialError   string    `json:"dial_error,omitempty"` // no TLS service at all
	Valid       bool      `json:"valid"`
	VerifyError string    `json:"verify_error,omitempty"`
	NotAfter    time.Time `json:"not_after,omitzero"`
	Issuer      string    `json:"issuer,omitempty"`
}

// HTTPProbe records one GET or HEAD request made by a check.
type HTTPProbe struct {
	URL      string `json:"url"`
	Method   string `json:"method"`
	Status   int    `json:"status,omitempty"`
	Location string `json:"location,omitempty"`
	Error    string `json:"error,omitempty"`
	DNSError bool   `json:"dns_error,omitempty"`
}

// Probes are the network observations Evaluate needs beyond the capture.
type Probes struct {
	TLS         *TLSProbe            `json:"tls,omitempty"`
	PlainHTTP   *HTTPProbe           `json:"plain_http,omitempty"` // http:// homepage, redirects not followed
	FormActions map[string]HTTPProbe `json:"form_actions,omitempty"`
}

// ProbeOptions configures Probe.
type ProbeOptions struct {
	HTTP  *http.Client   // must be the GET/HEAD-only client
	Roots *x509.CertPool // nil: system roots
	Now   time.Time      // certificate validity reference; zero: time.Now()
}

// Probe makes the network requests the checks need: a TLS handshake, the
// plain-HTTP homepage, and a HEAD (or GET) of each contact-form action URL.
// It never sends a form or any request other than GET and HEAD.
func Probe(ctx context.Context, o ProbeOptions, siteURL string, cap *capture.Result) *Probes {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	base := siteURL
	if cap != nil && cap.Home.FinalURL != "" {
		base = cap.Home.FinalURL
	}
	u, err := url.Parse(base)
	if err != nil {
		return &Probes{}
	}
	p := &Probes{}
	p.TLS = probeTLS(ctx, o, u)
	p.PlainHTTP = probePlainHTTP(ctx, o, u)
	if cap != nil {
		p.probeForms(ctx, o.HTTP, ContactForms(cap))
	}
	return p
}

// ProbePage probes the action URLs of the contact forms on one page, such as
// a page the agent inspected, and records them in FormActions. URLs already
// recorded are not requested again. Like Probe, it sends only HEAD or GET.
func (p *Probes) ProbePage(ctx context.Context, client *http.Client, page *capture.Page) {
	p.probeForms(ctx, client, PageContactForms(page))
}

func (p *Probes) probeForms(ctx context.Context, client *http.Client, forms []capture.Form) {
	for _, f := range forms {
		if f.Action == "" {
			continue
		}
		au, err := url.Parse(f.ActionURL)
		if err != nil || (au.Scheme != "http" && au.Scheme != "https") {
			continue
		}
		if p.FormActions == nil {
			p.FormActions = map[string]HTTPProbe{}
		}
		if _, done := p.FormActions[f.ActionURL]; !done {
			p.FormActions[f.ActionURL] = probeAction(ctx, client, f.ActionURL)
		}
	}
}

func probeTLS(ctx context.Context, o ProbeOptions, u *url.URL) *TLSProbe {
	host, port := u.Hostname(), "443"
	if u.Scheme == "https" && u.Port() != "" {
		port = u.Port()
	}
	tp := &TLSProbe{Address: net.JoinHostPort(host, port)}
	d := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		// Verification happens below, so an invalid certificate is still
		// recorded with its details instead of just failing the handshake.
		Config: &tls.Config{InsecureSkipVerify: true, ServerName: host},
	}
	conn, err := d.DialContext(ctx, "tcp", tp.Address)
	if err != nil {
		tp.DialError = err.Error()
		return tp
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		tp.VerifyError = "no certificate presented"
		return tp
	}
	leaf := certs[0]
	tp.NotAfter, tp.Issuer = leaf.NotAfter, leaf.Issuer.String()
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: o.Roots, Intermediates: inter, CurrentTime: o.Now})
	if err != nil {
		tp.VerifyError = err.Error()
		return tp
	}
	tp.Valid = true
	return tp
}

func probePlainHTTP(ctx context.Context, o ProbeOptions, u *url.URL) *HTTPProbe {
	pu := *u
	pu.Scheme, pu.Path, pu.RawQuery, pu.Fragment = "http", "/", "", ""
	if u.Scheme == "https" {
		pu.Host = u.Hostname() // the https port means nothing for plain HTTP
	}
	noFollow := *o.HTTP
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return do(ctx, &noFollow, http.MethodGet, pu.String())
}

func probeAction(ctx context.Context, c *http.Client, u string) HTTPProbe {
	hp := do(ctx, c, http.MethodHead, u)
	if hp.Status == http.StatusMethodNotAllowed || hp.Status == http.StatusNotImplemented {
		hp = do(ctx, c, http.MethodGet, u)
	}
	return *hp
}

func do(ctx context.Context, c *http.Client, method, u string) *HTTPProbe {
	hp := &HTTPProbe{URL: u, Method: method}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		hp.Error = err.Error()
		return hp
	}
	resp, err := c.Do(req)
	if err != nil {
		hp.Error = err.Error()
		var dnsErr *net.DNSError
		hp.DNSError = errors.As(err, &dnsErr)
		return hp
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) // drain for reuse; only the status matters
	resp.Body.Close()
	hp.Status, hp.Location = resp.StatusCode, resp.Header.Get("Location")
	return hp
}
