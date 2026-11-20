// Package config loads the per-locale rules and the scoring configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/napet/webaudit-agent/internal/report"
)

// PageRule finds a page by homepage link text first, then by guessed paths,
// then by fallback link text: pages that often carry the same information
// when the page itself does not exist.
type PageRule struct {
	LinkKeywords         []string `yaml:"link_keywords"`
	Paths                []string `yaml:"paths"`
	FallbackLinkKeywords []string `yaml:"fallback_link_keywords"`
}

// ProviderDetails are the patterns for the provider details an impresszum
// lists (registration and tax numbers, email, postal address); at least
// MinMatches of them must be found.
type ProviderDetails struct {
	MinMatches int               `yaml:"min_matches"`
	Patterns   map[string]string `yaml:"patterns"`

	Compiled map[string]*regexp.Regexp `yaml:"-"`
}

// LegalRules drive the legal-gap checks of a locale.
type LegalRules struct {
	ProviderDetails     ProviderDetails `yaml:"provider_details"`
	PrivacyLinkKeywords []string        `yaml:"privacy_link_keywords"`
}

// Locale holds everything market-specific. Legal is nil for locales without
// legal-gap checks.
type Locale struct {
	Code      string      `yaml:"code"`
	Contact   PageRule    `yaml:"contact"`
	LegalPage PageRule    `yaml:"legal_page"`
	Legal     *LegalRules `yaml:"legal"`
}

// ModelPrice is a model's price in USD per million tokens.
type ModelPrice struct {
	InputPerMTok  float64 `yaml:"input_per_mtok"`
	OutputPerMTok float64 `yaml:"output_per_mtok"`
}

// Cost returns the USD cost of a call with the given token counts.
func (p ModelPrice) Cost(in, out int64) float64 {
	return float64(in)*p.InputPerMTok/1e6 + float64(out)*p.OutputPerMTok/1e6
}

// AgentBudgets caps one agent run: extra pages inspected, screenshots
// viewed, model calls, and the USD cost of those calls.
type AgentBudgets struct {
	MaxPages       int     `yaml:"max_pages" json:"max_pages"`
	MaxScreenshots int     `yaml:"max_screenshots" json:"max_screenshots"`
	MaxTurns       int     `yaml:"max_turns" json:"max_turns"`
	MaxCostUSD     float64 `yaml:"max_cost_usd" json:"max_cost_usd"`
}

// DefaultAgentBudgets applies when scoring.yaml has no agent block, or to the
// fields it leaves out.
var DefaultAgentBudgets = AgentBudgets{MaxPages: 5, MaxScreenshots: 3, MaxTurns: 12, MaxCostUSD: 0.50}

// Scoring is config/scoring.yaml: sub-score weights, models and their
// prices, and the agent budgets.
type Scoring struct {
	Weights      map[string]float64    `yaml:"weights"`
	DefaultModel string                `yaml:"default_model"`
	Models       map[string]ModelPrice `yaml:"models"`
	Agent        AgentBudgets          `yaml:"agent"`
}

// SubScoreNames lists every sub-score the scorer knows, in a stable order.
var SubScoreNames = []string{
	report.CatPerformance, report.CatAccessibility, report.CatMobile, report.CatSecurity,
	report.CatStaleness, report.CatLegal, report.CatDatedness,
}

// ErrUnknownLocale is returned when no config file exists for a locale.
var ErrUnknownLocale = errors.New("unknown locale")

func readYAML(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// LoadLocale reads <dir>/locales/<code>.yaml and validates it.
func LoadLocale(dir, code string) (*Locale, error) {
	path := filepath.Join(dir, "locales", code+".yaml")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w %q: no file %s", ErrUnknownLocale, code, path)
	}
	var l Locale
	if err := readYAML(path, &l); err != nil {
		return nil, err
	}
	if l.Code != code {
		return nil, fmt.Errorf("%s: code is %q, want %q", path, l.Code, code)
	}
	if len(l.Contact.LinkKeywords) == 0 && len(l.Contact.Paths) == 0 {
		return nil, fmt.Errorf("%s: contact needs link_keywords or paths", path)
	}
	if l.Legal != nil {
		pd := &l.Legal.ProviderDetails
		if len(pd.Patterns) == 0 || pd.MinMatches < 1 || pd.MinMatches > len(pd.Patterns) {
			return nil, fmt.Errorf("%s: legal.provider_details needs patterns and 1 <= min_matches <= len(patterns)", path)
		}
		pd.Compiled = map[string]*regexp.Regexp{}
		for name, p := range pd.Patterns {
			re, err := regexp.Compile(p)
			if err != nil {
				return nil, fmt.Errorf("%s: pattern %s: %w", path, name, err)
			}
			pd.Compiled[name] = re
		}
	}
	return &l, nil
}

// LoadScoring reads <dir>/scoring.yaml and validates it.
func LoadScoring(dir string) (*Scoring, error) {
	path := filepath.Join(dir, "scoring.yaml")
	s := Scoring{Agent: DefaultAgentBudgets}
	if err := readYAML(path, &s); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, n := range SubScoreNames {
		known[n] = true
	}
	positive := false
	for k, w := range s.Weights {
		if !known[k] {
			return nil, fmt.Errorf("%s: unknown weight %q (known: %v)", path, k, SubScoreNames)
		}
		if w < 0 {
			return nil, fmt.Errorf("%s: weight %s is negative", path, k)
		}
		positive = positive || w > 0
	}
	if !positive {
		return nil, fmt.Errorf("%s: at least one weight must be positive", path)
	}
	if _, ok := s.Models[s.DefaultModel]; !ok {
		return nil, fmt.Errorf("%s: default_model %q has no price entry", path, s.DefaultModel)
	}
	a := s.Agent
	if a.MaxPages < 1 || a.MaxScreenshots < 0 || a.MaxTurns < 1 || a.MaxCostUSD <= 0 {
		return nil, fmt.Errorf("%s: agent budgets need max_pages >= 1, max_screenshots >= 0, max_turns >= 1, max_cost_usd > 0 (got %+v)", path, a)
	}
	return &s, nil
}

// ModelNames returns the priced models, sorted.
func (s *Scoring) ModelNames() []string {
	var names []string
	for n := range s.Models {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
