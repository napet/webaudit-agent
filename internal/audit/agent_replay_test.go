package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/napet/webaudit-agent/internal/agent"
	"github.com/napet/webaudit-agent/internal/assess"
	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
	"github.com/napet/webaudit-agent/internal/snapshot"
	"github.com/napet/webaudit-agent/internal/testsite"
)

// liveAgentRun audits the agentsite fixture in agent mode, offline, and
// writes the snapshot. The fake agent inspects the given pages.
func liveAgentRun(t *testing.T, site *testsite.Server, outcome agent.Outcome, paths ...string) *report.Report {
	t.Helper()
	p := offlinePipeline(t, site, &fakeAssessor{rating: 3})
	p.SnapshotRoot = t.TempDir()
	files := map[string]string{"/arkalkulator": "arkalkulator.html", "/rolunk/ceginfo": "rolunk/ceginfo.html"}
	var pages []capture.Page
	for _, path := range paths {
		var forms []capture.Form
		if path == "/arkalkulator" {
			forms = []capture.Form{quoteForm}
		}
		pages = append(pages, agentPage(t, site.URL, path, files[path], forms...))
	}
	p.Agent = &fakeRunner{pages: pages, outcome: outcome}
	rep, err := p.Run(context.Background(), site.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// replayPipeline can only replay: capture, PageSpeed and the network fail
// the test if used.
func replayPipeline(t *testing.T, a *fakeAssessor, ag AgentRunner) *Pipeline {
	l, s := loadConfig(t)
	return &Pipeline{
		Capturer: &fakeCapturer{}, PSI: &fakePSI{err: errors.New("must not be called")}, Assessor: a, Agent: ag,
		HTTP: &http.Client{Transport: failTransport{t}}, Locale: l, Scoring: s,
	}
}

// failRunner fails the test if the agent runs.
type failRunner struct{ t *testing.T }

func (f failRunner) Run(context.Context, agent.Input) (*agent.Result, error) {
	f.t.Error("the agent must not run")
	return nil, errors.New("must not run")
}

func readJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

// Spec: Transcript written (8.1), for a run that submitted and one that did not.
func TestAgentSnapshotRecorded(t *testing.T) {
	failed := agent.Outcome{StoppedBy: agent.StopTurnLimit, Err: errors.New("turn limit reached without a submitted assessment"),
		Model: "claude-opus-5-5", Turns: 13, InputTokens: 9000, OutputTokens: 1200, CostUSD: 0.15}
	for name, outcome := range map[string]agent.Outcome{"submitted": submitted(4), "failed": failed} {
		t.Run(name, func(t *testing.T) {
			site := testsite.StartTLS("agentsite")
			defer site.Close()
			rep := liveAgentRun(t, site, outcome, "/arkalkulator", "/rolunk/ceginfo")
			dir := rep.SnapshotPath

			for _, f := range []string{"agent/run.json", "agent/transcript.json",
				"agent/pages/01/capture.json", "agent/pages/01/desktop.png", "agent/pages/01/mobile.png",
				"agent/pages/02/capture.json", "agent/pages/02/desktop.png", "agent/pages/02/mobile.png"} {
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
					t.Errorf("snapshot missing %s", f)
				}
			}
			var tr agent.Transcript
			readJSONFile(t, filepath.Join(dir, "agent", "transcript.json"), &tr)
			if len(tr.Turns) != rep.Agent.Turns || tr.PromptVersion != "v1" {
				t.Errorf("transcript has %d turns (prompt %q), report %d", len(tr.Turns), tr.PromptVersion, rep.Agent.Turns)
			}
			images := 0
			for _, turn := range tr.Turns {
				if turn.Image == "" {
					continue
				}
				images++
				if b, err := os.ReadFile(filepath.Join(dir, "agent", filepath.FromSlash(turn.Image))); err != nil || len(b) == 0 {
					t.Errorf("turn %d image %s is not a file in the snapshot", turn.N, turn.Image)
				}
			}
			if images == 0 {
				t.Error("want at least one screenshot referenced by the transcript")
			}
			if b, _ := os.ReadFile(filepath.Join(dir, "capture.json")); strings.Contains(string(b), `"requested_url": "`+site.URL+`/arkalkulator"`) {
				t.Error("agent pages belong in agent/pages/, not capture.json")
			}

			snap, err := snapshot.Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !snap.Meta.Agent || snap.Meta.AgentPrompt != "v1" || snap.AgentRun == nil || len(snap.Capture.Agent) != 2 ||
				string(snap.Capture.AgentShots[0].Desktop) != "desktop "+site.URL+"/arkalkulator" {
				t.Fatalf("read back: meta %+v, run %v, agent pages %d", snap.Meta, snap.AgentRun != nil, len(snap.Capture.Agent))
			}
			if v1 := snap.AgentRun.V1.Assessment; v1 == nil || v1.Rating != 3 {
				t.Errorf("v1 assessment = %+v, want the baseline rating 3", v1)
			}
			if snap.AgentRun.StoppedBy != string(outcome.StoppedBy) || len(snap.AgentRun.Visits) != 2 || snap.AgentRun.Visits[1].Agent != 1 {
				t.Errorf("run = %+v", snap.AgentRun)
			}
		})
	}
}

// Spec: Agent replay, reusing the recorded result (8.2).
func TestAgentReplayReusesRun(t *testing.T) {
	site := testsite.StartTLS("agentsite")
	defer site.Close()
	live := liveAgentRun(t, site, submitted(4), "/arkalkulator", "/rolunk/ceginfo")
	before := len(site.Requests())

	v1 := &fakeAssessor{rating: 5}
	rep, err := replayPipeline(t, v1, failRunner{t}).Replay(context.Background(), live.SnapshotPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(site.Requests()) - before; n != 0 || v1.calls.Load() != 0 {
		t.Errorf("replay made %d site requests and %d assessment calls, want none", n, v1.calls.Load())
	}
	for name, pair := range map[string][2]any{
		"agent":      {live.Agent, rep.Agent},
		"checks":     {live.Checks, rep.Checks},
		"assessment": {live.Assessment, rep.Assessment},
		"components": {live.Components, rep.Components},
		"scores":     {[]any{live.Tier, live.Score, live.Flags, live.CostUSD}, []any{rep.Tier, rep.Score, rep.Flags, rep.CostUSD}},
	} {
		a, _ := json.Marshal(pair[0])
		b, _ := json.Marshal(pair[1])
		if string(a) != string(b) {
			t.Errorf("%s differs:\n live   %s\n replay %s", name, a, b)
		}
	}
}

// Spec: Re-run agent offline (8.2). The agent runs again; pages the
// snapshot recorded are served, others are refused, and the site gets no
// request.
func TestAgentReplayReassess(t *testing.T) {
	site := testsite.StartTLS("agentsite")
	defer site.Close()
	live := liveAgentRun(t, site, submitted(4), "/arkalkulator")
	before := len(site.Requests())

	model := &scriptedModel{t: t, replies: []string{
		toolReply("tu_1", agent.ToolInspectPage, map[string]string{"url": site.URL + "/arkalkulator", "reason": "Quote form."}),
		toolReply("tu_2", agent.ToolInspectPage, map[string]string{"url": site.URL + "/arak", "reason": "Prices."}),
		toolReply("tu_3", agent.ToolSubmit, map[string]any{"rating": 2, "confidence": "high",
			"reasons": []map[string]string{{"subject": "hero", "detail": "Clean, current layout."}, {"subject": "navigation", "detail": "Short, clear top menu."}}}),
	}}
	rubric, err := assess.LoadRubric("../../prompts/" + assess.RubricFile)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := agent.LoadPrompt("../../prompts/"+agent.PromptFile, rubric)
	if err != nil {
		t.Fatal(err)
	}
	l, s := loadConfig(t)
	runner := &agent.Runner{
		API:    model,
		Config: agent.Config{Model: "claude-opus-5-5", Price: config.ModelPrice{InputPerMTok: 5, OutputPerMTok: 25}, Prompt: prompt, Budgets: s.Agent},
		Open: func(context.Context) (capture.Session, error) {
			t.Error("a replay must not open a browser")
			return nil, errors.New("no browser in replay")
		},
		Locale: l,
	}
	v1 := &fakeAssessor{rating: 5}
	rep, err := replayPipeline(t, v1, runner).Replay(context.Background(), live.SnapshotPath, true)
	if err != nil {
		t.Fatal(err)
	}

	if n := len(site.Requests()) - before; n != 0 || v1.calls.Load() != 0 {
		t.Errorf("reassess made %d site requests and %d v1 calls; want none (v1's recorded assessment is the baseline)", n, v1.calls.Load())
	}
	if len(model.sent) != 3 {
		t.Fatalf("model calls = %d, want 3", len(model.sent))
	}
	if !strings.Contains(model.sent[1], "Inspection 1: "+site.URL+"/arkalkulator") {
		t.Error("the recorded quote page should be served")
	}
	if !strings.Contains(model.sent[2], "not available in this replay") {
		t.Error("the unrecorded page should be refused with a replay error")
	}
	ag := rep.Agent
	if ag == nil || ag.StoppedBy != "submit" || len(ag.Pages) != 1 || ag.Turns != 3 {
		t.Fatalf("agent section = %+v", ag)
	}
	if !hasChange(ag.Changed, "contact_form", report.StatusNotApplicable, report.StatusPass) {
		t.Errorf("changes = %+v", ag.Changed.Checks)
	}
	if r := ag.Changed.Rating; r.From == nil || *r.From != 3 || r.To == nil || *r.To != 2 {
		t.Errorf("rating change = %v → %v, want the recorded v1 rating 3 → 2", r.From, r.To)
	}

	// Replays never modify the snapshot.
	snap, err := snapshot.Read(live.SnapshotPath)
	if err != nil || snap.Assessment.Rating != 4 || len(snap.Capture.Agent) != 1 {
		t.Errorf("snapshot changed: %v, assessment %+v", err, snap.Assessment)
	}
}

func TestReplayAgentSnapshotWithoutRun(t *testing.T) {
	site := testsite.StartTLS("agentsite")
	defer site.Close()
	live := liveAgentRun(t, site, submitted(4), "/arkalkulator")
	if err := os.Remove(filepath.Join(live.SnapshotPath, "agent", "run.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := replayPipeline(t, &fakeAssessor{}, failRunner{t}).Replay(context.Background(), live.SnapshotPath, false); err == nil {
		t.Error("an agent snapshot without agent/run.json should not replay")
	}
}
