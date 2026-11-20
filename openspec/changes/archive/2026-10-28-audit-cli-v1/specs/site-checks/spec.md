# Spec Delta

## Purpose

Runs every non-LLM check on a captured site and reports each result with evidence, including the objective hard-failure flags, the passive contact-form check, and Hungarian "possible legal gaps".

## ADDED Requirements

### Requirement: Check result shape
Every check SHALL report a status of `pass`, `fail`, `suspicious`, `not_applicable` or `error`, plus evidence and a short English explanation. A check that errors SHALL NOT be reported as passed or failed.

#### Scenario: Check errors
- **WHEN** a check cannot complete (for example a timeout)
- **THEN** it is reported with status `error` and the error message, and it contributes nothing to flags or scores

### Requirement: HTTPS hard flag
The system SHALL raise the `no_https` hard flag when the site is not served over HTTPS, when HTTP does not redirect to HTTPS, or when the TLS certificate is invalid, expired or does not match the host.

#### Scenario: Expired certificate
- **WHEN** the site's certificate has expired
- **THEN** the `no_https` flag is raised with the certificate's expiry date as evidence

#### Scenario: HTTP not redirected
- **WHEN** `http://` serves the page with HTTP 200 instead of redirecting to `https://`
- **THEN** the `no_https` flag is raised

### Requirement: Site error or slow hard flag
The system SHALL raise the `site_error_or_slow` hard flag when the homepage returns HTTP 5xx or cannot be loaded, or when its measured load time exceeds 10 seconds.

#### Scenario: Very slow load
- **WHEN** the homepage takes more than 10 seconds to load
- **THEN** the `site_error_or_slow` flag is raised with the measured time as evidence

### Requirement: Mobile usability hard flag
The system SHALL raise the `not_mobile_usable` hard flag when the homepage has no responsive viewport meta tag, or when visible content on the mobile render reaches more than 20px past the screen edge, or when Lighthouse reports tap targets or font sizes as failing. Content hidden by an ancestor that clips overflow (such as carousel slides) SHALL NOT count as overflow. Overflow of 20px or less SHALL be reported as suspicious without a flag.

#### Scenario: No viewport meta tag
- **WHEN** the homepage has no `<meta name="viewport">` with `width=device-width`
- **THEN** the `not_mobile_usable` flag is raised

#### Scenario: Hidden carousel slides
- **WHEN** a carousel track is wider than the phone screen but its container hides the slides outside the screen
- **THEN** the slides do not count as overflow and no flag is raised

#### Scenario: Slight overflow
- **WHEN** visible content reaches 7px past a 390px phone screen
- **THEN** the mobile check is suspicious, the width is recorded as evidence, and no flag is raised

### Requirement: Passive contact-form check
The system SHALL inspect forms on the contact page (or homepage) without ever submitting them or POSTing to their endpoints. It SHALL classify the result as definitely broken, suspicious, working-looking, or no form.

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

### Requirement: PageSpeed Insights results
The system SHALL request the PageSpeed Insights mobile strategy for the homepage and record Lighthouse performance, accessibility, best-practices and SEO scores, plus real-user field metrics when Google has field data for the site.

#### Scenario: No field data
- **WHEN** PageSpeed Insights returns lab data but no field data
- **THEN** the lab scores are recorded and field data is marked absent, not as a failure

### Requirement: Accessibility scan
The system SHALL run an axe-core scan in the loaded homepage and record violations grouped by impact level with counts and example selectors.

#### Scenario: Violations found
- **WHEN** axe-core reports violations
- **THEN** the report lists each violation rule with its impact, node count and up to a few example selectors

### Requirement: Staleness signals
The system SHALL detect staleness signals and record each with evidence: a copyright year at least two years older than the current year, a jQuery version older than 3.0, table-based layout, Flash or other obsolete embeds, and missing modern HTML5 doctype.

#### Scenario: Old copyright year
- **WHEN** the footer says "© 2016" and the current year is 2026
- **THEN** a stale-copyright signal is recorded with the matched text

#### Scenario: Old jQuery
- **WHEN** the page loads jQuery 1.8.3
- **THEN** an old-jQuery signal is recorded with the script URL and version

### Requirement: Possible legal gaps
For locales that define them, the system SHALL run legal-gap checks and report each as "could not find X" with evidence of what was searched. Legal gaps SHALL NOT raise hard flags.

#### Scenario: No provider details found
- **WHEN** the `hu` locale is active and neither the homepage, contact page nor legal-information page contains the provider details the locale config lists (such as company name, address, registration and tax number)
- **THEN** a "could not find provider details (impresszum)" gap is reported, listing the pages searched, and no hard flag is raised

#### Scenario: Details in the privacy notice
- **WHEN** the site has no impresszum page but links its terms (ÁSZF) or privacy notice
- **THEN** that page is searched for provider details in place of the legal-information page, and nothing is claimed about the terms themselves

#### Scenario: No privacy notice with a form present
- **WHEN** the site has a contact form and no link to a privacy notice (*adatkezelési tájékoztató*) is found
- **THEN** a "could not find privacy notice" gap is reported

#### Scenario: Tracking before consent
- **WHEN** analytics or advertising cookies are set, or known tracker requests are made, before any user interaction
- **THEN** a "tracking before consent" gap is reported with the cookie names, or the host and path of tracker requests (without query strings), as evidence

### Requirement: Service businesses only
The v1 checks SHALL NOT include webshop-specific checks such as terms of sale (ÁSZF), checkout or payment pages.

#### Scenario: Webshop site
- **WHEN** a webshop is audited
- **THEN** only the general checks run, and the report does not claim anything about webshop-specific obligations
