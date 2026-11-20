// Package snapshot stores every input of a live audit on disk, so the report
// can be recomputed later without touching the site, PageSpeed Insights or,
// unless asked, the LLM. Snapshot directories are written once and never
// modified by replays.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/napet/webaudit-agent/internal/agent"
	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/checks"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
)

// Meta describes the run that produced a snapshot.
type Meta struct {
	URL         string    `json:"url"`
	Timestamp   time.Time `json:"timestamp"`
	Locale      string    `json:"locale"`
	PSIError    string    `json:"psi_error,omitempty"`
	AssessError string    `json:"assess_error,omitempty"`
	AssessSkip  string    `json:"assess_skipped,omitempty"`
	// AssessUsage is what a failed assessment still cost.
	AssessUsage *report.Usage `json:"assess_usage,omitempty"`
	// Agent is set for an audit run in agent mode, and AgentPrompt names the
	// agent prompt version it used.
	Agent       bool   `json:"agent,omitempty"`
	AgentPrompt string `json:"agent_prompt,omitempty"`
}

// Snapshot is the full set of recorded inputs.
type Snapshot struct {
	Meta       Meta
	Capture    *capture.Result
	Probes     *checks.Probes
	PSI        json.RawMessage // nil when the request failed
	Checks     []report.CheckResult
	Assessment *report.Assessment // nil when it failed or was skipped

	// Agent mode only. Capture.Agent and Capture.AgentShots hold the pages
	// the agent inspected; Assessment and the Meta assessment fields are the
	// agent's, and AgentRun.V1 keeps the baseline's.
	AgentRun   *AgentRun
	Transcript *agent.Transcript // nil when the agent did not run
}

// AgentRun is what the agent of an agent-mode audit did, and what the baseline had
// concluded before it. Replays rebuild the report's agent section from it.
type AgentRun struct {
	V1 V1Assessment `json:"v1"`

	// Skip says why the agent did not run. Otherwise StoppedBy is empty when
	// it could not start, and Error says why it did not submit or start.
	Skip      string `json:"skipped,omitempty"`
	StoppedBy string `json:"stopped_by,omitempty"`
	Error     string `json:"error,omitempty"`

	PromptVersion string              `json:"prompt_version,omitempty"`
	Model         string              `json:"model,omitempty"`
	Budgets       config.AgentBudgets `json:"budgets"`
	Visits        []agent.Visit       `json:"visits"`
	Screenshots   int                 `json:"screenshots"`
	Turns         int                 `json:"turns"`
	InputTokens   int64               `json:"input_tokens"`
	OutputTokens  int64               `json:"output_tokens"`
	CostUSD       float64             `json:"cost_usd"`
}

// V1Assessment is the baseline assessment of an agent-mode audit, which the
// agent is compared with. The name follows the snapshot format.
type V1Assessment struct {
	Assessment *report.Assessment `json:"assessment,omitempty"`
	Error      string             `json:"error,omitempty"`
	Skip       string             `json:"skipped,omitempty"`
	Usage      *report.Usage      `json:"usage,omitempty"`
}

const (
	metaFile       = "meta.json"
	captureFile    = "capture.json"
	pageFile       = "page.html"
	desktopFile    = "desktop.png"
	mobileFile     = "mobile.png"
	probesFile     = "probes.json"
	psiFile        = "psi.json"
	checksFile     = "checks.json"
	assessmentFile = "assessment.json"
	// ReportFile is the report written next to a live run's inputs.
	ReportFile = "report.json"

	// Agent mode: agent/run.json, agent/transcript.json and, per inspected
	// page, agent/pages/NN/{capture.json,desktop.png,mobile.png}.
	agentDir       = "agent"
	runFile        = "run.json"
	transcriptFile = "transcript.json"
)

// AgentPageDir is the directory of the agent page at index i, relative to
// the snapshot. Transcript image paths are relative to agent/, so they read
// pages/NN/desktop.png (see agent.ShotPath).
func AgentPageDir(i int) string { return filepath.Join(agentDir, "pages", fmt.Sprintf("%02d", i+1)) }

// Dir returns root/<host>/<UTC timestamp>.
func Dir(root, siteURL string, ts time.Time) string {
	host := "unknown"
	if u, err := url.Parse(siteURL); err == nil && u.Host != "" {
		host = strings.NewReplacer(":", "_", "/", "_", `\`, "_").Replace(u.Host)
	}
	return filepath.Join(root, host, ts.UTC().Format("20060102T150405Z"))
}

func writeJSON(dir, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644)
}

func readJSON(dir, name string, v any) error {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Write stores the snapshot in a new directory under root and returns it.
func Write(root string, s *Snapshot) (string, error) {
	dir := Dir(root, s.Meta.URL, s.Meta.Timestamp)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("snapshot %s already exists", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := writeJSON(dir, metaFile, s.Meta); err != nil {
		return "", err
	}
	if s.Capture != nil {
		if err := writeJSON(dir, captureFile, s.Capture); err != nil {
			return "", err
		}
		files := map[string][]byte{pageFile: []byte(s.Capture.Home.HTML), desktopFile: s.Capture.DesktopPNG, mobileFile: s.Capture.MobilePNG}
		for name, b := range files {
			if len(b) == 0 {
				continue
			}
			if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
				return "", err
			}
		}
	}
	if s.Probes != nil {
		if err := writeJSON(dir, probesFile, s.Probes); err != nil {
			return "", err
		}
	}
	if len(s.PSI) > 0 {
		if err := os.WriteFile(filepath.Join(dir, psiFile), s.PSI, 0o644); err != nil {
			return "", err
		}
	}
	if err := writeJSON(dir, checksFile, s.Checks); err != nil {
		return "", err
	}
	if s.Assessment != nil {
		if err := writeJSON(dir, assessmentFile, s.Assessment); err != nil {
			return "", err
		}
	}
	if err := writeAgent(dir, s); err != nil {
		return "", err
	}
	return dir, nil
}

func writeAgent(dir string, s *Snapshot) error {
	if s.AgentRun == nil {
		return nil
	}
	ad := filepath.Join(dir, agentDir)
	if err := os.MkdirAll(ad, 0o755); err != nil {
		return err
	}
	if err := writeJSON(ad, runFile, s.AgentRun); err != nil {
		return err
	}
	if s.Transcript != nil {
		if err := writeJSON(ad, transcriptFile, s.Transcript); err != nil {
			return err
		}
	}
	if s.Capture == nil {
		return nil
	}
	for i, page := range s.Capture.Agent {
		pd := filepath.Join(dir, AgentPageDir(i))
		if err := os.MkdirAll(pd, 0o755); err != nil {
			return err
		}
		if err := writeJSON(pd, captureFile, page); err != nil {
			return err
		}
		if i >= len(s.Capture.AgentShots) {
			continue
		}
		shot := s.Capture.AgentShots[i]
		for name, b := range map[string][]byte{desktopFile: shot.Desktop, mobileFile: shot.Mobile} {
			if len(b) == 0 {
				continue
			}
			if err := os.WriteFile(filepath.Join(pd, name), b, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteReport stores the report produced by the live run next to its inputs.
func WriteReport(dir string, r *report.Report) error { return writeJSON(dir, ReportFile, r) }

// Read loads a snapshot directory. Missing optional files are fine; a file
// that exists but cannot be read or parsed is an error.
func Read(dir string) (*Snapshot, error) {
	s := &Snapshot{}
	if err := readJSON(dir, metaFile, &s.Meta); err != nil {
		return nil, fmt.Errorf("not a snapshot directory: %w", err)
	}
	var c capture.Result
	if ok, err := readOptional(dir, captureFile, &c); err != nil {
		return nil, err
	} else if ok {
		if c.DesktopPNG, err = readOptionalFile(dir, desktopFile); err != nil {
			return nil, err
		}
		if c.MobilePNG, err = readOptionalFile(dir, mobileFile); err != nil {
			return nil, err
		}
		s.Capture = &c
	}
	var p checks.Probes
	if ok, err := readOptional(dir, probesFile, &p); err != nil {
		return nil, err
	} else if ok {
		s.Probes = &p
	}
	psi, err := readOptionalFile(dir, psiFile)
	if err != nil {
		return nil, err
	}
	s.PSI = psi
	if _, err := readOptional(dir, checksFile, &s.Checks); err != nil {
		return nil, err
	}
	var a report.Assessment
	if ok, err := readOptional(dir, assessmentFile, &a); err != nil {
		return nil, err
	} else if ok {
		s.Assessment = &a
	}
	if err := readAgent(dir, s); err != nil {
		return nil, err
	}
	return s, nil
}

func readAgent(dir string, s *Snapshot) error {
	var run AgentRun
	if ok, err := readOptional(dir, filepath.Join(agentDir, runFile), &run); err != nil || !ok {
		return err
	}
	s.AgentRun = &run
	var tr agent.Transcript
	if ok, err := readOptional(dir, filepath.Join(agentDir, transcriptFile), &tr); err != nil {
		return err
	} else if ok {
		s.Transcript = &tr
	}
	if s.Capture == nil {
		return nil
	}
	for i := 0; ; i++ {
		pd := AgentPageDir(i)
		var page capture.Page
		if ok, err := readOptional(dir, filepath.Join(pd, captureFile), &page); err != nil || !ok {
			return err
		}
		var shot capture.Shot
		var err error
		if shot.Desktop, err = readOptionalFile(dir, filepath.Join(pd, desktopFile)); err != nil {
			return err
		}
		if shot.Mobile, err = readOptionalFile(dir, filepath.Join(pd, mobileFile)); err != nil {
			return err
		}
		s.Capture.Agent = append(s.Capture.Agent, page)
		s.Capture.AgentShots = append(s.Capture.AgentShots, shot)
	}
}

// readOptional reads dir/name into v and reports whether the file exists.
// Errors name the file relative to the snapshot directory.
func readOptional(dir, name string, v any) (bool, error) {
	err := readJSON(dir, name, v)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	}
	return false, fmt.Errorf("%s: %w", filepath.ToSlash(name), err)
}

// readOptionalFile reads dir/name; a missing file gives nil.
func readOptionalFile(dir, name string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.ToSlash(name), err)
	}
	return b, nil
}
