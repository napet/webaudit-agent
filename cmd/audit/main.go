// Command audit audits one small-business website and prints a JSON report.
//
//	audit [--agent] [flags] <url>
//	audit --from-snapshot <dir> [--reassess] [flags]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/napet/webaudit-agent/internal/agent"
	"github.com/napet/webaudit-agent/internal/assess"
	"github.com/napet/webaudit-agent/internal/audit"
	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/psi"
	"github.com/napet/webaudit-agent/internal/report"
	"github.com/napet/webaudit-agent/internal/safehttp"
	"github.com/napet/webaudit-agent/internal/snapshot"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const (
	envAnthropic = "ANTHROPIC_API_KEY"
	envPSI       = "PSI_API_KEY"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

type options struct {
	url          string
	out          string
	locale       string
	model        string
	configDir    string
	promptsDir   string
	snapshotsDir string
	fromSnapshot string
	reassess     bool
	agent        bool
	chrome       string
}

func usage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprintf(w, "usage: audit [--agent] [flags] <url>\n       audit --from-snapshot <dir> [--reassess] [flags]\n\nflags:\n")
	fs.SetOutput(w)
	fs.PrintDefaults()
	fmt.Fprintf(w, "\nenvironment: %s and %s are required for live audits; %s also for --reassess.\n", envAnthropic, envPSI, envAnthropic)
}

func parse(args []string) (*options, *flag.FlagSet, error) {
	o := &options{}
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.out, "out", "", "write the report to this file instead of stdout")
	fs.StringVar(&o.locale, "locale", "hu", "locale config to use (config/locales/<locale>.yaml)")
	fs.StringVar(&o.model, "model", "", "Claude model for the assessment (default from config/scoring.yaml)")
	fs.StringVar(&o.configDir, "config", "config", "directory with scoring.yaml and locales/")
	fs.StringVar(&o.promptsDir, "prompts", "prompts", "directory with the datedness rubric and the agent prompt")
	fs.StringVar(&o.snapshotsDir, "snapshots", "snapshots", "directory to store snapshots in")
	fs.StringVar(&o.fromSnapshot, "from-snapshot", "", "recompute the report from this snapshot directory, without touching the site")
	fs.BoolVar(&o.reassess, "reassess", false, "with --from-snapshot: run the LLM assessment again instead of reusing it")
	fs.BoolVar(&o.agent, "agent", false, "after the baseline audit, let the agent inspect more pages and give the datedness rating (budgets in config/scoring.yaml)")
	fs.StringVar(&o.chrome, "chrome", "", "path to the Chrome binary (default: find it automatically)")
	if err := fs.Parse(args); err != nil {
		return nil, fs, err
	}
	switch {
	case o.fromSnapshot != "" && fs.NArg() > 0:
		return nil, fs, errors.New("give either a URL or --from-snapshot, not both")
	case o.fromSnapshot == "" && fs.NArg() != 1:
		return nil, fs, errors.New("expected exactly one URL")
	case o.reassess && o.fromSnapshot == "":
		return nil, fs, errors.New("--reassess only works with --from-snapshot")
	}
	if o.fromSnapshot == "" {
		u, err := normalizeURL(fs.Arg(0))
		if err != nil {
			return nil, fs, err
		}
		o.url = u
	}
	return o, fs, nil
}

// normalizeURL adds https:// when no scheme is given and checks the result.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("not a valid website address: %q", raw)
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	o, fs, err := parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(fs, stdout)
			return exitOK
		}
		fmt.Fprintf(stderr, "audit: %v\n\n", err)
		usage(fs, stderr)
		return exitUsage
	}

	scoring, err := config.LoadScoring(o.configDir)
	if err != nil {
		fmt.Fprintf(stderr, "audit: %v\n", err)
		return exitUsage
	}
	if o.model == "" {
		o.model = scoring.DefaultModel
	}
	price, ok := scoring.Models[o.model]
	if !ok {
		fmt.Fprintf(stderr, "audit: model %q has no price in %s (known: %s)\n", o.model,
			filepath.Join(o.configDir, "scoring.yaml"), strings.Join(scoring.ModelNames(), ", "))
		return exitUsage
	}

	var snap *snapshot.Snapshot
	if o.fromSnapshot != "" {
		if snap, err = snapshot.Read(o.fromSnapshot); err != nil {
			fmt.Fprintf(stderr, "audit: %v\n", err)
			return exitUsage
		}
		o.locale = snap.Meta.Locale
		if o.agent && !snap.Meta.Agent {
			fmt.Fprintf(stderr, "audit: --agent: snapshot %s was not recorded in agent mode, so it has no agent recording to replay\n", o.fromSnapshot)
			return exitUsage
		}
		// An agent snapshot replays in agent mode, flag or not.
		o.agent = snap.Meta.Agent
	}
	locale, err := config.LoadLocale(o.configDir, o.locale)
	if err != nil {
		fmt.Fprintf(stderr, "audit: %v\n", err)
		return exitUsage
	}

	// Keys are checked before anything touches the network.
	needAnthropic := o.fromSnapshot == "" || o.reassess
	for _, env := range []string{envAnthropic, envPSI} {
		if getenv(env) != "" || (env == envAnthropic && !needAnthropic) || (env == envPSI && o.fromSnapshot != "") {
			continue
		}
		fmt.Fprintf(stderr, "audit: environment variable %s is not set\n", env)
		return exitUsage
	}

	logf := func(format string, a ...any) { fmt.Fprintf(stderr, "audit: "+format+"\n", a...) }
	p, err := newPipeline(o, locale, scoring, price, needAnthropic, getenv, logf)
	if err != nil {
		fmt.Fprintf(stderr, "audit: %v\n", err)
		return exitUsage
	}

	var rep *report.Report
	if o.fromSnapshot != "" {
		rep, err = p.Replay(ctx, o.fromSnapshot, o.reassess)
	} else {
		rep, err = p.Run(ctx, o.url)
	}
	if err != nil {
		fmt.Fprintf(stderr, "audit: %v\n", err)
		return exitFailure
	}
	return writeReport(rep, o.out, stdout, stderr)
}

// newPipeline wires the stages for the options. needAnthropic says whether
// an LLM call can happen: a live audit, or a replay with --reassess.
func newPipeline(o *options, locale *config.Locale, scoring *config.Scoring, price config.ModelPrice, needAnthropic bool,
	getenv func(string) string, logf func(string, ...any)) (*audit.Pipeline, error) {
	client := safehttp.New(safehttp.Options{})
	chrome := &capture.Chrome{Locale: locale, HTTP: client, ExecPath: o.chrome}
	p := &audit.Pipeline{
		Capturer:     chrome,
		PSI:          &psi.Client{Key: getenv(envPSI)},
		HTTP:         client,
		Locale:       locale,
		Scoring:      scoring,
		SnapshotRoot: o.snapshotsDir,
		Log:          logf,
	}
	if !needAnthropic {
		return p, nil
	}
	rubric, err := assess.LoadRubric(filepath.Join(o.promptsDir, assess.RubricFile))
	if err != nil {
		return nil, err
	}
	api := anthropic.NewClient()
	p.Assessor = &assess.Claude{API: &api.Beta.Messages, Model: o.model, Price: price, Rubric: rubric}
	// A live agent audit, or --reassess of an agent snapshot (which replays
	// recorded pages instead of opening the browser).
	if o.agent {
		prompt, err := agent.LoadPrompt(filepath.Join(o.promptsDir, agent.PromptFile), rubric)
		if err != nil {
			return nil, err
		}
		p.Agent = &agent.Runner{
			API:    &api.Beta.Messages,
			Config: agent.Config{Model: o.model, Price: price, Prompt: prompt, Budgets: scoring.Agent},
			Open:   func(ctx context.Context) (capture.Session, error) { return chrome.Open(ctx) },
			HTTP:   client,
			Locale: locale,
		}
	}
	return p, nil
}

func writeReport(rep *report.Report, out string, stdout, stderr io.Writer) int {
	b, err := audit.MarshalReport(rep)
	if err != nil {
		fmt.Fprintf(stderr, "audit: %v\n", err)
		return exitFailure
	}
	if out == "" {
		if _, err := stdout.Write(b); err != nil {
			fmt.Fprintf(stderr, "audit: write report: %v\n", err)
			return exitFailure
		}
	} else if err := os.WriteFile(out, b, 0o644); err != nil {
		fmt.Fprintf(stderr, "audit: %v\n", err)
		return exitFailure
	} else {
		fmt.Fprintf(stderr, "audit: report written to %s\n", out)
	}
	fmt.Fprintf(stderr, "audit: tier %d, score %.1f, flags %v\n", rep.Tier, rep.Score, rep.Flags)
	return exitOK
}
