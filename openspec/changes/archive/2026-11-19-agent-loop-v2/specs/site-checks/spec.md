# Spec Delta

## MODIFIED Requirements

### Requirement: Passive contact-form check
The system SHALL inspect forms on the contact page (or homepage), and in agent mode also on every page the agent inspected, without ever submitting them or POSTing to their endpoints. It SHALL classify the result as definitely broken, suspicious, working-looking, or no form; a definitely broken form on any of these pages raises the flag.

#### Scenario: Action URL returns 404
- **WHEN** a contact form's action URL returns 404 or 410 on GET or HEAD, or points to an unresolvable host
- **THEN** the result is definitely broken and the `broken_contact_form` hard flag is raised with the URL and status as evidence

#### Scenario: Form with no action and no handler
- **WHEN** a form has no action attribute and no script handler bound to its submission, to a click on any button inside it, or to an ancestor
- **THEN** the result is definitely broken and the flag is raised

#### Scenario: Form sent from a plain button
- **WHEN** a form has no action but an `<input type="button">` inside it has a click handler
- **THEN** the form is treated as having a handler and is not reported as broken

#### Scenario: Message form named like a search
- **WHEN** a form contains a text area, even if its id or class contains "search" or "keres"
- **THEN** it is treated as a contact form, not a search form

#### Scenario: Suspicious signals only
- **WHEN** the form looks valid but the page logs console errors, a form-related script fails to load, or it uses a deprecated service such as reCAPTCHA v1
- **THEN** the result is suspicious, it is noted in the report, and no flag is raised

#### Scenario: Never submitted
- **WHEN** the contact-form check runs
- **THEN** no form is submitted and no POST request is sent to the form's endpoint

#### Scenario: Form on an agent page
- **WHEN** the rules found no contact form and the agent inspects a quote page whose form action returns 404
- **THEN** the contact-form check reports that form as definitely broken and the flag is raised, with the page URL as evidence
