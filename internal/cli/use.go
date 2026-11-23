package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chrom-ar/collector-cli/internal/api"
	"github.com/chrom-ar/collector-cli/internal/auth"
	"github.com/chrom-ar/collector-cli/internal/render"
	"github.com/chrom-ar/collector-cli/internal/target"
	"github.com/spf13/cobra"
)

func newUseCmd(a *app) *cobra.Command {
	var clear, here bool
	c := &cobra.Command{
		Use:   "use [<org>[/<project>]]",
		Short: "Set the organization and project commands use by default",
		Long: "Set the organization, and optionally the project, that commands use on\n" +
			"this Collector when nothing else says. Without an argument, show what\n" +
			"commands here use, and where that comes from.\n\n" +
			"--here writes it to " + target.FileName + " in this directory instead, for every\n" +
			"Collector: commit it, and commands run anywhere in the repository use it.\n\n" +
			"Which wins: --org/--project, then COLLECTOR_ORG/COLLECTOR_PROJECT, then\n" +
			"the nearest " + target.FileName + " (here or above), then collector use, then\n" +
			"the only organization or project there is. A project counts only with\n" +
			"the organization it was set with.",
		Example: "  collector use acme/shop\n  collector use acme\n  collector use\n" +
			"  collector use --here acme/shop\n  collector use --clear",
		Args: argsBetween(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clear && len(args) > 0 {
				return usageError{errors.New("--clear takes no argument")}
			}
			if here {
				return a.useHere(cmd.Context(), args, clear)
			}
			s, err := a.resolve()
			if err != nil {
				return err
			}
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			if clear {
				delete(cfg.Defaults, s.url)
				if err := cfg.Save(a.configPath()); err != nil {
					return err
				}
				a.out("No default on %s.", s.url)
				if _, path, _ := a.projectFile(); path != "" {
					a.progress("%s still says which here.", render.Safe(path))
				}
				return nil
			}
			if len(args) == 0 {
				text, err := a.namedText(s.url)
				if err != nil {
					return err
				}
				if text == "" {
					a.out("No default on %s.", s.url)
					a.progress("Set one with: collector use <org>[/<project>] (or --here for this repository)")
					return nil
				}
				a.out("%s", text)
				return nil
			}
			d, err := a.checkUse(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if cfg.Defaults == nil {
				cfg.Defaults = map[string]auth.Default{}
			}
			cfg.Defaults[s.url] = d
			if err := cfg.Save(a.configPath()); err != nil {
				return err
			}
			a.out("Commands on %s now use %s.", s.url, render.Safe(defaultText(d)))
			a.progress("--org and --project, COLLECTOR_ORG and COLLECTOR_PROJECT, and a %s, still win.", target.FileName)
			return nil
		},
	}
	c.Flags().BoolVar(&clear, "clear", false, "forget the default (with --here, remove "+target.FileName+")")
	c.Flags().BoolVar(&here, "here", false, "write it to "+target.FileName+" in this directory, to commit")
	return c
}

// checkUse reads <org>[/<project>] and checks that it exists, and that
// this token can see it.
func (a *app) checkUse(ctx context.Context, arg string) (auth.Default, error) {
	org, project, _ := strings.Cut(arg, "/")
	if org == "" || strings.Contains(project, "/") {
		return auth.Default{}, usageError{fmt.Errorf("%q: expected <org> or <org>/<project>", arg)}
	}
	c, _, err := a.client()
	if err != nil {
		return auth.Default{}, err
	}
	path := api.OrgPath(org)
	if project != "" {
		path = api.ProjectPath(org, project)
	}
	if _, err := c.Get(ctx, path, nil, nil); err != nil {
		return auth.Default{}, err
	}
	return auth.Default{Org: org, Project: project}, nil
}

// useHere writes (or with clear, removes) the project file in the
// working directory. Without an argument it writes what commands use now.
func (a *app) useHere(ctx context.Context, args []string, clear bool) error {
	dir := a.env.Dir
	if dir == "" {
		return errors.New("can't tell which directory this is")
	}
	if clear {
		removed, err := target.RemoveFile(dir)
		if err != nil {
			return err
		}
		if !removed {
			a.out("No %s in %s.", target.FileName, render.Safe(dir))
			return nil
		}
		a.out("Removed %s from %s.", target.FileName, render.Safe(dir))
		return nil
	}
	var d auth.Default
	if len(args) == 1 {
		var err error
		if d, err = a.checkUse(ctx, args[0]); err != nil {
			return err
		}
	} else {
		c, _, err := a.client()
		if err != nil {
			return err
		}
		res, err := a.resolver(c)
		if err != nil {
			return err
		}
		if d.Org, d.Project, err = res.Both(ctx); err != nil {
			return err
		}
	}
	if err := target.WriteFile(dir, target.Given{Org: d.Org, Project: d.Project}); err != nil {
		return err
	}
	a.out("Commands in %s now use %s: %s written; commit it.", render.Safe(dir), render.Safe(defaultText(d)), target.FileName)
	a.progress("--org and --project, and COLLECTOR_ORG and COLLECTOR_PROJECT, still win; it wins over collector use.")
	return nil
}

// namedText is what commands here use without asking, and where from,
// or "" when nothing names it.
func (a *app) namedText(url string) (string, error) {
	r, err := a.resolverFor(nil, url)
	if err != nil {
		return "", err
	}
	_, path, _ := a.projectFile()
	org, orgFrom, project, projectFrom := r.Named()
	if org == "" {
		return "", nil
	}
	from := func(l target.Layer, part string) string {
		switch l {
		case target.FromFlag:
			return "--" + part
		case target.FromEnv:
			return "COLLECTOR_" + strings.ToUpper(part)
		case target.FromFile:
			return render.Safe(path)
		default:
			return "collector use"
		}
	}
	o, p := render.Safe(org), render.Safe(project)
	switch of := from(orgFrom, "org"); {
	case project == "":
		return fmt.Sprintf("%s (from %s)", o, of), nil
	case orgFrom == projectFrom && orgFrom != target.FromFlag && orgFrom != target.FromEnv:
		return fmt.Sprintf("%s/%s (from %s)", o, p, of), nil
	default:
		return fmt.Sprintf("%s/%s (%s from %s, %s from %s)", o, p, o, of, p, from(projectFrom, "project")), nil
	}
}

func defaultText(d auth.Default) string {
	if d.Project == "" {
		return d.Org
	}
	return d.Org + "/" + d.Project
}
