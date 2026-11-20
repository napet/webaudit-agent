// Package score turns check results and the datedness rating into a ranking
// that is reproducible and explainable: hard flags decide the tier, and a
// weighted average of sub-scores ranks sites within a tier.
package score

import (
	"math"

	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
)

// FlagOrder is the canonical order flags are reported in.
var FlagOrder = []string{report.FlagSiteErrorOrSlow, report.FlagNoHTTPS, report.FlagBrokenContactForm, report.FlagNotMobileUsable}

// Input is everything scoring reads.
type Input struct {
	Checks        []report.CheckResult
	PageSpeed     *report.PageSpeed
	Accessibility *report.Accessibility
	Assessment    *report.Assessment
	Weights       map[string]float64
}

// Result is the ranking part of the report.
type Result struct {
	Tier      int
	Score     float64 // 0 good .. 100 bad
	SubScores map[string]float64
	Missing   []string
	Flags     []string
}

// Compute scores a site. It is a pure function of its input.
func Compute(in Input) Result {
	r := Result{Tier: 2, SubScores: map[string]float64{}}

	raised := map[string]bool{}
	for _, c := range in.Checks {
		if c.Flag != "" && c.Status == report.StatusFail {
			raised[c.Flag] = true
		}
	}
	for _, f := range FlagOrder {
		if raised[f] {
			r.Flags = append(r.Flags, f)
		}
	}
	if len(r.Flags) > 0 {
		r.Tier = 1
	}

	subs := map[string]*float64{
		report.CatPerformance:   performance(in.PageSpeed),
		report.CatAccessibility: accessibility(in.PageSpeed, in.Accessibility),
		report.CatMobile:        binary(in.Checks, "mobile_usability"),
		report.CatSecurity:      binary(in.Checks, "https"),
		report.CatStaleness:     failShare(in.Checks, report.CatStaleness),
		report.CatLegal:         failShare(in.Checks, report.CatLegal),
		report.CatDatedness:     datedness(in.Assessment),
	}

	// Iterate in a fixed order so floating-point sums are identical run to run.
	var sum, wsum float64
	for _, name := range config.SubScoreNames {
		w := in.Weights[name]
		v := subs[name]
		if v == nil {
			if w > 0 {
				r.Missing = append(r.Missing, name)
			}
			continue
		}
		r.SubScores[name] = round1(*v)
		if w > 0 {
			sum += w * *v
			wsum += w
		}
	}
	if wsum > 0 {
		r.Score = round1(sum / wsum)
	}
	return r
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func ptr(v float64) *float64 { return &v }

func performance(ps *report.PageSpeed) *float64 {
	if ps == nil {
		return nil
	}
	s, ok := ps.Scores["performance"]
	if !ok {
		return nil
	}
	return ptr((1 - s) * 100)
}

// accessibility averages Lighthouse's accessibility score and a weighted
// count of axe violations, using whichever is available.
func accessibility(ps *report.PageSpeed, ax *report.Accessibility) *float64 {
	var parts []float64
	if ps != nil {
		if s, ok := ps.Scores["accessibility"]; ok {
			parts = append(parts, (1-s)*100)
		}
	}
	if ax != nil {
		v := 10*float64(ax.ByImpact["critical"]+ax.ByImpact["serious"]) +
			3*float64(ax.ByImpact["moderate"]) + float64(ax.ByImpact["minor"])
		parts = append(parts, math.Min(100, v))
	}
	if len(parts) == 0 {
		return nil
	}
	var sum float64
	for _, p := range parts {
		sum += p
	}
	return ptr(sum / float64(len(parts)))
}

func binary(checks []report.CheckResult, id string) *float64 {
	for _, c := range checks {
		if c.ID != id {
			continue
		}
		switch c.Status {
		case report.StatusFail:
			return ptr(100)
		case report.StatusPass:
			return ptr(0)
		}
	}
	return nil
}

// failShare is the share of failing checks among the applicable checks of a
// category. Errored and not-applicable checks don't count either way.
func failShare(checks []report.CheckResult, cat string) *float64 {
	var fail, total int
	for _, c := range checks {
		if c.Category != cat {
			continue
		}
		switch c.Status {
		case report.StatusFail:
			fail++
			total++
		case report.StatusPass, report.StatusSuspicious:
			total++
		}
	}
	if total == 0 {
		return nil
	}
	return ptr(100 * float64(fail) / float64(total))
}

func datedness(a *report.Assessment) *float64 {
	if a == nil || a.Rating < 1 || a.Rating > 5 {
		return nil
	}
	return ptr(float64(a.Rating-1) * 25)
}
