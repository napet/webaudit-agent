// Package audit wires the stages together: capture → checks (with PageSpeed
// Insights in parallel) → assessment → scoring → report, and snapshot replay
// of the same pipeline from recorded inputs.
package audit

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/napet/webaudit-agent/internal/assess"
	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/checks"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/psi"
	"github.com/napet/webaudit-agent/internal/report"
	"github.com/napet/webaudit-agent/internal/score"
	"github.com/napet/webaudit-agent/internal/snapshot"
)

// Pipeline holds the stages. Every network-facing stage is an interface so
// tests can swap in fakes.
type Pipeline struct {
	Capturer capture.Capturer
	PSI      psi.Runner
	Assessor assess.Assessor // nil: skip the assessment
	Agent    AgentRunner     // nil: default mode; set: agent mode
	HTTP     *http.Client    // GET/HEAD-only client for check probes
	Locale   *config.Locale
	Scoring  *config.Scoring

	SnapshotRoot string           // "" disables snapshot writing
	Now          func() time.Time // default time.Now
	ProbeRoots   *x509.CertPool   // nil: system roots
	Log          func(format string, args ...any)
}

func (p *Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pipeline) logf(format string, args ...any) {
	if p.Log != nil {
		p.Log(format, args...)
	}
}

// Run audits one live site. A site that fails to load still gives a report
// (with the site-error flag); an error means the tool itself failed.
func (p *Pipeline) Run(ctx context.Context, siteURL string) (*report.Report, error) {
	start := time.Now()
	snap := &snapshot.Snapshot{Meta: snapshot.Meta{URL: siteURL, Timestamp: p.now().UTC(), Locale: p.Locale.Code}}

	// PageSpeed Insights takes 15–30 s on Google's side, so it runs while we
	// capture and probe the site ourselves.
	var g errgroup.Group
	g.Go(func() error {
		p.logf("pagespeed: requesting mobile analysis")
		raw, err := p.PSI.Run(ctx, siteURL)
		if err != nil {
			snap.Meta.PSIError = err.Error()
			p.logf("pagespeed: failed: %v", err)
			return nil
		}
		snap.PSI = raw
		p.logf("pagespeed: done")
		return nil
	})
	g.Go(func() error {
		p.logf("capture: loading %s", siteURL)
		c, err := p.Capturer.Capture(ctx, siteURL)
		if err != nil {
			return err
		}
		snap.Capture = c
		p.logf("probes: checking HTTPS and form endpoints")
		snap.Probes = checks.Probe(ctx, checks.ProbeOptions{HTTP: p.HTTP, Roots: p.ProbeRoots, Now: snap.Meta.Timestamp}, siteURL, c)
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("capture: %w", err)
	}

	in := p.checkInput(snap)
	out := checks.Evaluate(in)
	snap.Checks = out.Checks

	if p.Assessor == nil {
		snap.Meta.AssessSkip = "no assessor configured"
	} else {
		p.assess(ctx, snap, out.Checks)
	}

	// In agent mode the result above is the baseline. The agent adds
	// pages, so the checks run again over all of them, whatever the outcome.
	var ag *agentStage
	if p.Agent != nil {
		ag = p.runAgent(ctx, snap, in, out, nil)
		out = checks.Evaluate(in)
		snap.Checks = out.Checks
	}

	rep := assemble(snap, in, out, p.Scoring.Weights)
	if ag != nil {
		ag.finish(rep)
	}
	if p.SnapshotRoot != "" {
		dir, err := snapshot.Write(p.SnapshotRoot, snap)
		if err != nil {
			return nil, fmt.Errorf("write snapshot: %w", err)
		}
		rep.SnapshotPath = dir
	}
	rep.DurationMS = time.Since(start).Milliseconds()
	if rep.SnapshotPath != "" {
		if err := snapshot.WriteReport(rep.SnapshotPath, rep); err != nil {
			return nil, fmt.Errorf("write report: %w", err)
		}
	}
	return rep, nil
}

// Replay recomputes a report from a snapshot. It makes no request to the
// site or PageSpeed Insights. The recorded assessment is reused unless
// reassess is set, in which case p.Assessor is called on the recorded inputs.
// An agent-mode snapshot is replayed in agent mode: with reassess, p.Agent
// runs again on the recorded pages only.
func (p *Pipeline) Replay(ctx context.Context, dir string, reassess bool) (*report.Report, error) {
	start := time.Now()
	snap, err := snapshot.Read(dir)
	if err != nil {
		return nil, err
	}
	if snap.Capture == nil {
		return nil, errors.New("snapshot has no capture data")
	}
	if snap.Meta.Agent {
		rep, note, err := p.replayAgent(ctx, snap, reassess)
		if err != nil {
			return nil, err
		}
		rep.SnapshotPath = dir
		rep.Notes = append(rep.Notes, "Replayed from snapshot; no request was made to the site or PageSpeed Insights.", note)
		rep.DurationMS = time.Since(start).Milliseconds()
		return rep, nil
	}
	in := p.checkInput(snap)
	out := checks.Evaluate(in)

	note := "Assessment reused from the snapshot."
	if reassess {
		if p.Assessor == nil {
			return nil, errors.New("reassess requested without an assessor")
		}
		snap.Assessment, snap.Meta.AssessError, snap.Meta.AssessSkip, snap.Meta.AssessUsage = nil, "", "", nil
		p.assess(ctx, snap, out.Checks)
		note = "Assessment re-run on the snapshot's inputs."
	}

	rep := assemble(snap, in, out, p.Scoring.Weights)
	rep.SnapshotPath = dir
	rep.Notes = append(rep.Notes, "Replayed from snapshot; no request was made to the site or PageSpeed Insights.", note)
	rep.DurationMS = time.Since(start).Milliseconds()
	return rep, nil
}

func (p *Pipeline) checkInput(snap *snapshot.Snapshot) checks.Input {
	in := checks.Input{Capture: snap.Capture, Probes: snap.Probes, Locale: p.Locale, Now: snap.Meta.Timestamp}
	if len(snap.PSI) > 0 {
		if ps, err := psi.Parse(snap.PSI); err == nil {
			in.PageSpeed = ps
		} else if snap.Meta.PSIError == "" {
			snap.Meta.PSIError = err.Error()
		}
	}
	return in
}

// recordAssessError stores a failed assessment, including what its calls
// cost when any went through.
// skipNotLoaded is the skip reason when there is no homepage to rate.
const skipNotLoaded = "the homepage did not load, so there is nothing to look at"

// assess runs the datedness assessment on the snapshot's homepage and records
// the rating, the error or the skip reason in snap.
func (p *Pipeline) assess(ctx context.Context, snap *snapshot.Snapshot, results []report.CheckResult) {
	if !snap.Capture.Home.Loaded() || len(snap.Capture.DesktopPNG) == 0 {
		snap.Meta.AssessSkip = skipNotLoaded
		return
	}
	p.logf("assessment: asking the model")
	a, err := p.Assessor.Assess(ctx, assessInput(snap, results))
	if err != nil {
		recordAssessError(snap, err)
		p.logf("assessment: failed: %v", err)
		return
	}
	snap.Assessment = a
}

func recordAssessError(snap *snapshot.Snapshot, err error) {
	snap.Meta.AssessError = err.Error()
	var ce *assess.CallError
	if errors.As(err, &ce) {
		snap.Meta.AssessUsage = &ce.Usage
	}
}

func assessInput(snap *snapshot.Snapshot, cs []report.CheckResult) assess.Input {
	return assess.Input{URL: snap.Meta.URL, DesktopPNG: snap.Capture.DesktopPNG, MobilePNG: snap.Capture.MobilePNG,
		Summary: snap.Capture.Summary, Checks: cs}
}

// assemble builds the report from recorded inputs and evaluated checks. Live
// runs and replays both go through it, so they produce the same report.
func assemble(snap *snapshot.Snapshot, in checks.Input, out checks.Output, weights map[string]float64) *report.Report {
	c := snap.Capture
	rep := &report.Report{
		SchemaVersion:  report.SchemaVersion,
		URL:            snap.Meta.URL,
		FinalURL:       c.Home.FinalURL,
		Timestamp:      snap.Meta.Timestamp,
		Locale:         snap.Meta.Locale,
		Weights:        weights,
		Checks:         out.Checks,
		LegalGaps:      out.LegalGaps,
		PageSpeed:      in.PageSpeed,
		Accessibility:  out.Accessibility,
		Assessment:     snap.Assessment,
		ContactPageURL: c.ContactURL,
		LegalPageURL:   c.LegalURL,
		Components:     map[string]report.Component{},
	}
	if rep.LegalGaps == nil {
		rep.LegalGaps = []report.CheckResult{}
	}

	if c.Home.Loaded() {
		rep.Components["capture"] = report.Component{Status: "ok"}
	} else {
		rep.Components["capture"] = report.Component{Status: "failed", Error: firstNonEmpty(c.Home.LoadError, fmt.Sprintf("HTTP %d", c.Home.Status))}
	}
	switch {
	case in.PageSpeed != nil:
		rep.Components["pagespeed"] = report.Component{Status: "ok"}
		if in.PageSpeed.Field == nil {
			rep.Notes = append(rep.Notes, "Google has no real-user field data for this site; PageSpeed figures are lab measurements only.")
		}
	default:
		rep.Components["pagespeed"] = report.Component{Status: "failed", Error: snap.Meta.PSIError}
	}
	switch {
	case snap.Assessment != nil:
		rep.Components["assessment"] = report.Component{Status: "ok"}
		rep.CostUSD = snap.Assessment.CostUSD
	case snap.Meta.AssessError != "":
		rep.Components["assessment"] = report.Component{Status: "failed", Error: snap.Meta.AssessError, Usage: snap.Meta.AssessUsage}
		if u := snap.Meta.AssessUsage; u != nil {
			rep.CostUSD = u.CostUSD
		}
	default:
		rep.Components["assessment"] = report.Component{Status: "skipped", Error: snap.Meta.AssessSkip}
	}
	if c.Home.Loaded() && c.ContactURL == "" {
		rep.Notes = append(rep.Notes, "No contact page was found; contact-form checks ran on the homepage only.")
	}

	s := score.Compute(score.Input{Checks: out.Checks, PageSpeed: in.PageSpeed, Accessibility: out.Accessibility,
		Assessment: snap.Assessment, Weights: weights})
	rep.Tier, rep.Score, rep.SubScores, rep.MissingSubScores, rep.Flags = s.Tier, s.Score, s.SubScores, s.Missing, s.Flags
	if rep.Flags == nil {
		rep.Flags = []string{}
	}
	return rep
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// MarshalReport renders the report as indented JSON.
func MarshalReport(r *report.Report) ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
