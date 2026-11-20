package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/napet/webaudit-agent/internal/agent"
	"github.com/napet/webaudit-agent/internal/audit"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
	"github.com/napet/webaudit-agent/internal/testsite"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

var keys = map[string]string{envAnthropic: "a", envPSI: "p"}

func runCLI(args []string, e map[string]string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(context.Background(), append([]string{"--config", "../../config", "--prompts", "../../prompts"}, args...), &out, &errb, env(e))
	return code, out.String(), errb.String()
}

func TestNoArgument(t *testing.T) {
	code, out, errOut := runCLI(nil, keys)
	if code == 0 || out != "" || !strings.Contains(errOut, "usage: audit") {
		t.Errorf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestMissingAPIKeyTouchesNothing(t *testing.T) {
	site := testsite.Start("modern")
	defer site.Close()
	for _, missing := range []string{envAnthropic, envPSI} {
		e := map[string]string{envAnthropic: "a", envPSI: "p"}
		delete(e, missing)
		code, _, errOut := runCLI([]string{site.URL}, e)
		if code == 0 || !strings.Contains(errOut, missing) {
			t.Errorf("%s missing: code=%d stderr=%q", missing, code, errOut)
		}
	}
	if r := site.Requests(); len(r) != 0 {
		t.Errorf("site was contacted: %v", r)
	}
}

func TestUnknownLocale(t *testing.T) {
	code, _, errOut := runCLI([]string{"--locale", "xx", "example.hu"}, keys)
	if code == 0 || !strings.Contains(errOut, `"xx"`) {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"a.hu", "b.hu"},
		{"--reassess", "a.hu"},
		{"--from-snapshot", "x", "a.hu"},
		{"ftp://a.hu"},
		{"--model", "gpt-4", "a.hu"},
	} {
		if code, _, errOut := runCLI(args, keys); code != exitUsage {
			t.Errorf("%v: code=%d stderr=%q", args, code, errOut)
		}
	}
}

// --out writes the report to the file; stdout stays empty and stderr only
// carries progress.
func TestOutputToFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.json")
	code, stdout, stderr := runCLI([]string{"--from-snapshot", "../../testdata/snapshots/example", "--out", out}, nil)
	if code != exitOK || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var rep report.Report
	if err := json.Unmarshal(b, &rep); err != nil || rep.SchemaVersion != report.SchemaVersion || rep.Tier == 0 {
		t.Errorf("report file: err=%v %.200s", err, b)
	}
	if !strings.Contains(stderr, "report written to") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{
		"example.hu":               "https://example.hu/",
		"http://example.hu":        "http://example.hu/",
		" https://example.hu/a?b ": "https://example.hu/a?b",
	} {
		if got, err := normalizeURL(in); err != nil || got != want {
			t.Errorf("%q -> %q, %v; want %q", in, got, err, want)
		}
	}
}

// pipelineFor wires the pipeline a live audit with these arguments would
// run, without running it.
func pipelineFor(t *testing.T, args ...string) *audit.Pipeline {
	t.Helper()
	o, _, err := parse(append([]string{"--config", "../../config", "--prompts", "../../prompts"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	scoring, err := config.LoadScoring(o.configDir)
	if err != nil {
		t.Fatal(err)
	}
	locale, err := config.LoadLocale(o.configDir, o.locale)
	if err != nil {
		t.Fatal(err)
	}
	o.model = scoring.DefaultModel
	p, err := newPipeline(o, locale, scoring, scoring.Models[o.model], true, env(keys), func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Spec: Default mode unchanged. The pipeline's own tests show its report
// is byte-for-byte the pre-agent one.
func TestDefaultModeHasNoAgent(t *testing.T) {
	p := pipelineFor(t, "example.hu")
	if p.Agent != nil || p.Assessor == nil {
		t.Errorf("agent %v, assessor %v; want one assessment call and no agent", p.Agent, p.Assessor)
	}
}

// Spec: Agent mode.
func TestAgentFlagRunsAgent(t *testing.T) {
	p := pipelineFor(t, "--agent", "example.hu")
	r, ok := p.Agent.(*agent.Runner)
	if !ok || p.Assessor == nil {
		t.Fatalf("agent %T, assessor %v; want the v1 assessment and the agent", p.Agent, p.Assessor)
	}
	scoring, _ := config.LoadScoring("../../config")
	if r.Config.Budgets != scoring.Agent || r.Config.Model != scoring.DefaultModel || r.Config.Prompt.Version != "v2" ||
		r.Config.Prompt.RubricVersion != "v1" || r.Open == nil || r.HTTP == nil || r.Locale == nil {
		t.Errorf("runner config = %+v", r.Config)
	}
}

// Spec: Agent flag with a snapshot.
func TestAgentFlagWithNonAgentSnapshot(t *testing.T) {
	code, out, errOut := runCLI([]string{"--agent", "--from-snapshot", "../../testdata/snapshots/example"}, nil)
	if code != exitUsage || out != "" || !strings.Contains(errOut, "not recorded in agent mode") {
		t.Errorf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

// Replaying an agent snapshot uses agent replay without the flag, and needs
// no API key: the recorded run is reused.
func TestReplayAgentSnapshot(t *testing.T) {
	code, out, errOut := runCLI([]string{"--from-snapshot", "../../testdata/snapshots/agent-example"}, nil)
	if code != exitOK {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	var rep report.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Agent == nil || rep.Agent.StoppedBy != "submit" || len(rep.Agent.Pages) != 2 {
		t.Errorf("agent section = %+v", rep.Agent)
	}
	if code, _, _ := runCLI([]string{"--agent", "--from-snapshot", "../../testdata/snapshots/agent-example"}, nil); code != exitOK {
		t.Errorf("--agent with an agent snapshot: code=%d", code)
	}
}

// --reassess of an agent snapshot wires the agent, to run on recorded pages.
func TestReassessAgentSnapshotWiresAgent(t *testing.T) {
	code, _, errOut := runCLI([]string{"--from-snapshot", "../../testdata/snapshots/agent-example", "--reassess"}, nil)
	if code != exitUsage || !strings.Contains(errOut, envAnthropic) {
		t.Errorf("without a key: code=%d stderr=%q", code, errOut)
	}
	p := pipelineFor(t, "--agent", "--from-snapshot", "../../testdata/snapshots/agent-example", "--reassess")
	if _, ok := p.Agent.(*agent.Runner); !ok {
		t.Errorf("agent = %T, want the runner", p.Agent)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestStdoutWriteFailure(t *testing.T) {
	var errb bytes.Buffer
	code := writeReport(&report.Report{}, "", failingWriter{}, &errb)
	if code != exitFailure || !strings.Contains(errb.String(), "write report") {
		t.Errorf("code=%d stderr=%q", code, errb.String())
	}
}
