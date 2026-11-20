# datedness-assessment Specification

## Purpose

Uses one LLM call to make the single judgment heuristics can't: how visually dated a site looks to a visitor, rated on a fixed rubric with reasons that can be checked against the evidence.

## Requirements

### Requirement: Single structured LLM call
In the default mode the system SHALL make exactly one LLM call per live audit, sending the two screenshots, the trimmed DOM summary, and the deterministic check results, and SHALL receive the result as structured output validated against a schema. In agent mode the assessment is submitted through the same schema at the end of the agent loop, with one LLM call per turn, as specified by the agent-assessment capability.

#### Scenario: One call per audit
- **WHEN** a live audit runs in the default mode
- **THEN** exactly one LLM request is made for the assessment

#### Scenario: Output fails validation
- **WHEN** the model's output does not match the schema in the default mode
- **THEN** the system retries once, and if it fails again marks the assessment as failed in the report

#### Scenario: Agent mode
- **WHEN** a live audit runs in agent mode
- **THEN** the assessment uses the same rubric and schema as the default mode, and the report records the total tokens and cost of all agent turns

### Requirement: Datedness rubric
The model SHALL rate visual datedness as an integer from 1 (looks current) to 5 (looks very outdated) against a written rubric with anchor descriptions for each level, stored in the repository as versioned text.

#### Scenario: Rating in range
- **WHEN** the assessment succeeds
- **THEN** the report contains an integer rating from 1 to 5 and the rubric version used

### Requirement: Reasons tied to evidence
The assessment SHALL include two to six short reasons, each naming the visible element or check result it is based on, and a confidence level of low, medium or high. An assessment with fewer than two reasons, or with a placeholder reason, SHALL be rejected with an error that names the reason and says what a reason must contain. The default mode retries once; agent mode returns the error as the `submit_assessment` result.

#### Scenario: Reasons present
- **WHEN** the assessment succeeds
- **THEN** each reason names what it refers to (for example "hero image uses a 2000s-era stock photo style" or "check: table-based layout")

#### Scenario: Placeholder reason rejected
- **WHEN** the model submits a rating with the single reason subject "x", detail "placeholder"
- **THEN** the submission is rejected, the error names reason 1 and says a reason needs a named element or check and a sentence about it, and no assessment is recorded from that submission

#### Scenario: What counts as a placeholder
- **WHEN** a reason's subject is under three characters or a word such as "placeholder", "todo" or "n/a", or its detail is under three words
- **THEN** the reason is a placeholder and the assessment is rejected

### Requirement: Judgment scope
The assessment SHALL rate only visual datedness. It SHALL NOT raise flags, change check results, judge legal compliance, or produce the overall score.

#### Scenario: Model mentions a missing impresszum
- **WHEN** the model's reasons mention legal topics
- **THEN** the rating is still recorded, but no legal gap or flag is created from the model's output

### Requirement: Cost and model recorded
The report SHALL record the model ID, input and output token counts, and the estimated cost of the call.

#### Scenario: Usage recorded
- **WHEN** the assessment call completes
- **THEN** the model ID, token counts and estimated cost appear in the report
