package snapshot

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/napet/webaudit-agent/internal/agent"
	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/report"
)

var ts = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

func baseline() *Snapshot {
	return &Snapshot{
		Meta: Meta{URL: "https://example.hu/", Timestamp: ts, Locale: "hu"},
		Capture: &capture.Result{
			Home:       capture.Page{RequestedURL: "https://example.hu/", FinalURL: "https://example.hu/", Status: 200, HTML: "<html><title>Példa</title></html>"},
			DesktopPNG: []byte("desktop-png"),
			MobilePNG:  []byte("mobile-png"),
		},
		PSI:        json.RawMessage(`{"lighthouseResult":{}}`),
		Checks:     []report.CheckResult{{ID: "https", Category: report.CatSecurity, Status: report.StatusPass}},
		Assessment: &report.Assessment{Rating: 3},
	}
}

func withAgent(s *Snapshot) *Snapshot {
	s.Meta.Agent, s.Meta.AgentPrompt = true, "v2"
	s.AgentRun = &AgentRun{V1: V1Assessment{Assessment: &report.Assessment{Rating: 2}}, StoppedBy: "submit", Turns: 3}
	s.Transcript = &agent.Transcript{}
	s.Capture.Agent = []capture.Page{
		{RequestedURL: "https://example.hu/kapcsolat", Status: 200},
		{RequestedURL: "https://example.hu/rolunk", Status: 200},
	}
	// The second page has no screenshots, as when it failed to load.
	s.Capture.AgentShots = []capture.Shot{{Desktop: []byte("p1-desktop"), Mobile: []byte("p1-mobile")}, {}}
	return s
}

func TestRoundTripBaseline(t *testing.T) {
	dir, err := Write(t.TempDir(), baseline())
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.URL != "https://example.hu/" || !got.Meta.Timestamp.Equal(ts) || got.Meta.Agent {
		t.Errorf("meta = %+v", got.Meta)
	}
	if got.Capture == nil || got.Capture.Home.HTML != "<html><title>Példa</title></html>" {
		t.Fatalf("capture = %+v", got.Capture)
	}
	if !bytes.Equal(got.Capture.DesktopPNG, []byte("desktop-png")) || !bytes.Equal(got.Capture.MobilePNG, []byte("mobile-png")) {
		t.Error("screenshots not read back")
	}
	if string(got.PSI) != `{"lighthouseResult":{}}` {
		t.Errorf("psi = %s", got.PSI)
	}
	if len(got.Checks) != 1 || got.Checks[0].ID != "https" {
		t.Errorf("checks = %+v", got.Checks)
	}
	if got.Assessment == nil || got.Assessment.Rating != 3 {
		t.Errorf("assessment = %+v", got.Assessment)
	}
	if got.AgentRun != nil || got.Transcript != nil || len(got.Capture.Agent) != 0 {
		t.Error("baseline snapshot read back with agent data")
	}
}

func TestRoundTripAgent(t *testing.T) {
	dir, err := Write(t.TempDir(), withAgent(baseline()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Meta.Agent || got.AgentRun == nil || got.AgentRun.Turns != 3 || got.AgentRun.V1.Assessment.Rating != 2 {
		t.Fatalf("agent run = %+v", got.AgentRun)
	}
	if got.Transcript == nil {
		t.Error("transcript not read back")
	}
	if len(got.Capture.Agent) != 2 || got.Capture.Agent[1].RequestedURL != "https://example.hu/rolunk" {
		t.Fatalf("agent pages = %+v", got.Capture.Agent)
	}
	if len(got.Capture.AgentShots) != 2 || string(got.Capture.AgentShots[0].Mobile) != "p1-mobile" || got.Capture.AgentShots[1].Desktop != nil {
		t.Errorf("agent shots = %q", got.Capture.AgentShots)
	}
}

func TestReadMissingOptionalFiles(t *testing.T) {
	s := baseline()
	s.PSI, s.Assessment = nil, nil
	s.Capture.MobilePNG = nil
	dir, err := Write(t.TempDir(), s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.PSI != nil || got.Assessment != nil || got.Capture.MobilePNG != nil {
		t.Errorf("missing files read as %v / %+v / %q", got.PSI, got.Assessment, got.Capture.MobilePNG)
	}
}

func TestReadNotASnapshot(t *testing.T) {
	if _, err := Read(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a snapshot directory") {
		t.Errorf("err = %v", err)
	}
}

func TestReadBrokenFiles(t *testing.T) {
	cases := []struct {
		name  string
		agent bool
		file  string
		dir   bool // replace the file with a directory, so reading it fails
	}{
		{"corrupt capture", false, captureFile, false},
		{"corrupt assessment", false, assessmentFile, false},
		{"unreadable screenshot", false, desktopFile, true},
		{"unreadable psi", false, psiFile, true},
		{"corrupt transcript", true, filepath.Join(agentDir, transcriptFile), false},
		{"corrupt agent page", true, filepath.Join(AgentPageDir(1), captureFile), false},
		{"unreadable agent screenshot", true, filepath.Join(AgentPageDir(0), mobileFile), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := baseline()
			if c.agent {
				s = withAgent(s)
			}
			dir, err := Write(t.TempDir(), s)
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, c.file)
			if c.dir {
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				err = os.Mkdir(p, 0o755)
			} else {
				err = os.WriteFile(p, []byte("{not json"), 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = Read(dir)
			if err == nil || !strings.Contains(err.Error(), filepath.ToSlash(c.file)) {
				t.Errorf("err = %v, want one naming %s", err, filepath.ToSlash(c.file))
			}
		})
	}
}

func TestWriteRefusesExistingDir(t *testing.T) {
	root := t.TempDir()
	if _, err := Write(root, baseline()); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(root, baseline()); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second write: err = %v", err)
	}
}
