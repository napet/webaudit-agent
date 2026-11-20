# Spec Delta

## ADDED Requirements

### Requirement: Opt-in agent mode
The tool SHALL run in agent mode only when given the agent flag. Without the flag, a live audit and a replay SHALL behave exactly as before this change. Replaying an agent snapshot SHALL use agent replay whether or not the flag is given.

#### Scenario: Default mode unchanged
- **WHEN** the user runs `audit https://example.hu` without the agent flag
- **THEN** the audit makes one assessment call, inspects no pages beyond the rule-based ones, and the report has no agent section

#### Scenario: Agent mode
- **WHEN** the user runs `audit --agent https://example.hu`
- **THEN** the audit runs the v1 pipeline followed by the agent, and the report contains the agent section

#### Scenario: Agent flag with a snapshot
- **WHEN** the user replays a snapshot that was not recorded in agent mode and passes the agent flag
- **THEN** the tool exits with a usage error explaining that the snapshot has no agent recording
