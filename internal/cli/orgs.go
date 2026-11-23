package cli

import (
	"context"
	"errors"
	"net/http"

	"github.com/chrom-ar/collector-cli/internal/api"
	"github.com/chrom-ar/collector-cli/internal/render"
	"github.com/spf13/cobra"
)

func newOrgsCmd(a *app) *cobra.Command {
	return group(a, "orgs", "Your organizations",
		"  collector orgs list\n  collector orgs show acme\n  collector orgs edit acme --name \"Acme Labs\"",
		newOrgsListCmd(a), newOrgsShowCmd(a), newOrgsEditCmd(a))
}

func newOrgsListCmd(a *app) *cobra.Command {
	var l listFlags
	c := &cobra.Command{
		Use:     "list",
		Short:   "Your organizations and your role in each",
		Example: "  collector orgs list\n  collector orgs list --json",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, _, err := a.client()
			if err != nil {
				return err
			}
			now := a.env.Now()
			return printList(cmd.Context(), a, cl, &l, "/orgs", nil, func(os []api.Org) string {
				rows := make([][]string, 0, len(os))
				for _, o := range os {
					rows = append(rows, []string{o.Slug, o.Name, o.Role, render.Ago(o.CreatedAt, now)})
				}
				return render.Table([]string{"SLUG", "NAME", "ROLE", "CREATED"}, rows)
			}, "You're in no organizations.")
		},
	}
	l.add(c)
	return c
}

// orgArg is the organization named by args, or the one implied.
func (a *app) orgArg(ctx context.Context, args []string) (*api.Client, string, error) {
	cl, _, err := a.client()
	if err != nil {
		return nil, "", err
	}
	org := ""
	if len(args) > 0 {
		org = args[0]
	} else {
		res, err := a.resolver(cl)
		if err != nil {
			return nil, "", err
		}
		if org, err = res.Org(ctx); err != nil {
			return nil, "", err
		}
	}
	a.org = org
	return cl, org, nil
}

func (a *app) printOrg(o *api.Org) {
	a.out("%s (%s)", render.Safe(o.Name), render.Safe(o.Slug))
	a.out("Your role: %s", render.Safe(o.Role))
	a.out("%s", render.Safe(o.URL))
}

func newOrgsShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "show [<org>]",
		Short:   "One organization",
		Example: "  collector orgs show\n  collector orgs show acme --json",
		Args:    argsBetween(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, org, err := a.orgArg(cmd.Context(), args)
			if err != nil {
				return err
			}
			var o api.Org
			raw, err := cl.Get(cmd.Context(), api.OrgPath(org), nil, &o)
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(raw)
			}
			a.printOrg(&o)
			return nil
		},
	}
}

func newOrgsEditCmd(a *app) *cobra.Command {
	var name, slug string
	c := &cobra.Command{
		Use:   "edit [<org>]",
		Short: "Rename it, or change its slug (owners)",
		Long: "Rename the organization, or change its slug. A new slug changes its\n" +
			"URLs, and the old one stops working: update --org and COLLECTOR_ORG.",
		Example: "  collector orgs edit acme --name \"Acme Labs\"\n  collector orgs edit acme --slug acme-labs",
		Args:    argsBetween(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]string{}
			if cmd.Flags().Changed("name") {
				body["name"] = name
			}
			if cmd.Flags().Changed("slug") {
				body["slug"] = slug
			}
			if len(body) == 0 {
				return usageError{errors.New("say what to change: --name or --slug")}
			}
			cl, org, err := a.orgArg(cmd.Context(), args)
			if err != nil {
				return err
			}
			var o api.Org
			raw, err := cl.Send(cmd.Context(), http.MethodPatch, api.OrgPath(org), nil, body, &o)
			if err != nil {
				return err
			}
			if a.flags.json {
				if err := a.printJSON(raw); err != nil {
					return err
				}
			} else {
				a.printOrg(&o)
			}
			if o.Slug != "" && o.Slug != org {
				a.progress("The slug is now %s: %s stops working; update --org and COLLECTOR_ORG.", render.Safe(o.Slug), render.Safe(org))
			}
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "the new name")
	c.Flags().StringVar(&slug, "slug", "", "the new slug (changes its URLs)")
	return c
}
