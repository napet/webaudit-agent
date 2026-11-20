// Package report defines the JSON audit report: the contract every later
// piece (eval, dashboard, owner report, agent loop) reads.
package report

import "time"

// SchemaVersion identifies the report format. Bump it on any breaking change.
const SchemaVersion = "1"

// Hard flags. Any of these puts a site in tier 1.
const (
	FlagNoHTTPS           = "no_https"
	FlagSiteErrorOrSlow   = "site_error_or_slow"
	FlagNotMobileUsable   = "not_mobile_usable"
	FlagBrokenContactForm = "broken_contact_form"
)

// Status is the outcome of one check.
type Status string

// The check statuses.
const (
	StatusPass          Status = "pass"
	StatusFail          Status = "fail"
	StatusSuspicious    Status = "suspicious"
	StatusNotApplicable Status = "not_applicable"
	StatusError         Status = "error"
)

// Check categories, which are also the deterministic sub-score names.
const (
	CatPerformance   = "performance"
	CatAccessibility = "accessibility"
	CatMobile        = "mobile"
	CatSecurity      = "security"
	CatStaleness     = "staleness"
	CatLegal         = "legal"
	CatContact       = "contact"
	CatDatedness     = "datedness" // sub-score only; comes from the LLM
)

// CheckResult is one deterministic finding with its evidence.
type CheckResult struct {
	ID          string         `json:"id"`
	Category    string         `json:"category"`
	Status      Status         `json:"status"`
	Flag        string         `json:"flag,omitempty"` // hard flag raised by this result
	Explanation string         `json:"explanation"`
	Evidence    map[string]any `json:"evidence,omitempty"`
	Error       string         `json:"error,omitempty"`
}

// Component records whether a pipeline component (pagespeed, assessment,
// capture, ...) succeeded, so partial reports say what is missing and why.
type Component struct {
	Status string `json:"status"` // "ok", "failed" or "skipped"
	Error  string `json:"error,omitempty"`
	// Usage is what a failed LLM call still cost; a successful one reports
	// it in the assessment.
	Usage *Usage `json:"usage,omitempty"`
}

// Usage is the token usage and estimated cost of LLM calls.
type Usage struct {
	Model        string  `json:"model"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// PageSpeed holds the PageSpeed Insights results that feed sub-scores.
type PageSpeed struct {
	Strategy string `json:"strategy"`
	// Lighthouse category scores, 0..1 (1 is best).
	Scores map[string]float64 `json:"scores"`
	// Real-user field metrics keyed by metric name; nil when Google has none.
	Field map[string]FieldMetric `json:"field,omitempty"`
	// Audits used by other checks, keyed by Lighthouse audit ID; score 0..1,
	// nil when not applicable.
	Audits map[string]*float64 `json:"audits,omitempty"`
}

// FieldMetric is one real-user (CrUX) metric from PageSpeed Insights.
type FieldMetric struct {
	Percentile float64 `json:"percentile"`
	Category   string  `json:"category"` // FAST, AVERAGE, SLOW
}

// Accessibility summarizes the axe-core scan.
type Accessibility struct {
	AxeVersion string         `json:"axe_version"`
	ByImpact   map[string]int `json:"by_impact"` // impact -> violating node count
	Violations []AxeViolation `json:"violations"`
}

// AxeViolation is one violated axe-core rule with up to three example nodes.
type AxeViolation struct {
	Rule     string   `json:"rule"`
	Impact   string   `json:"impact"`
	Nodes    int      `json:"nodes"`
	Examples []string `json:"examples,omitempty"`
}

// Reason is one piece of the datedness judgment, naming what it refers to.
type Reason struct {
	Subject string `json:"subject"` // visible element or "check: <id>"
	Detail  string `json:"detail"`
}

// Assessment is the LLM's visual datedness judgment.
type Assessment struct {
	Rating        int      `json:"rating"` // 1 current .. 5 very outdated
	Confidence    string   `json:"confidence"`
	Reasons       []Reason `json:"reasons"`
	RubricVersion string   `json:"rubric_version"`
	Model         string   `json:"model"`
	InputTokens   int64    `json:"input_tokens"`
	OutputTokens  int64    `json:"output_tokens"`
	CostUSD       float64  `json:"cost_usd"`
}

// Report is the full audit of one site.
type Report struct {
	SchemaVersion string    `json:"schema_version"`
	URL           string    `json:"url"`
	FinalURL      string    `json:"final_url"`
	Timestamp     time.Time `json:"timestamp"`
	Locale        string    `json:"locale"`

	Tier             int                `json:"tier"`
	Score            float64            `json:"score"` // within-tier, 0 good .. 100 bad
	SubScores        map[string]float64 `json:"sub_scores"`
	MissingSubScores []string           `json:"missing_sub_scores,omitempty"`
	Weights          map[string]float64 `json:"weights"`
	Flags            []string           `json:"flags"`

	Checks        []CheckResult  `json:"checks"`
	LegalGaps     []CheckResult  `json:"legal_gaps"`
	PageSpeed     *PageSpeed     `json:"pagespeed,omitempty"`
	Accessibility *Accessibility `json:"accessibility,omitempty"`
	Assessment    *Assessment    `json:"assessment,omitempty"`
	// Agent is set only in agent mode.
	Agent *Agent `json:"agent,omitempty"`

	ContactPageURL string               `json:"contact_page_url,omitempty"`
	LegalPageURL   string               `json:"legal_page_url,omitempty"`
	Components     map[string]Component `json:"components"`
	Notes          []string             `json:"notes,omitempty"`

	CostUSD      float64 `json:"cost_usd"`
	DurationMS   int64   `json:"duration_ms"`
	SnapshotPath string  `json:"snapshot_path,omitempty"`
}

// Agent is the agent's part of an audit: the pages it inspected and why,
// what it cost, why it stopped, and what changed compared with the baseline result
// of the same run.
type Agent struct {
	PromptVersion string `json:"prompt_version,omitempty"`
	Model         string `json:"model,omitempty"`
	// StoppedBy is submit, turn_limit, cost_limit, refusal or api_error; ""
	// when the agent did not run, and Error says why.
	StoppedBy string `json:"stopped_by,omitempty"`
	Error     string `json:"error,omitempty"`

	Pages             []AgentPage  `json:"pages"`
	ScreenshotsViewed int          `json:"screenshots_viewed"`
	Budgets           AgentBudgets `json:"budgets"`

	Turns        int     `json:"turns"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`

	Changed AgentChanges `json:"changed"`
}

// AgentPage is one page the agent inspected.
type AgentPage struct {
	URL      string `json:"url"`
	Reason   string `json:"reason"`
	FinalURL string `json:"final_url,omitempty"`
	Status   int    `json:"status,omitempty"`
	Error    string `json:"error,omitempty"` // why the page gave nothing to check
}

// AgentBudgets are the limits the run had.
type AgentBudgets struct {
	MaxPages       int     `json:"max_pages"`
	MaxScreenshots int     `json:"max_screenshots"`
	MaxTurns       int     `json:"max_turns"`
	MaxCostUSD     float64 `json:"max_cost_usd"`
}

// AgentChanges compares the final result with the baseline of the same run.
type AgentChanges struct {
	Checks            []CheckChange `json:"checks"`
	LegalGapsResolved []string      `json:"legal_gaps_resolved"`
	LegalGapsAdded    []string      `json:"legal_gaps_added"`
	// Rating is the datedness rating before and after; nil when there was none.
	Rating struct {
		From *int `json:"from"`
		To   *int `json:"to"`
	} `json:"rating"`
	Tier struct {
		From int `json:"from"`
		To   int `json:"to"`
	} `json:"tier"`
}

// CheckChange is a check whose status changed.
type CheckChange struct {
	ID   string `json:"id"`
	From Status `json:"from"`
	To   Status `json:"to"`
}
