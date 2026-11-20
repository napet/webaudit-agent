package audit

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/napet/webaudit-agent/internal/testsite"
)

var (
	writeExample      = flag.Bool("write-example-snapshot", false, "regenerate testdata/snapshots/example from the brokenform fixture")
	writeAgentExample = flag.Bool("write-agent-example-snapshot", false, "regenerate testdata/snapshots/agent-example from the agentsite fixture")
)

// TestWriteExampleSnapshot regenerates the committed example snapshot that
// the README's replay command uses. Run with -write-example-snapshot.
func TestWriteExampleSnapshot(t *testing.T) {
	if !*writeExample {
		t.Skip("run with -write-example-snapshot to regenerate")
	}
	needChrome(t)
	site := testsite.StartTLS("brokenform")
	defer site.Close()
	base := filepath.Join("..", "..", "testdata", "snapshots")
	tmp := filepath.Join(base, ".tmp") // same drive as the destination, so Rename works
	os.RemoveAll(tmp)
	defer os.RemoveAll(tmp)
	rep, err := tlsPipeline(t, site, tmp, &fakeAssessor{rating: 3}).Run(context.Background(), site.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(base, "example")
	os.RemoveAll(dst)
	if err := os.Rename(rep.SnapshotPath, dst); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (tier %d, flags %v)", dst, rep.Tier, rep.Flags)
}

// TestWriteAgentExampleSnapshot regenerates the committed agent-mode example
// snapshot (agentsite fixture, scripted model) that the README's agent replay
// commands use. Run with -write-agent-example-snapshot.
func TestWriteAgentExampleSnapshot(t *testing.T) {
	if !*writeAgentExample {
		t.Skip("run with -write-agent-example-snapshot to regenerate")
	}
	needChrome(t)
	site := testsite.StartTLS("agentsite")
	defer site.Close()
	base := filepath.Join("..", "..", "testdata", "snapshots")
	tmp := filepath.Join(base, ".tmp")
	os.RemoveAll(tmp)
	defer os.RemoveAll(tmp)
	p, _ := agentPipeline(t, site, tmp)
	rep, err := p.Run(context.Background(), site.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(base, "agent-example")
	os.RemoveAll(dst)
	if err := os.Rename(rep.SnapshotPath, dst); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (tier %d, agent stopped by %s after %d turns)", dst, rep.Tier, rep.Agent.StoppedBy, rep.Agent.Turns)
}
