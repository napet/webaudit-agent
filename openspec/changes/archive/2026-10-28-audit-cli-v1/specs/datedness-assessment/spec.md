# Spec Delta

## Purpose

Uses one LLM call to make the single judgment heuristics can't: how visually dated a site looks to a visitor, rated on a fixed rubric with reasons that can be checked against the evidence.

## ADDED Requirements

### Requirement: Single structured LLM call
The system SHALL make exactly one LLM call per live audit, sending the two screenshots, the trimmed DOM summary, and the deterministic check results, and SHALL receive the result as structured output validated against a schema.

#### Scenario: One call per audit
- **WHEN** a live audit runs
- **THEN** exactly one LLM request is made for the assessment

#### Scenario: Output fails validation
- **WHEN** the model's output does not match the schema
- **THEN** the system retries once, and if it fails again marks the assessment as failed in the report

### Requirement: Datedness rubric
The model SHALL rate visual datedness as an integer from 1 (looks current) to 5 (looks very outdated) against a written rubric with anchor descriptions for each level, stored in the repository as versioned text.

#### Scenario: Rating in range
- **WHEN** the assessment succeeds
- **THEN** the report contains an integer rating from 1 to 5 and the rubric version used

### Requirement: Reasons tied to evidence
The assessment SHALL include a list of short reasons, each naming the visible element or check result it is based on, and a confidence level of low, medium or high.

#### Scenario: Reasons present
- **WHEN** the assessment succeeds
- **THEN** each reason names what it refers to (for example "hero image uses a 2000s-era stock photo style" or "check: table-based layout")

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
