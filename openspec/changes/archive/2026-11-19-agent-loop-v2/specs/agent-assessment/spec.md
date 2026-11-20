# Spec Delta

## Purpose

Extends a v1 audit with a single agent that chooses which extra pages of the site to inspect, within budgets, and then submits the visual datedness assessment, so gaps that page-discovery rules cannot foresee are covered without giving up deterministic checks and scoring.

## ADDED Requirements

### Requirement: Agent starts from the v1 result
In agent mode the system SHALL first run the complete v1 audit unchanged, then start the agent with the v1 result: the homepage screenshots, the page summary, the pages already inspected with their links, and the check results.

#### Scenario: Baseline before the agent
- **WHEN** an audit runs in agent mode
- **THEN** the homepage, the rule-based contact and legal pages and all v1 checks are processed before the first agent turn, and the first agent request contains their results

### Requirement: Agent tools
The agent SHALL have exactly three tools: inspect a page, view a screenshot of an inspected page, and submit the assessment. Submitting the assessment SHALL end the loop.

#### Scenario: Assessment submitted
- **WHEN** the agent calls the submit tool with a valid assessment
- **THEN** the loop ends and that assessment is the audit's datedness assessment

### Requirement: Compact page results
Inspecting a page SHALL return a compact text summary: HTTP status, title, main headings, forms with their contact-form check result, provider details found, links not yet inspected, and the budget used. It SHALL NOT return raw HTML or full scan dumps.

#### Scenario: Page with a quote form
- **WHEN** the agent inspects a page containing a quote form sent by script
- **THEN** the result lists the form with its check result and the page's uninspected links, as text, and no screenshot

### Requirement: Screenshots on request
Screenshots of pages other than the homepage SHALL be sent to the model only when it asks for the desktop or phone screenshot of a page it has already inspected.

#### Scenario: Screenshot before inspection
- **WHEN** the agent asks for a screenshot of a page it has not inspected
- **THEN** the tool returns an error telling it to inspect the page first, and no screenshot budget is used

### Requirement: Only linked URLs
The agent SHALL only open URLs that appeared as links on a page already inspected in this audit, the homepage included. Other URLs SHALL be refused with an error that tells the agent to choose from the listed links.

#### Scenario: Invented URL
- **WHEN** the agent asks to inspect `/impresszum` and no inspected page links to it
- **THEN** no request is made, the tool returns an error naming the rule, and no page budget is used

#### Scenario: Link to another domain
- **WHEN** an inspected page links to a quote form on another domain and the agent asks for it
- **THEN** the page is inspected

### Requirement: Agent budgets
Each agent run SHALL be limited by four configurable budgets: extra pages inspected, screenshots viewed, turns (model calls) and cost in USD. Every tool result SHALL state the remaining page and screenshot budget.

#### Scenario: Last page
- **WHEN** one page remains in the page budget
- **THEN** the tool result warns that one page is left and suggests submitting when the agent has enough

#### Scenario: Page budget used up
- **WHEN** the agent asks to inspect a page after the page budget is used up
- **THEN** no request is made and the tool returns an error telling the agent to submit the assessment now

### Requirement: Final request when limits are reached
When the turn or cost budget is reached without a submitted assessment, the system SHALL send exactly one more message asking the agent to submit now. If that reply does not submit a valid assessment, the assessment SHALL fail as in v1: component marked failed, its cost recorded, and the score computed without datedness.

#### Scenario: Turn limit without submit
- **WHEN** the agent reaches the turn limit without submitting, and ignores the final request
- **THEN** the assessment is marked failed with the reason, the cost of every turn is recorded, and the rest of the report is produced

### Requirement: Recoverable tool errors
Errors the agent can act on SHALL be returned to it as error tool results that state what happened and what to do next, and the loop SHALL continue. This covers unknown URLs, pages already inspected, pages that fail to load, screenshots of uninspected pages, used-up budgets and assessments that fail validation.

#### Scenario: Page fails to load
- **WHEN** an inspected page times out
- **THEN** the tool result says the page did not load and suggests another page or submitting, and one page of budget is used

#### Scenario: Page already inspected
- **WHEN** the agent asks to inspect a page it already inspected
- **THEN** the tool result points to the earlier result and no page budget is used

#### Scenario: Invalid assessment
- **WHEN** the agent submits an assessment that fails validation
- **THEN** the tool result names the problem, asks it to submit again, and the loop continues

### Requirement: Feedback for replies without a tool call
When the agent replies without calling a tool, or its reply is cut off by the output limit, the system SHALL keep that reply in the conversation and add a message telling the agent what to do next, instead of resending the same request. Each such exchange SHALL count as a turn.

#### Scenario: Text reply
- **WHEN** the agent replies with text only
- **THEN** the next request contains that reply followed by a message asking it to call a tool or submit the assessment

### Requirement: Unrecoverable errors stop the loop
A refusal, an API error that persists after the client's retries, or exhausted limits after the final request SHALL stop the loop without further model calls. The assessment SHALL then fail as in v1.

#### Scenario: Refusal
- **WHEN** a model reply is a refusal
- **THEN** the loop stops immediately, no further call is made, and the assessment is marked failed with the refusal category

### Requirement: Inspected pages feed the checks
Pages the agent inspected SHALL be added to the pages the deterministic checks evaluate for contact forms, provider details and privacy-notice links, also when the assessment itself fails. The agent's own output SHALL NOT raise flags, change check results or produce scores.

#### Scenario: Impresszum found by the agent
- **WHEN** the rules found no legal page and the agent inspects a page containing the provider details
- **THEN** the provider-details check passes on that page and no legal gap is reported for it

#### Scenario: Assessment failed after pages were inspected
- **WHEN** the agent inspected a contact page and then the assessment failed
- **THEN** that page's forms are still checked and can raise the broken-contact-form flag

### Requirement: Agent never submits or posts
Agent tools SHALL load pages with GET requests only, through the same request guard as v1, and SHALL never submit forms or click page controls.

#### Scenario: Form on an inspected page
- **WHEN** the agent inspects a page with a contact form
- **THEN** no form is submitted and no POST request is sent to the form's endpoint

### Requirement: Transcript recording
Every live agent run SHALL record in the snapshot a transcript of every turn (the tool called, its arguments including the agent's stated reason, the result, tokens and cost) and the captures and screenshots of every inspected page.

#### Scenario: Transcript written
- **WHEN** a live agent audit completes, whether or not the assessment succeeded
- **THEN** the snapshot contains the transcript and every inspected page, and the turn count in the transcript matches the report

### Requirement: Agent replay
Replaying an agent snapshot SHALL reuse its recorded result without any model call or network request. Replaying with a fresh assessment SHALL run the agent again where page inspection serves only pages recorded in the snapshot; other pages SHALL be refused with an error tool result, and no request SHALL be made to the site.

#### Scenario: Re-run agent offline
- **WHEN** an agent snapshot is replayed with a fresh assessment and the agent asks for a page that was not recorded
- **THEN** the tool returns an error saying the page is not available in this replay, and no request is sent to the site

### Requirement: Agent report section
The report of an agent run SHALL contain an agent section listing the inspected pages with the agent's reasons, the number of turns, the agent's cost, why the loop stopped, and what changed compared with the v1 result of the same run: check status changes, legal gaps resolved or added, the datedness rating and the tier.

#### Scenario: Agent finds the contact form
- **WHEN** the v1 result had no contact form and the agent inspects a page with a working one
- **THEN** the agent section lists the contact-form check changing from not applicable to pass, and the stop reason is the submitted assessment
