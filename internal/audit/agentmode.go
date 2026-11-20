package audit

import (
	"context"
	"errors"
	"fmt"

	"github.com/napet/webaudit-agent/internal/agent"
	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/checks"
	"github.com/napet/webaudit-agent/internal/report"
	"github.com/napet/webaudit-agent/internal/snapshot"
)

// AgentRunner runs the agent from a finished baseline audit. *agent.Runner is
// the live one; the pipeline's tests use fakes, so they don't depend on the
// loop.
type AgentRunner interface {
	Run(ctx context.Context, in agent.Input) (*agent.Result, error)
}

// agentStage is the agent's part of one audit: the baseline report it started
// from, and the recorded run. Live audits and replays both finish the report
// from it, so they produce the same agent section.
type agentStage struct {
	baseline *report.Report
	run      snapshot.AgentRun
}

// runAgent keeps the baseline result, then runs the agent and
// records the run in snap. The agent's inspected pages land in
// snap.Capture.Agent and their form probes in snap.Probes. Its assessment
// replaces the baseline's; when it submits none, the assessment fails as in the baseline, with
// the agent's cost. When the agent cannot run at all, the baseline's assessment stays.
// session is nil for a live run; a replay passes the recorded pages.
func (p *Pipeline) runAgent(ctx context.Context, snap *snapshot.Snapshot, in checks.Input, out checks.Output, session capture.Session) *agentStage {
	st := &agentStage{baseline: assemble(snap, in, out, p.Scoring.Weights)}
	st.run.V1 = snapshot.V1Assessment{Assessment: snap.Assessment, Error: snap.Meta.AssessError, Skip: snap.Meta.AssessSkip, Usage: snap.Meta.AssessUsage}
	snap.Meta.Agent = true
	snap.AgentRun, snap.Transcript = &st.run, nil
	if !snap.Capture.Home.Loaded() || len(snap.Capture.DesktopPNG) == 0 {
		st.run.Skip = skipNotLoaded
		return st
	}
	p.logf("agent: starting from the baseline result")
	res, err := p.Agent.Run(ctx, agent.Input{Assess: assessInput(snap, out.Checks), Capture: snap.Capture, Probes: snap.Probes, Session: session})
	if err != nil {
		st.run.Error = err.Error()
		p.logf("agent: could not run: %v", err)
		return st
	}
	o := res.Outcome
	snap.Transcript = res.Transcript
	snap.Meta.AgentPrompt = res.PromptVersion
	st.run.StoppedBy, st.run.PromptVersion, st.run.Model = string(o.StoppedBy), res.PromptVersion, o.Model
	if o.Err != nil {
		st.run.Error = o.Err.Error()
	}
	st.run.Budgets, st.run.Visits, st.run.Screenshots = res.Budgets, res.Visits, res.Screenshots
	st.run.Turns, st.run.InputTokens, st.run.OutputTokens, st.run.CostUSD = o.Turns, o.InputTokens, o.OutputTokens, o.CostUSD
	if st.run.Visits == nil {
		st.run.Visits = []agent.Visit{}
	}
	p.logf("agent: stopped by %s after %d turns and %d pages ($%.3f)", o.StoppedBy, o.Turns, len(res.Visits), o.CostUSD)

	snap.Meta.AssessError, snap.Meta.AssessSkip, snap.Meta.AssessUsage = "", "", nil
	if o.Assessment != nil {
		a := *o.Assessment
		a.Model, a.InputTokens, a.OutputTokens, a.CostUSD = o.Model, o.InputTokens, o.OutputTokens, o.CostUSD
		snap.Assessment = &a
	} else {
		snap.Assessment = nil
		snap.Meta.AssessError = fmt.Sprintf("agent stopped by %s: %v", o.StoppedBy, o.Err)
		snap.Meta.AssessUsage = &report.Usage{Model: o.Model, InputTokens: o.InputTokens, OutputTokens: o.OutputTokens, CostUSD: o.CostUSD}
	}
	return st
}

// finish adds the agent section to the final report and the baseline call's cost
// to its total.
func (st *agentStage) finish(rep *report.Report) {
	r := st.run
	sec := &report.Agent{Pages: []report.AgentPage{}, Changed: compare(st.baseline, rep)}
	rep.Agent = sec
	switch {
	case r.Skip != "":
		sec.Error = r.Skip
		rep.Components["agent"] = report.Component{Status: "skipped", Error: r.Skip}
		return
	case r.StoppedBy == "":
		sec.Error = r.Error
		rep.Components["agent"] = report.Component{Status: "failed", Error: r.Error}
		return
	}
	rep.Components["agent"] = report.Component{Status: "ok"}
	sec.PromptVersion, sec.Model, sec.StoppedBy, sec.Error = r.PromptVersion, r.Model, r.StoppedBy, r.Error
	for _, v := range r.Visits {
		sec.Pages = append(sec.Pages, report.AgentPage{URL: v.URL, Reason: v.Reason, FinalURL: v.FinalURL, Status: v.Status, Error: v.Error})
	}
	sec.ScreenshotsViewed = r.Screenshots
	b := r.Budgets
	sec.Budgets = report.AgentBudgets{MaxPages: b.MaxPages, MaxScreenshots: b.MaxScreenshots, MaxTurns: b.MaxTurns, MaxCostUSD: b.MaxCostUSD}
	sec.Turns, sec.InputTokens, sec.OutputTokens, sec.CostUSD = r.Turns, r.InputTokens, r.OutputTokens, r.CostUSD
	// The final report's own cost is the agent's; the baseline call ran too.
	rep.CostUSD += st.baseline.CostUSD
}

// baselineView is the snapshot as the baseline left it: without the agent's pages, and with
// the baseline's assessment.
func baselineView(snap *snapshot.Snapshot) *snapshot.Snapshot {
	v := *snap
	c := *snap.Capture
	c.Agent, c.AgentShots = nil, nil
	v.Capture = &c
	v1 := snap.AgentRun.V1
	v.Assessment = v1.Assessment
	v.Meta.AssessError, v.Meta.AssessSkip, v.Meta.AssessUsage = v1.Error, v1.Skip, v1.Usage
	v.AgentRun, v.Transcript = nil, nil
	return &v
}

// replayAgent rebuilds an agent-mode report. Without reassess it reuses the
// recorded run. With reassess it runs the agent again from the recorded baseline
// result, serving only the pages the snapshot recorded; the recorded baseline
// assessment is kept.
func (p *Pipeline) replayAgent(ctx context.Context, snap *snapshot.Snapshot, reassess bool) (*report.Report, string, error) {
	if snap.AgentRun == nil {
		return nil, "", errors.New("agent snapshot has no agent/run.json")
	}
	base := baselineView(snap)
	bin := p.checkInput(base)
	bout := checks.Evaluate(bin)
	if !reassess {
		st := &agentStage{baseline: assemble(base, bin, bout, p.Scoring.Weights), run: *snap.AgentRun}
		in := p.checkInput(snap)
		rep := assemble(snap, in, checks.Evaluate(in), p.Scoring.Weights)
		st.finish(rep)
		return rep, "Agent result reused from the snapshot.", nil
	}
	if p.Agent == nil {
		return nil, "", errors.New("reassess of an agent snapshot needs an agent runner")
	}
	session := agent.NewRecordedSession(snap.Capture.Agent, snap.Capture.AgentShots)
	st := p.runAgent(ctx, base, bin, bout, session)
	out := checks.Evaluate(bin) // bin.Capture is base.Capture, which now holds the re-run's pages
	rep := assemble(base, bin, out, p.Scoring.Weights)
	st.finish(rep)
	return rep, "Agent re-run on the pages recorded in the snapshot; others were refused.", nil
}

// compare lists what changed from the baseline to the final report.
func compare(base, final *report.Report) report.AgentChanges {
	c := report.AgentChanges{Checks: []report.CheckChange{}, LegalGapsResolved: []string{}, LegalGapsAdded: []string{}}
	before := map[string]report.Status{}
	for _, r := range base.Checks {
		before[r.ID] = r.Status
	}
	for _, r := range final.Checks {
		if s, ok := before[r.ID]; ok && s != r.Status {
			c.Checks = append(c.Checks, report.CheckChange{ID: r.ID, From: s, To: r.Status})
		}
	}
	gaps := func(rs []report.CheckResult) map[string]bool {
		m := map[string]bool{}
		for _, r := range rs {
			m[r.ID] = true
		}
		return m
	}
	was, is := gaps(base.LegalGaps), gaps(final.LegalGaps)
	for _, r := range base.LegalGaps {
		if !is[r.ID] {
			c.LegalGapsResolved = append(c.LegalGapsResolved, r.ID)
		}
	}
	for _, r := range final.LegalGaps {
		if !was[r.ID] {
			c.LegalGapsAdded = append(c.LegalGapsAdded, r.ID)
		}
	}
	if a := base.Assessment; a != nil {
		c.Rating.From = &a.Rating
	}
	if a := final.Assessment; a != nil {
		c.Rating.To = &a.Rating
	}
	c.Tier.From, c.Tier.To = base.Tier, final.Tier
	return c
}
