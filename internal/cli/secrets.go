package cli

import (
	"fmt"
	"net/http"

	"github.com/chrom-ar/collector-cli/internal/api"
	"github.com/chrom-ar/collector-cli/internal/render"
	"github.com/spf13/cobra"
)

// secretKind is upload-tokens or server-keys: a project's secrets, with
// the same shapes.
type secretKind struct {
	group, prefix, noun, column, purpose, short, example string
}

var (
	uploadTokens = secretKind{
		group: "upload-tokens", prefix: "cltu_", noun: "Upload token", column: "TOKEN",
		purpose: "Store it as COLLECTOR_UPLOAD_TOKEN in your CI's secrets.",
		short:   "Tokens CI uploads source maps with",
		example: "  collector upload-tokens list\n  collector upload-tokens create \"GitHub Actions\"",
	}
	serverKeys = secretKind{
		group: "server-keys", prefix: "clts_", noun: "Server key", column: "KEY",
		purpose: "Give it to your servers as COLLECTOR_SERVER_KEY: it turns on sending logs.",
		short:   "Keys servers send logs with",
		example: "  collector server-keys list\n  collector server-keys create \"API servers\"",
	}
)

func newSecretsCmd(a *app, k secretKind) *cobra.Command {
	return group(a, k.group, k.short, k.example,
		newSecretsListCmd(a, k), newSecretsCreateCmd(a, k), newSecretsRevokeCmd(a, k))
}

func newSecretsListCmd(a *app, k secretKind) *cobra.Command {
	var l listFlags
	c := &cobra.Command{
		Use:     "list",
		Short:   "The project's " + lower(k.noun) + "s",
		Example: fmt.Sprintf("  collector %s list\n  collector %s list --project shop --json", k.group, k.group),
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			now := a.env.Now()
			return printList(ctx, a, cl, &l, api.ProjectPath(org, project)+"/"+k.group, nil, func(ss []api.Secret) string {
				rows := make([][]string, 0, len(ss))
				for _, s := range ss {
					used := "never"
					if s.LastUsedAt != nil {
						used = render.Ago(*s.LastUsedAt, now)
					}
					by := ""
					if s.CreatedBy != nil {
						by = s.CreatedBy.FullName()
					}
					rows = append(rows, []string{s.ID, s.Name, k.prefix + "…" + s.Hint, used, by, render.Ago(s.CreatedAt, now)})
				}
				return render.Table([]string{"ID", "NAME", k.column, "LAST USED", "CREATED BY", "CREATED"}, rows)
			}, fmt.Sprintf("No %ss. Create one with: collector %s create <name>", lower(k.noun), k.group))
		},
	}
	l.add(c)
	return c
}

func newSecretsCreateCmd(a *app, k secretKind) *cobra.Command {
	return &cobra.Command{
		Use:     "create <name>",
		Short:   "Create one; prints the secret once",
		Example: fmt.Sprintf("  collector %s create \"GitHub Actions\"", k.group),
		Args:    argsBetween(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			var s api.Secret
			raw, err := cl.Send(ctx, http.MethodPost, api.ProjectPath(org, project)+"/"+k.group, nil, map[string]string{"name": args[0]}, &s)
			if err != nil {
				return err
			}
			if a.flags.json {
				if err := a.printJSON(raw); err != nil {
					return err
				}
			} else {
				a.progress("%s %q (%s):", k.noun, render.Safe(s.Name), render.Safe(s.ID))
				a.out("%s", render.Safe(s.Secret))
			}
			fmt.Fprintf(a.env.Stderr, "This is the only time it's shown. %s\n", k.purpose)
			return nil
		},
	}
}

func newSecretsRevokeCmd(a *app, k secretKind) *cobra.Command {
	return &cobra.Command{
		Use:     "revoke <id>",
		Short:   "Revoke it",
		Example: fmt.Sprintf("  collector %s revoke Tk7x2Qp9LmZa", k.group),
		Args:    argsBetween(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, org, project, err := a.project(ctx, "")
			if err != nil {
				return err
			}
			if _, err := cl.Send(ctx, http.MethodDelete, api.ProjectPath(org, project)+"/"+k.group+"/"+api.Seg(args[0]), nil, nil, nil); err != nil {
				return err
			}
			a.out("Revoked.")
			return nil
		},
	}
}
