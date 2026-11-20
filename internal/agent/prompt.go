package agent

import "github.com/napet/webaudit-agent/internal/assess"

// PromptFile is the current agent prompt, in the prompts directory. Older
// versions stay in place so recorded runs still name a prompt that exists.
const PromptFile = "agent.v2.md"

// Prompt is the agent's versioned system prompt, with the datedness rubric
// appended unchanged so the baseline and the agent rate against the same text.
type Prompt struct {
	Version       string
	RubricVersion string
	Text          string
}

// LoadPrompt reads a prompt file named like agent.v1.md and appends the
// rubric. The version is the part of the name between the last two dots.
func LoadPrompt(path string, rubric assess.Rubric) (Prompt, error) {
	// LoadRubric parses the same <name>.<version>.md scheme.
	p, err := assess.LoadRubric(path)
	if err != nil {
		return Prompt{}, err
	}
	return Prompt{Version: p.Version, RubricVersion: rubric.Version, Text: p.Text + rubric.Text}, nil
}
