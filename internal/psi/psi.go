// Package psi calls Google's PageSpeed Insights API, which runs Lighthouse on
// Google's servers and adds real-user field data (CrUX) when it has any.
package psi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/napet/webaudit-agent/internal/report"
)

// Endpoint is the PageSpeed Insights v5 API.
const Endpoint = "https://www.googleapis.com/pagespeedonline/v5/runPagespeed"

// Categories requested from Lighthouse.
var Categories = []string{"PERFORMANCE", "ACCESSIBILITY", "BEST_PRACTICES", "SEO"}

// MobileAudits are the Lighthouse audits the mobile usability check reads.
// Newer Lighthouse versions dropped some of them; absent ones are ignored.
var MobileAudits = []string{"viewport", "font-size", "tap-targets"}

// Runner fetches a raw PSI response. The pipeline depends on this interface
// so tests and snapshot replay never call Google.
type Runner interface {
	Run(ctx context.Context, pageURL string) (json.RawMessage, error)
}

// Client is the live PSI API client.
type Client struct {
	Key      string
	HTTP     *http.Client // default: 90s timeout
	Endpoint string       // default Endpoint
}

// Run requests a mobile Lighthouse report for pageURL and returns the raw
// response.
func (c *Client) Run(ctx context.Context, pageURL string) (json.RawMessage, error) {
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 90 * time.Second}
	}
	ep := c.Endpoint
	if ep == "" {
		ep = Endpoint
	}
	q := url.Values{"url": {pageURL}, "strategy": {"mobile"}, "key": {c.Key}}
	for _, cat := range Categories {
		q.Add("category", cat)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pagespeed: %w", redactKey(err, c.Key))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("pagespeed: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		_ = json.Unmarshal(body, &e) // best effort: an unparsable body leaves the message empty
		return nil, fmt.Errorf("pagespeed: HTTP %d: %s", resp.StatusCode, e.Error.Message)
	}
	return body, nil
}

// redactKey keeps the API key out of error messages that quote the URL.
func redactKey(err error, key string) error {
	if key == "" {
		return err
	}
	if ue, ok := err.(*url.Error); ok {
		ue.URL = "(PageSpeed Insights request)"
		return ue
	}
	return err
}

type rawResponse struct {
	LoadingExperience *struct {
		Metrics map[string]struct {
			Percentile float64 `json:"percentile"`
			Category   string  `json:"category"`
		} `json:"metrics"`
	} `json:"loadingExperience"`
	LighthouseResult *struct {
		Categories map[string]struct {
			Score *float64 `json:"score"`
		} `json:"categories"`
		Audits map[string]struct {
			Score *float64 `json:"score"`
		} `json:"audits"`
	} `json:"lighthouseResult"`
}

// Parse turns a raw PSI response into the report's PageSpeed section.
func Parse(raw json.RawMessage) (*report.PageSpeed, error) {
	var r rawResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("pagespeed: parse: %w", err)
	}
	if r.LighthouseResult == nil {
		return nil, fmt.Errorf("pagespeed: response has no lighthouseResult")
	}
	ps := &report.PageSpeed{Strategy: "mobile", Scores: map[string]float64{}, Audits: map[string]*float64{}}
	for id, c := range r.LighthouseResult.Categories {
		if c.Score != nil {
			ps.Scores[id] = *c.Score
		}
	}
	for _, id := range MobileAudits {
		if a, ok := r.LighthouseResult.Audits[id]; ok {
			ps.Audits[id] = a.Score
		}
	}
	if r.LoadingExperience != nil && len(r.LoadingExperience.Metrics) > 0 {
		ps.Field = map[string]report.FieldMetric{}
		for name, m := range r.LoadingExperience.Metrics {
			ps.Field[name] = report.FieldMetric{Percentile: m.Percentile, Category: m.Category}
		}
	}
	return ps, nil
}
