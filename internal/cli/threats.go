package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/chrom-ar/collector-cli/internal/api"
	"github.com/chrom-ar/collector-cli/internal/render"
	"github.com/spf13/cobra"
)

func newThreatsCmd(a *app) *cobra.Command {
	return group(a, "threats", "What the threat rules caught: list, read and resolve",
		"  collector threats list\n  collector threats show Det7Kq2XpAb2\n  collector threats resolve Det7Kq2XpAb2",
		newThreatsListCmd(a), newThreatsShowCmd(a), newThreatsResolveCmd(a))
}

func detectionPath(org, project, id string) string {
	return api.ProjectPath(org, project) + "/detections/" + api.Seg(id)
}

// subjectText is "ip: 203.0.113.9", by key.
func subjectText(s map[string]any) string {
	parts := make([]string, 0, len(s))
	for _, k := range slices.Sorted(maps.Keys(s)) {
		parts = append(parts, fmt.Sprintf("%s: %v", k, s[k]))
	}
	return strings.Join(parts, ", ")
}

func number(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func newThreatsListCmd(a *app) *cobra.Command {
	var (
		l      listFlags
		status string
	)
	c := &cobra.Command{
		Use:   "list",
		Short: "Detections, last seen first",
		Long: "Detections, last seen first: each is one subject (an IP, an account)\n" +
			"firing one threat rule. Without --status, the open ones.",
		Example: "  collector threats list\n  collector threats list --status all --json",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			empty := "No open threats."
			switch status {
			case "":
			case "all":
				empty = "No threats caught yet."
			default:
				empty = "No " + render.Safe(status) + " threats."
			}
			now := a.env.Now()
			return printList(ctx, a, cl, &l, api.ProjectPath(org, project)+"/detections", api.Query{"status": status}, func(ds []api.Detection) string {
				rows := make([][]string, 0, len(ds))
				for _, d := range ds {
					rows = append(rows, []string{d.ID, d.Severity, d.Status, render.Clip(render.Safe(d.RuleName), 50),
						render.Clip(render.Safe(subjectText(d.Subject)), 50), strconv.Itoa(d.FiredCount), render.Ago(d.LastSeenAt, now)})
				}
				return render.Table([]string{"ID", "SEVERITY", "STATUS", "RULE", "SUBJECT", "FIRED", "LAST SEEN"}, rows)
			}, empty)
		},
	}
	c.Flags().StringVar(&status, "status", "", "open (default), resolved or all")
	l.add(c)
	return c
}

func newThreatsShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "show <id>",
		Short:   "One detection and its evidence",
		Example: "  collector threats show Det7Kq2XpAb2\n  collector threats show Det7Kq2XpAb2 --json",
		Args:    argsBetween(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			var d api.Detection
			raw, err := cl.Get(ctx, detectionPath(org, project, args[0]), nil, &d)
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(raw)
			}
			now := a.env.Now()
			facts := fmt.Sprintf("%s · %s · fired %s", d.Status, d.Severity, plural(d.FiredCount, "time"))
			if d.LastMeasure != nil {
				facts += " · last measure " + number(*d.LastMeasure)
			}
			lines := []string{
				d.RuleName + ": " + subjectText(d.Subject),
				facts,
				fmt.Sprintf("First seen %s · last seen %s", render.Ago(d.FirstSeenAt, now), render.Ago(d.LastSeenAt, now)),
			}
			if len(d.Evidence) > 0 {
				lines = append(lines, "Evidence:")
				for _, e := range d.Evidence {
					lines = append(lines, fmt.Sprintf("  %s %s, %s", e.Kind, e.ID, render.Ago(e.At, now)))
				}
			}
			lines = append(lines, d.URL)
			for _, l := range lines {
				a.out("%s", render.Safe(l))
			}
			return nil
		},
	}
}

func newThreatsResolveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "resolve <id>...",
		Short:   "Mark resolved",
		Example: "  collector threats resolve Det7Kq2XpAb2\n  collector threats resolve Det7Kq2XpAb2 Det8Mn3YqBc3",
		Args:    argsBetween(1, 100),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			return a.each(ctx, args, func(id string) ([]byte, string, error) {
				var d api.Detection
				raw, err := cl.Send(ctx, http.MethodPatch, detectionPath(org, project, id), nil, map[string]string{"status": "resolved"}, &d)
				return raw, d.ID + " " + d.Status, err
			})
		},
	}
}

func newThreatRulesCmd(a *app) *cobra.Command {
	return group(a, "threat-rules", "What counts as a threat: the defaults and your own rules",
		"  collector threat-rules list\n"+
			"  collector threat-rules add 'More than 20 failed sign-ins for one account in 5 minutes'\n"+
			"  collector threat-rules off d-probing",
		newThreatRulesListCmd(a), newThreatRulesWriteCmd(a, false), newThreatRulesWriteCmd(a, true),
		newThreatRulesSwitchCmd(a, true), newThreatRulesSwitchCmd(a, false), newThreatRulesThresholdCmd(a))
}

func rulePath(org, project, ref string) string {
	return api.ProjectPath(org, project) + "/threat-rules/" + api.Seg(ref)
}

func ruleState(r *api.ThreatRule) string {
	switch {
	case !r.Enabled:
		return "off"
	case r.Paused:
		return "paused"
	}
	return "on"
}

func newThreatRulesListCmd(a *app) *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "Every rule, defaults first",
		Long: "Every threat rule, defaults first. paused: Collector stopped running it\n" +
			"(turn it on again to clear that). --json has what each one checks, in\n" +
			"words (sentence) and as Collector runs it (spec).",
		Example: "  collector threat-rules list\n  collector threat-rules list --json",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			var l listFlags
			return printList(ctx, a, cl, &l, api.ProjectPath(org, project)+"/threat-rules", nil, func(rs []api.ThreatRule) string {
				rows := make([][]string, 0, len(rs))
				for _, r := range rs {
					def := "no"
					if r.Default {
						def = "yes"
					}
					rows = append(rows, []string{r.Ref, ruleState(&r), r.Severity, def, render.Clip(render.Safe(r.Name), 80)})
				}
				return render.Table([]string{"REF", "STATUS", "SEVERITY", "DEFAULT", "NAME"}, rows)
			}, "No threat rules.")
		},
	}
	return c
}

// printCheck writes what Collector understood of a rule's words and
// what it would have caught over the last day.
func printCheck(w io.Writer, rc *api.RuleCheck) {
	fmt.Fprintln(w, render.Safe(rc.Sentence))
	switch {
	case rc.Preview == nil:
		fmt.Fprintln(w, "No preview: Collector couldn't read the last day's logs.")
	case rc.Preview.Count == 0:
		fmt.Fprintln(w, "Over the last day it wouldn't have fired.")
	default:
		line := "Over the last day it would have fired " + plural(rc.Preview.Count, "time")
		if len(rc.Preview.Examples) == 0 {
			fmt.Fprintln(w, line+".")
			return
		}
		fmt.Fprintln(w, line+", as for:")
		width := 0
		for _, e := range rc.Preview.Examples {
			width = max(width, utf8.RuneCountInString(render.Safe(e.Subject)))
		}
		for _, e := range rc.Preview.Examples {
			s := render.Safe(e.Subject)
			fmt.Fprintf(w, "  %s%s  %s\n", s, strings.Repeat(" ", width-utf8.RuneCountInString(s)), number(e.Measure))
		}
	}
}

func newThreatRulesWriteCmd(a *app, rewrite bool) *cobra.Command {
	var yes, dryRun bool
	use, short := "add <words>...", "Write a rule in your words, and turn it on"
	example := "  collector threat-rules add 'More than 20 failed sign-ins for one account in 5 minutes'\n" +
		"  collector threat-rules add 'Any request to /admin from outside 10.0.0.0/8' --dry-run --json"
	minArgs := 1
	if rewrite {
		use, short = "rewrite <ref> <words>...", "New words for one of your rules; it's on afterwards"
		example = "  collector threat-rules rewrite r-Rgx7Kq2XpAb2 'More than 50 failed sign-ins for one account in 5 minutes'"
		minArgs = 2
	}
	c := &cobra.Command{
		Use:   use,
		Short: short,
		Long: "Collector reads the words with an AI and says, in plain words, what the\n" +
			"rule would check, and what it would have caught over the last day. It\n" +
			"turns on only once you say yes (or with --yes). --dry-run only checks;\n" +
			"with --json it prints Collector's answer, spec included. Owners and\n" +
			"admins only.",
		Example: example,
		Args:    argsBetween(minArgs, 1000),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if rewrite {
				ref, args = args[0], args[1:]
			}
			text := strings.TrimSpace(strings.Join(args, " "))
			if text == "" {
				return usageError{errors.New("say what to look for, in words")}
			}
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			return a.writeRule(ctx, cl, org, project, ref, text, yes, dryRun)
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "only check the words; turn nothing on")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "turn it on without asking")
	return c
}

// writeRule checks text, shows what Collector understood, and once the
// person agrees turns exactly that on: a new rule, or ref's new words.
func (a *app) writeRule(ctx context.Context, cl *api.Client, org, project, ref, text string, yes, dryRun bool) error {
	base := api.ProjectPath(org, project) + "/threat-rules"
	stop := a.working("Checking the rule with Collector…")
	var rc api.RuleCheck
	raw, err := cl.Send(ctx, http.MethodPost, base+"/check", nil, map[string]string{"text": text}, &rc)
	stop()
	if err != nil {
		return err
	}
	if rc.Status != "ok" {
		if a.flags.json {
			if err := a.printJSON(raw); err != nil {
				return err
			}
			return silentError{ExitError}
		}
		return fmt.Errorf("collector can't make a rule of that: %s", render.Safe(rc.Reason))
	}
	if dryRun {
		if a.flags.json {
			return a.printJSON(raw)
		}
		printCheck(a.env.Stdout, &rc)
		a.progress("Nothing turned on (--dry-run).")
		return nil
	}
	w := a.env.Stdout
	if a.flags.json {
		w = a.env.Stderr
	}
	printCheck(w, &rc)
	if err := a.ask(yes, "Turn it on?", "pass --yes to turn it on without a terminal to ask at, or --dry-run to only check it"); err != nil {
		return err
	}
	method, path := http.MethodPost, base
	if ref != "" {
		method, path = http.MethodPut, rulePath(org, project, ref)
	}
	var r api.ThreatRule
	raw, err = cl.Send(ctx, method, path, nil, map[string]string{"token": rc.Token}, &r)
	if err != nil {
		// The token is the CLI's doing, not a usage mistake.
		return failure{err}
	}
	if a.flags.json {
		return a.printJSON(raw)
	}
	a.out("On: %s %s", render.Safe(r.Ref), render.Safe(r.Name))
	return nil
}

func newThreatRulesSwitchCmd(a *app, on bool) *cobra.Command {
	use, short, done := "on", "Turn rules on (also clears a pause)", "On"
	if !on {
		use, short, done = "off", "Turn rules off", "Off"
	}
	return &cobra.Command{
		Use:     use + " <ref>...",
		Short:   short,
		Example: fmt.Sprintf("  collector threat-rules %s d-probing\n  collector threat-rules %s r-Rgx7Kq2XpAb2 d-scanning", use, use),
		Args:    argsBetween(1, 100),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			return a.each(ctx, args, func(ref string) ([]byte, string, error) {
				var r api.ThreatRule
				raw, err := cl.Send(ctx, http.MethodPatch, rulePath(org, project, ref), nil, map[string]bool{"enabled": on}, &r)
				return raw, done + ": " + r.Ref + " " + r.Name, err
			})
		},
	}
}

func newThreatRulesThresholdCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "threshold <ref> <n|default>",
		Short: "Set when a counting default fires; default goes back to Collector's",
		Example: "  collector threat-rules threshold d-account-brute-force 30\n" +
			"  collector threat-rules threshold d-account-brute-force default",
		Args: argsBetween(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var threshold any
			if args[1] != "default" {
				n, err := strconv.Atoi(args[1])
				if err != nil || n < 1 {
					return usageError{fmt.Errorf("threshold %q: give a whole number, 1 or more, or default", render.Safe(args[1]))}
				}
				threshold = n
			}
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			var r api.ThreatRule
			raw, err := cl.Send(ctx, http.MethodPatch, rulePath(org, project, args[0]), nil, map[string]any{"threshold": threshold}, &r)
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(raw)
			}
			a.out("%s: %s", render.Safe(r.Ref), render.Safe(r.Sentence))
			return nil
		},
	}
}
