# Spec Delta

## Purpose

Turns check results and the datedness rating into a ranking that is reproducible and explainable: hard failures decide the tier, and a weighted score computed in code ranks sites within each tier.

## ADDED Requirements

### Requirement: Two-tier ranking
The system SHALL assign tier 1 to any site with at least one hard flag (`no_https`, `site_error_or_slow`, `not_mobile_usable`, `broken_contact_form`) and tier 2 to every other site. The report SHALL list which flags caused tier 1.

#### Scenario: One flag raised
- **WHEN** only `broken_contact_form` is raised
- **THEN** the site is tier 1 and the report lists `broken_contact_form` as the reason

#### Scenario: No flags
- **WHEN** no hard flag is raised
- **THEN** the site is tier 2

### Requirement: Sub-scores
The system SHALL compute sub-scores from 0 (good) to 100 (bad) for performance, accessibility, mobile, security, staleness, legal gaps, and visual datedness, each derived from the corresponding check results or rating.

#### Scenario: Datedness mapped
- **WHEN** the datedness rating is 5
- **THEN** the visual datedness sub-score is 100, and a rating of 1 maps to 0

### Requirement: Weighted within-tier score
The system SHALL compute the within-tier score as a weighted average of the available sub-scores, using weights read from configuration, and SHALL record the weights used in the report.

#### Scenario: Weights changed
- **WHEN** the weights in configuration change and a snapshot is replayed without re-running the assessment
- **THEN** the new score reflects the new weights and no LLM call is made

### Requirement: Missing components
When a sub-score cannot be computed because its component failed, the system SHALL leave it out and renormalize the remaining weights, and SHALL list the missing sub-scores in the report.

#### Scenario: Assessment failed
- **WHEN** the datedness assessment fails
- **THEN** the score is computed from the other sub-scores and the report lists visual datedness as missing

### Requirement: Deterministic scoring
Given the same check results, rating and weights, scoring SHALL always produce the same tier and score.

#### Scenario: Repeat scoring
- **WHEN** the same snapshot is replayed twice without re-running the assessment
- **THEN** both reports have identical tiers, sub-scores and scores
