# Spec Delta

## Purpose

Loads an audited site the way a visitor would see it and gathers the raw material the checks and the datedness assessment need: rendered HTML, above-the-fold screenshots, a trimmed DOM summary, and the likely contact page.

## ADDED Requirements

### Requirement: Rendered page load
The system SHALL load the homepage in a headless browser, wait for the page to settle (load event plus a bounded idle wait), and record the final URL, HTTP status, load time, console errors, cookies set, and network requests made.

#### Scenario: Normal page load
- **WHEN** the homepage loads successfully
- **THEN** the rendered HTML, final URL, status, load time, console errors, cookies and request log are available to the checks

#### Scenario: Page never settles
- **WHEN** the page keeps loading past the load timeout
- **THEN** the system proceeds with what has rendered, records the timeout, and does not hang

### Requirement: Above-the-fold screenshots
The system SHALL take exactly two screenshots of the visible viewport only: desktop at 1280×800 and mobile at 390×844 with a mobile user agent and device emulation. It SHALL NOT capture full-page screenshots.

#### Scenario: Screenshots captured
- **WHEN** the homepage loads
- **THEN** one desktop and one mobile viewport screenshot are produced at the sizes above

### Requirement: Cookie banners left untouched
The system SHALL NOT click, accept or dismiss cookie consent banners or any other on-page control before screenshots and checks are taken.

#### Scenario: Consent banner present
- **WHEN** the page shows a cookie consent banner
- **THEN** the screenshots show the banner and no consent is given on the visitor's behalf

### Requirement: Trimmed DOM summary
The system SHALL build a compact text summary of the homepage for the LLM, containing the title, meta description, language, headings, navigation link texts, footer text, and detected technology hints. It SHALL stay under a fixed size limit.

#### Scenario: Large page summarized
- **WHEN** the homepage HTML is large
- **THEN** the summary contains the listed elements and is truncated to stay within the size limit

### Requirement: Rule-based contact page discovery
The system SHALL find the contact page using the locale config's URL path patterns and link-text keywords, preferring a matching link found on the homepage over a guessed path. Links include elements that navigate from an onclick handler. When several links match, the one matching the earliest keyword in the config wins. When neither a link nor a path finds the page, the locale's fallback link keywords are tried. It SHALL inspect at most one contact page and one legal-information page.

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

### Requirement: Polite fetching
The system SHALL identify itself with a descriptive user agent, use only GET and HEAD requests toward the audited site, and limit the number of requests per audit to a fixed cap.

#### Scenario: Request methods
- **WHEN** an audit runs
- **THEN** no request with a method other than GET or HEAD is sent to the audited site by the tool itself
