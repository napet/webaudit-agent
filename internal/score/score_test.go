package score

import (
	"reflect"
	"testing"

	"github.com/napet/webaudit-agent/internal/report"
)

func equalWeights() map[string]float64 {
	return map[string]float64{"performance": 1, "accessibility": 1, "mobile": 1, "security": 1, "staleness": 1, "legal": 1, "datedness": 1}
}

func check(id, cat string, st report.Status, flag string) report.CheckResult {
	return report.CheckResult{ID: id, Category: cat, Status: st, Flag: flag}
}

// 6.1
func TestTiers(t *testing.T) {
	one := Compute(Input{Weights: equalWeights(), Checks: []report.CheckResult{
		check("contact_form", report.CatContact, report.StatusFail, report.FlagBrokenContactForm),
		check("https", report.CatSecurity, report.StatusPass, ""),
	}})
	if one.Tier != 1 || !reflect.DeepEqual(one.Flags, []string{report.FlagBrokenContactForm}) {
		t.Errorf("one flag: %+v", one)
	}
	none := Compute(Input{Weights: equalWeights(), Checks: []report.CheckResult{
		check("contact_form", report.CatContact, report.StatusSuspicious, ""),
	}})
	if none.Tier != 2 || len(none.Flags) != 0 {
		t.Errorf("no flags: %+v", none)
	}
	many := Compute(Input{Weights: equalWeights(), Checks: []report.CheckResult{
		check("mobile_usability", report.CatMobile, report.StatusFail, report.FlagNotMobileUsable),
		check("https", report.CatSecurity, report.StatusFail, report.FlagNoHTTPS),
	}})
	if !reflect.DeepEqual(many.Flags, []string{report.FlagNoHTTPS, report.FlagNotMobileUsable}) {
		t.Errorf("flags should come in canonical order: %v", many.Flags)
	}
}

// 6.2
func TestSubScores(t *testing.T) {
	for rating, want := range map[int]float64{1: 0, 2: 25, 3: 50, 4: 75, 5: 100} {
		r := Compute(Input{Weights: equalWeights(), Assessment: &report.Assessment{Rating: rating}})
		if r.SubScores["datedness"] != want {
			t.Errorf("rating %d -> %v, want %v", rating, r.SubScores["datedness"], want)
		}
	}
	cases := []struct {
		name string
		in   Input
		sub  string
		want float64
	}{
		{"performance from PSI", Input{PageSpeed: &report.PageSpeed{Scores: map[string]float64{"performance": 0.42}}}, "performance", 58},
		{"accessibility averages PSI and axe", Input{
			PageSpeed:     &report.PageSpeed{Scores: map[string]float64{"accessibility": 0.8}},
			Accessibility: &report.Accessibility{ByImpact: map[string]int{"serious": 2, "moderate": 5}},
		}, "accessibility", (20 + 35) / 2.0},
		{"axe capped at 100", Input{Accessibility: &report.Accessibility{ByImpact: map[string]int{"critical": 40}}}, "accessibility", 100},
		{"mobile fail", Input{Checks: []report.CheckResult{check("mobile_usability", report.CatMobile, report.StatusFail, report.FlagNotMobileUsable)}}, "mobile", 100},
		{"security pass", Input{Checks: []report.CheckResult{check("https", report.CatSecurity, report.StatusPass, "")}}, "security", 0},
		{"staleness share ignores n/a and errors", Input{Checks: []report.CheckResult{
			check("stale_copyright", report.CatStaleness, report.StatusFail, ""),
			check("old_jquery", report.CatStaleness, report.StatusNotApplicable, ""),
			check("table_layout", report.CatStaleness, report.StatusPass, ""),
			check("obsolete_markup", report.CatStaleness, report.StatusError, ""),
			check("html5_doctype", report.CatStaleness, report.StatusPass, ""),
		}}, "staleness", 100.0 / 3},
		{"legal share", Input{Checks: []report.CheckResult{
			check("legal_provider_details", report.CatLegal, report.StatusFail, ""),
			check("legal_privacy_notice", report.CatLegal, report.StatusPass, ""),
		}}, "legal", 50},
	}
	for _, tc := range cases {
		tc.in.Weights = equalWeights()
		got, ok := Compute(tc.in).SubScores[tc.sub]
		if !ok || got != round1(tc.want) {
			t.Errorf("%s: %s = %v (present %v), want %v", tc.name, tc.sub, got, ok, round1(tc.want))
		}
	}
}

// 6.3
func TestWeightedScoreAndMissing(t *testing.T) {
	in := Input{
		Weights:    map[string]float64{"security": 3, "datedness": 1, "performance": 1},
		Checks:     []report.CheckResult{check("https", report.CatSecurity, report.StatusFail, report.FlagNoHTTPS)},
		Assessment: &report.Assessment{Rating: 1},
	}
	r := Compute(in)
	// performance is missing (no PSI), so weights renormalize over security and datedness.
	if r.Score != 75 || !reflect.DeepEqual(r.Missing, []string{"performance"}) {
		t.Errorf("score=%v missing=%v", r.Score, r.Missing)
	}

	in.Assessment = nil // assessment failed
	r = Compute(in)
	if r.Score != 100 || !reflect.DeepEqual(r.Missing, []string{"performance", "datedness"}) {
		t.Errorf("without assessment: score=%v missing=%v", r.Score, r.Missing)
	}

	// A zero weight leaves a sub-score in the report but out of the score.
	in.Weights = map[string]float64{"security": 0, "datedness": 1}
	in.Assessment = &report.Assessment{Rating: 3}
	r = Compute(in)
	if r.Score != 50 || r.SubScores["security"] != 100 || len(r.Missing) != 0 {
		t.Errorf("zero weight: %+v", r)
	}
}

func TestDeterministic(t *testing.T) {
	in := Input{
		Weights: map[string]float64{"performance": 0.7, "accessibility": 1.3, "mobile": 1, "security": 2.1, "staleness": 0.9, "legal": 0.4, "datedness": 1.7},
		Checks: []report.CheckResult{
			check("https", report.CatSecurity, report.StatusPass, ""),
			check("mobile_usability", report.CatMobile, report.StatusFail, report.FlagNotMobileUsable),
			check("stale_copyright", report.CatStaleness, report.StatusFail, ""),
			check("legal_privacy_notice", report.CatLegal, report.StatusPass, ""),
		},
		PageSpeed:     &report.PageSpeed{Scores: map[string]float64{"performance": 0.33, "accessibility": 0.71}},
		Accessibility: &report.Accessibility{ByImpact: map[string]int{"serious": 1, "minor": 7}},
		Assessment:    &report.Assessment{Rating: 4},
	}
	first := Compute(in)
	for i := 0; i < 200; i++ {
		if got := Compute(in); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differs: %+v vs %+v", i, got, first)
		}
	}
}
