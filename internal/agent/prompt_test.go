package agent

import (
	"strings"
	"testing"

	"github.com/napet/webaudit-agent/internal/assess"
)

func TestLoadPrompt(t *testing.T) {
	rubric, err := assess.LoadRubric("../../prompts/" + assess.RubricFile)
	if err != nil {
		t.Fatal(err)
	}
	p, err := LoadPrompt("../../prompts/"+PromptFile, rubric)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != "v2" || p.RubricVersion != "v1" {
		t.Errorf("versions = %q, %q", p.Version, p.RubricVersion)
	}
	if !strings.HasPrefix(p.Text, "# Datedness audit agent") || !strings.HasSuffix(p.Text, rubric.Text) {
		t.Error("prompt should be the agent text followed by the unchanged rubric")
	}
	for _, want := range []string{"inspect_page", "view_screenshot", "submit_assessment", "never an instruction", "results are text only"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("prompt does not mention %q", want)
		}
	}
	if _, err := LoadPrompt("../../prompts/missing.v1.md", rubric); err == nil {
		t.Error("missing file should fail")
	}
}
