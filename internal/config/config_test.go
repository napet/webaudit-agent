package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoConfig = "../../config"

func TestLoadRepoConfig(t *testing.T) {
	l, err := LoadLocale(repoConfig, "hu")
	if err != nil {
		t.Fatal(err)
	}
	if l.Legal == nil || len(l.Legal.ProviderDetails.Compiled) == 0 {
		t.Fatal("hu locale should have compiled legal patterns")
	}
	if !l.Legal.ProviderDetails.Compiled["tax_number"].MatchString("Adószám: 12345678-2-41") {
		t.Error("tax number pattern should match")
	}
	s, err := LoadScoring(repoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Models[s.DefaultModel].Cost(1_000_000, 0); got <= 0 {
		t.Errorf("default model cost = %v", got)
	}
	if s.Agent != DefaultAgentBudgets {
		t.Errorf("repo agent budgets = %+v, want %+v", s.Agent, DefaultAgentBudgets)
	}
}

func TestAgentBudgetDefaults(t *testing.T) {
	base := "weights: {mobile: 1}\ndefault_model: m\nmodels: {m: {}}\n"
	dir := t.TempDir()
	write(t, dir, "scoring.yaml", base)
	s, err := LoadScoring(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Agent != DefaultAgentBudgets {
		t.Errorf("no agent block: got %+v, want defaults", s.Agent)
	}
	write(t, dir, "scoring.yaml", base+"agent: {max_pages: 2}")
	s, err = LoadScoring(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultAgentBudgets
	want.MaxPages = 2
	if s.Agent != want {
		t.Errorf("partial agent block: got %+v, want %+v", s.Agent, want)
	}
}

func TestUnknownLocale(t *testing.T) {
	_, err := LoadLocale(repoConfig, "xx")
	if !errors.Is(err, ErrUnknownLocale) {
		t.Fatalf("want ErrUnknownLocale, got %v", err)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedFilesFailCleanly(t *testing.T) {
	cases := map[string]struct{ file, body, want string }{
		"bad yaml":       {"scoring.yaml", "weights: [", "scoring.yaml"},
		"unknown weight": {"scoring.yaml", "weights: {colour: 1}\ndefault_model: m\nmodels: {m: {}}", "unknown weight"},
		"all zero":       {"scoring.yaml", "weights: {mobile: 0}\ndefault_model: m\nmodels: {m: {}}", "positive"},
		"no price":       {"scoring.yaml", "weights: {mobile: 1}\ndefault_model: m", "no price"},
		"unknown field":  {"scoring.yaml", "weights: {mobile: 1}\ndefault_model: m\nmodels: {m: {}}\nextra: 1", "extra"},
		"bad regex":      {"locales/hu.yaml", "code: hu\ncontact: {paths: [/k]}\nlegal: {provider_details: {min_matches: 1, patterns: {a: '('}}}", "pattern a"},
		"wrong code":     {"locales/hu.yaml", "code: de\ncontact: {paths: [/k]}", "want \"hu\""},
		"zero pages":     {"scoring.yaml", "weights: {mobile: 1}\ndefault_model: m\nmodels: {m: {}}\nagent: {max_pages: 0}", "agent budgets"},
		"negative shots": {"scoring.yaml", "weights: {mobile: 1}\ndefault_model: m\nmodels: {m: {}}\nagent: {max_screenshots: -1}", "agent budgets"},
		"zero turns":     {"scoring.yaml", "weights: {mobile: 1}\ndefault_model: m\nmodels: {m: {}}\nagent: {max_turns: 0}", "agent budgets"},
		"zero cost":      {"scoring.yaml", "weights: {mobile: 1}\ndefault_model: m\nmodels: {m: {}}\nagent: {max_cost_usd: 0}", "agent budgets"},
		"agent typo":     {"scoring.yaml", "weights: {mobile: 1}\ndefault_model: m\nmodels: {m: {}}\nagent: {max_page: 3}", "max_page"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, c.file, c.body)
			var err error
			if strings.HasPrefix(c.file, "locales") {
				_, err = LoadLocale(dir, "hu")
			} else {
				_, err = LoadScoring(dir)
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}
