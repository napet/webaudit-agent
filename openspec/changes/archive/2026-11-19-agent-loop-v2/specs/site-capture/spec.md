# Spec Delta

## MODIFIED Requirements

### Requirement: Rule-based contact page discovery
The system SHALL find the contact page using the locale config's URL path patterns and link-text keywords, preferring a matching link found on the homepage over a guessed path. Links include elements that navigate from an onclick handler. When several links match, the one matching the earliest keyword in the config wins. When neither a link nor a path finds the page, the locale's fallback link keywords are tried. Rule-based discovery SHALL inspect at most one contact page and one legal-information page; in agent mode, the agent may inspect further pages within its page budget.

#### Scenario: Contact link on homepage
- **WHEN** the homepage links to `/kapcsolat` with the text "Kapcsolat"
- **THEN** that page is selected as the contact page

#### Scenario: Button navigation
- **WHEN** the homepage menu is `<button onclick="window.location.href='/jelentkezes'">Jelentkezés</button>`
- **THEN** `/jelentkezes` is selected as the contact page

#### Scenario: Fallback for the legal-information page
- **WHEN** the site has no impresszum link or path, but links its privacy notice
- **THEN** the privacy notice is selected as the legal-information page

#### Scenario: Guessed path
- **WHEN** no homepage link matches, but a configured path such as `/elerhetoseg` returns HTTP 200
- **THEN** that page is selected as the contact page

#### Scenario: No contact page found
- **WHEN** neither a link nor a configured path matches
- **THEN** the report notes that no contact page was found, and contact-form checks run on the homepage only

#### Scenario: Agent adds pages
- **WHEN** an audit runs in agent mode with a page budget of 5
- **THEN** at most one contact page and one legal page are chosen by the rules, and the agent inspects at most 5 further pages
