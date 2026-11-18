package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
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

// shownRecords is how many of a preview's records the summary lists;
// --dry-run --json has all of them.
const shownRecords = 5

// printCheck writes what Collector understood of a rule's words, what it
// would have caught over the last day, and the records it matches.
func printCheck(w io.Writer, rc *api.RuleCheck, now time.Time) {
	if name := deref(rc.Name); name != "" {
		if d := deref(rc.Description); d != "" {
			name += " — " + d
		}
		fmt.Fprintln(w, render.Safe(name))
	}
	if rc.Text != "" {
		fmt.Fprintln(w, render.Safe(rc.Text))
	}
	fmt.Fprintln(w, "Checks: "+render.Safe(rc.Sentence))
	p := rc.Preview
	switch {
	case p == nil:
		fmt.Fprintln(w, "No preview: Collector couldn't read the last day's logs.")
		return
	case p.Count == 0:
		fmt.Fprintln(w, "Over the last day it wouldn't have fired.")
	case len(p.Examples) == 0:
		fmt.Fprintln(w, "Over the last day it would have fired "+plural(p.Count, "time")+".")
	default:
		fmt.Fprintln(w, "Over the last day it would have fired "+plural(p.Count, "time")+", as for:")
		width := 0
		for _, e := range p.Examples {
			width = max(width, utf8.RuneCountInString(render.Safe(e.Subject)))
		}
		for _, e := range p.Examples {
			s := render.Safe(e.Subject)
			fmt.Fprintf(w, "  %s%s  %s\n", s, strings.Repeat(" ", width-utf8.RuneCountInString(s)), number(e.Measure))
		}
	}
	if p.Matched == nil {
		return
	}
	if *p.Matched == 0 || len(p.Records) == 0 {
		fmt.Fprintln(w, "Its conditions match no records of the last day.")
		return
	}
	fmt.Fprintf(w, "Its conditions match %s of the last day; the newest:\n", plural(*p.Matched, "record"))
	for i, r := range p.Records {
		if i == shownRecords {
			fmt.Fprintf(w, "  and %d more; --dry-run --json lists them\n", len(p.Records)-shownRecords)
			break
		}
		fields := make([]string, 0, len(r.Fields))
		for _, k := range slices.Sorted(maps.Keys(r.Fields)) {
			fields = append(fields, k+": "+r.Fields[k])
		}
		fmt.Fprintf(w, "  %s  %s\n", render.Ago(r.At, now), render.Safe(strings.Join(fields, ", ")))
	}
}

// ruleFlags are add's and rewrite's flags.
type ruleFlags struct {
	yes, dryRun              bool
	token, name, description string
}

func newThreatRulesWriteCmd(a *app, rewrite bool) *cobra.Command {
	var f ruleFlags
	use, short := "add [<words>...]", "Write a rule in your words, and turn it on"
	example := "  collector threat-rules add 'More than 20 failed sign-ins for one account in 5 minutes'\n" +
		"  collector threat-rules add 'Any request to /admin from outside 10.0.0.0/8' --dry-run --json\n" +
		"  collector threat-rules add 'Make it 30' --continue <token> --dry-run\n" +
		"  collector threat-rules add --continue <token> --name 'Password guessing' --yes"
	minArgs := 0
	if rewrite {
		use, short = "rewrite <ref> [<words>...]", "New words for one of your rules; it's on afterwards"
		example = "  collector threat-rules rewrite r-Rgx7Kq2XpAb2 'More than 50 failed sign-ins for one account in 5 minutes'"
		minArgs = 1
	}
	c := &cobra.Command{
		Use:   use,
		Short: short,
		Long: "Collector reads the words with an AI and says what it understood: a\n" +
			"suggested name, the rule in one sentence, what it checks, what it would\n" +
			"have caught over the last day and the records it matches. Then it asks\n" +
			"before turning it on; type a change instead of y to tell Collector more\n" +
			"(up to 10 messages). --yes doesn't ask.\n\n" +
			"Without a terminal: --dry-run only checks (--json prints Collector's\n" +
			"answer, its token and spec included). --continue <token> goes on from\n" +
			"that check with more words, or with none turns exactly that version on.\n" +
			"Owners and admins only.",
		Example: example,
		Args:    argsBetween(minArgs, 1000),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if rewrite {
				ref, args = args[0], args[1:]
			}
			text := strings.TrimSpace(strings.Join(args, " "))
			switch {
			case text == "" && f.token == "":
				return usageError{errors.New("say what to look for, in words")}
			case text == "" && f.dryRun:
				return usageError{errors.New("--dry-run checks words: give some")}
			case cmd.Flags().Changed("name") && strings.TrimSpace(f.name) == "",
				cmd.Flags().Changed("description") && strings.TrimSpace(f.description) == "":
				return usageError{errors.New("--name and --description can't be blank")}
			}
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			return a.writeRule(ctx, cl, org, project, ref, text, &f)
		},
	}
	fl := c.Flags()
	fl.BoolVar(&f.dryRun, "dry-run", false, "only check the words; turn nothing on")
	fl.BoolVarP(&f.yes, "yes", "y", false, "turn it on without asking")
	fl.StringVar(&f.token, "continue", "", "go on from an earlier check, by its token")
	fl.StringVar(&f.name, "name", "", "the rule's name, instead of Collector's suggestion (up to 80 characters)")
	fl.StringVar(&f.description, "description", "", "its description, instead of Collector's suggestion (up to 280 characters)")
	return c
}

// writeRule checks text (going on from f.token, if any) and shows what
// Collector understood; at a terminal, typed changes go back to it. Once
// the person agrees it turns exactly that on: a new rule, or ref's new
// words. With no text, f.token's check is turned on as it is.
func (a *app) writeRule(ctx context.Context, cl *api.Client, org, project, ref, text string, f *ruleFlags) error {
	base := api.ProjectPath(org, project) + "/threat-rules"
	interactive := !f.yes && !f.dryRun && a.env.StdinIsTerminal
	if !f.yes && !f.dryRun && !interactive {
		// Before the check: it costs an AI call.
		return usageError{errors.New("pass --yes to turn it on without a terminal to ask at, or --dry-run to only check it")}
	}
	in := bufio.NewReader(a.env.Stdin)
	token := f.token
	for text != "" {
		stop := a.working("Checking the rule with Collector…")
		var rc api.RuleCheck
		body := map[string]string{"text": text}
		if token != "" {
			body["token"] = token
		}
		raw, err := cl.Send(ctx, http.MethodPost, base+"/check", nil, body, &rc)
		stop()
		if err != nil {
			return err
		}
		if f.dryRun || (a.flags.json && rc.Status != "ok") {
			if a.flags.json {
				if err := a.printJSON(raw); err != nil {
					return err
				}
			} else {
				a.printOutcome(a.env.Stdout, &rc)
			}
			if rc.Status != "ok" {
				return silentError{ExitError}
			}
			a.progress("Nothing turned on (--dry-run). To change it, or to turn it on:\n" +
				"  collector threat-rules " + verb(ref) + " '<more words>' --continue " + rc.Token + "\n" +
				"  collector threat-rules " + verb(ref) + " --continue " + rc.Token + " --yes")
			return nil
		}
		w := a.env.Stdout
		if a.flags.json {
			w = a.env.Stderr
		}
		if rc.Status != "ok" && !interactive {
			return fmt.Errorf("collector can't make a rule of that: %s", render.Safe(rc.Reason))
		}
		a.printOutcome(w, &rc)
		token = rc.Token
		if !interactive {
			break
		}
		question := "Turn it on? [y/N, or type a change]"
		if rc.Status != "ok" {
			question = "Say it another way, or press Enter to stop:"
		}
		fmt.Fprintf(a.env.Stderr, "%s ", question)
		line, _ := in.ReadString('\n')
		answer := strings.TrimSpace(line)
		switch strings.ToLower(answer) {
		case "y", "yes":
			if rc.Status == "ok" {
				text = ""
				continue
			}
		case "", "n", "no":
			fmt.Fprintln(a.env.Stderr, "Nothing changed.")
			return silentError{ExitError}
		}
		text = answer
	}
	return a.turnOn(ctx, cl, org, project, ref, token, f)
}

func (a *app) printOutcome(w io.Writer, rc *api.RuleCheck) {
	if rc.Status != "ok" {
		fmt.Fprintln(w, "Collector can't make a rule of that: "+render.Safe(rc.Reason))
		return
	}
	printCheck(w, rc, a.env.Now())
}

func verb(ref string) string {
	if ref == "" {
		return "add"
	}
	return "rewrite " + ref
}

// turnOn creates the rule token's check describes, or gives ref its words.
func (a *app) turnOn(ctx context.Context, cl *api.Client, org, project, ref, token string, f *ruleFlags) error {
	body := map[string]string{"token": token}
	if f.name != "" {
		body["name"] = strings.TrimSpace(f.name)
	}
	if f.description != "" {
		body["description"] = strings.TrimSpace(f.description)
	}
	method, path := http.MethodPost, api.ProjectPath(org, project)+"/threat-rules"
	if ref != "" {
		method, path = http.MethodPut, rulePath(org, project, ref)
	}
	var r api.ThreatRule
	raw, err := cl.Send(ctx, method, path, nil, body, &r)
	if err != nil {
		// The token came from a check, not from how the command was typed.
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
