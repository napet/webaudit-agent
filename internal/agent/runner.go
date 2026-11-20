package agent

import (
	"context"
	"fmt"
	"net/http"

	"github.com/napet/webaudit-agent/internal/assess"
	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/checks"
	"github.com/napet/webaudit-agent/internal/config"
)

// Input is the baseline result an agent run starts from.
type Input struct {
	// Assess is what the baseline's assessment saw: screenshots, summary, checks.
	Assess assess.Input
	// Capture is the baseline capture; inspected pages are appended to it.
	Capture *capture.Result
	// Probes receives the form-action probes of inspected pages.
	Probes *checks.Probes
	// Session, when set, replaces the browser: a replay passes a
	// RecordedSession. No probes are made then; the recorded ones are used.
	Session capture.Session
}

// Result is a finished agent run.
type Result struct {
	Outcome       Outcome
	Visits        []Visit
	Screenshots   int
	Transcript    *Transcript
	PromptVersion string
	Budgets       config.AgentBudgets
}

// Runner runs the agent for the pipeline: it opens the browser, builds the
// tools and the first message, and drives Agent.Run.
type Runner struct {
	API    assess.Messages
	Config Config
	// Open starts the browser session the tools render pages in.
	Open func(ctx context.Context) (capture.Session, error)
	// HTTP is the GET/HEAD-only client for form-action probes; nil skips
	// probing (replay).
	HTTP   *http.Client
	Locale *config.Locale
}

// Run runs one agent. An error means the agent could not start or the loop
// was misused; every way a started run ends is in Result.Outcome.
func (r *Runner) Run(ctx context.Context, in Input) (*Result, error) {
	s, client := in.Session, r.HTTP
	if s == nil {
		var err error
		if s, err = r.Open(ctx); err != nil {
			return nil, fmt.Errorf("open browser: %w", err)
		}
		defer s.Close()
	} else {
		client = nil
	}
	if in.Probes == nil {
		in.Probes = &checks.Probes{}
	}
	tools := NewTools(ToolsConfig{
		Session: s, Capture: in.Capture, Probes: in.Probes, HTTP: client, Locale: r.Locale,
		Budgets: r.Config.Budgets, RubricVersion: r.Config.Prompt.RubricVersion,
	})
	tr := &Transcript{PromptVersion: r.Config.Prompt.Version}
	a := &Agent{API: r.API, Config: r.Config, Tools: tools, First: FirstMessage(in.Assess, tools), Transcript: tr}
	out, err := a.Run(ctx)
	if err != nil {
		return nil, err
	}
	if out.Model == "" {
		out.Model = r.Config.Model
	}
	return &Result{
		Outcome: *out, Visits: tools.Visits(), Screenshots: tools.ScreenshotsUsed(), Transcript: tr,
		PromptVersion: r.Config.Prompt.Version, Budgets: r.Config.Budgets,
	}, nil
}
