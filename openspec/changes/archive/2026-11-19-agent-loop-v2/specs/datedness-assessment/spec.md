# Spec Delta

## MODIFIED Requirements

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
