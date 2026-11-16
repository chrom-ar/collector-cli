package cli

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/chrom-ar/collector-cli/internal/api"
	"github.com/chrom-ar/collector-cli/internal/render"
	"github.com/spf13/cobra"
)

func newMembersCmd(a *app) *cobra.Command {
	return group(a, "members", "People in the organization",
		"  collector members list\n  collector members invite new@example.com --role admin\n  collector members role bob@example.com admin",
		newMembersListCmd(a), newMembersInviteCmd(a), newMembersRoleCmd(a), newMembersRemoveCmd(a), newMembersLeaveCmd(a))
}

// orgClient is a signed-in client and the organization (--org, or the
// only one).
func (a *app) orgClient(ctx context.Context) (*api.Client, string, error) {
	return a.orgArg(ctx, nil)
}

func newMembersListCmd(a *app) *cobra.Command {
	var l listFlags
	c := &cobra.Command{
		Use:     "list",
		Short:   "Members and their roles",
		Example: "  collector members list\n  collector members list --org acme --json",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cl, org, err := a.orgClient(ctx)
			if err != nil {
				return err
			}
			return printList(ctx, a, cl, &l, api.OrgPath(org)+"/members", nil, func(ms []api.Member) string {
				rows := make([][]string, 0, len(ms))
				for _, m := range ms {
					rows = append(rows, []string{m.Email, m.FullName(), m.Role})
				}
				return render.Table([]string{"EMAIL", "NAME", "ROLE"}, rows)
			}, "No members in "+render.Safe(org)+".")
		},
	}
	l.add(c)
	return c
}

// member finds someone in org by email (any case) or user id.
func (a *app) member(ctx context.Context, cl *api.Client, org, who string) (*api.Member, error) {
	page, err := api.List[api.Member](ctx, cl, api.OrgPath(org)+"/members", nil, true)
	if err != nil {
		return nil, err
	}
	for _, m := range page.Items {
		if strings.EqualFold(m.Email, who) || m.ID == who {
			return &m, nil
		}
	}
	return nil, &api.Error{Status: http.StatusNotFound, Code: "not_found",
		Message: fmt.Sprintf("%s isn't a member of %s", render.Safe(who), render.Safe(org))}
}

var roles = map[string]bool{"member": true, "admin": true, "owner": true}

func checkRole(role string) error {
	if !roles[role] {
		return usageError{fmt.Errorf("unknown role %q: use member, admin or owner", role)}
	}
	return nil
}

func newMembersInviteCmd(a *app) *cobra.Command {
	var role string
	c := &cobra.Command{
		Use:   "invite <email>...",
		Short: "Invite people, by email (owners and admins)",
		Long: "Invite people to the organization; each gets an email with a link that\n" +
			"lasts seven days. Inviting an address again replaces its pending\n" +
			"invitation.",
		Example: "  collector members invite new@example.com\n  collector members invite ana@example.com bo@example.com --role admin",
		Args:    argsBetween(1, 100),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkRole(role); err != nil {
				return err
			}
			ctx := cmd.Context()
			cl, org, err := a.orgClient(ctx)
			if err != nil {
				return err
			}
			return a.each(ctx, args, func(email string) ([]byte, string, error) {
				var inv api.Invitation
				raw, err := cl.Send(ctx, http.MethodPost, api.OrgPath(org)+"/invitations", nil, map[string]string{"email": email, "role": role}, &inv)
				return raw, fmt.Sprintf("Invited %s to %s as %s (%s).", inv.Email, org, inv.Role, inv.ID), err
			})
		},
	}
	c.Flags().StringVar(&role, "role", "member", "member, admin or owner")
	return c
}

// each runs do for every argument in order, one line per success; any
// failure is reported and the rest still go. It exits 1, or 3 if the
// token was refused.
func (a *app) each(ctx context.Context, args []string, do func(arg string) ([]byte, string, error)) error {
	code := ExitOK
	for _, arg := range args {
		raw, line, err := do(arg)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			a.reportError(fmt.Errorf("%s: %w", render.Safe(arg), err))
			if exitCode(err) == ExitAuth {
				code = ExitAuth
			} else if code != ExitAuth {
				code = ExitError
			}
			continue
		}
		if a.flags.json {
			if len(raw) > 0 {
				if err := a.printJSON(raw); err != nil {
					return err
				}
			}
			continue
		}
		a.out("%s", render.Safe(line))
	}
	if code != ExitOK {
		return silentError{code}
	}
	return nil
}

func newMembersRoleCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "role <email> <member|admin|owner>",
		Short:   "Change someone's role (owners and admins)",
		Long:    "Change someone's role. The web app's rules apply: owners change anyone;\nadmins change admins and members, never owners; nobody changes their own.",
		Example: "  collector members role bob@example.com admin",
		Args:    argsBetween(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkRole(args[1]); err != nil {
				return err
			}
			ctx := cmd.Context()
			cl, org, err := a.orgClient(ctx)
			if err != nil {
				return err
			}
			m, err := a.member(ctx, cl, org, args[0])
			if err != nil {
				return err
			}
			var out api.Member
			raw, err := cl.Send(ctx, http.MethodPatch, api.OrgPath(org)+"/members/"+api.Seg(m.ID), nil, map[string]string{"role": args[1]}, &out)
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(raw)
			}
			a.out("%s is now %s in %s.", render.Safe(out.Email), render.Safe(out.Role), render.Safe(org))
			return nil
		},
	}
}

func newMembersRemoveCmd(a *app) *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:     "remove <email>",
		Short:   "Remove someone from the organization (owners and admins)",
		Example: "  collector members remove bob@example.com\n  collector members remove bob@example.com --yes",
		Args:    argsBetween(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, org, err := a.orgClient(ctx)
			if err != nil {
				return err
			}
			m, err := a.member(ctx, cl, org, args[0])
			if err != nil {
				return err
			}
			if err := a.confirm(yes, fmt.Sprintf("Remove %s from %s?", render.Safe(m.Email), render.Safe(org))); err != nil {
				return err
			}
			if _, err := cl.Send(ctx, http.MethodDelete, api.OrgPath(org)+"/members/"+api.Seg(m.ID), nil, nil, nil); err != nil {
				return err
			}
			a.out("Removed %s from %s.", render.Safe(m.Email), render.Safe(org))
			return nil
		},
	}
	addYes(c, &yes)
	return c
}

func newMembersLeaveCmd(a *app) *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:     "leave [<org>]",
		Short:   "Leave the organization",
		Example: "  collector members leave acme\n  collector members leave acme --yes",
		Args:    argsBetween(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, org, err := a.orgArg(ctx, args)
			if err != nil {
				return err
			}
			if err := a.confirm(yes, fmt.Sprintf("Leave %s? Only an owner or admin can bring you back.", render.Safe(org))); err != nil {
				return err
			}
			if _, err := cl.Send(ctx, http.MethodDelete, api.OrgPath(org)+"/members/me", nil, nil, nil); err != nil {
				return err
			}
			a.out("You left %s.", render.Safe(org))
			return nil
		},
	}
	addYes(c, &yes)
	return c
}

func newInvitationsCmd(a *app) *cobra.Command {
	return group(a, "invitations", "Pending invitations to the organization",
		"  collector invitations list\n  collector invitations resend Nvt7Kq2XpAb2",
		newInvitationsListCmd(a), newInvitationsResendCmd(a), newInvitationsRevokeCmd(a))
}

func newInvitationsListCmd(a *app) *cobra.Command {
	var l listFlags
	c := &cobra.Command{
		Use:     "list",
		Short:   "Pending invitations, newest first",
		Example: "  collector invitations list\n  collector invitations list --json",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cl, org, err := a.orgClient(ctx)
			if err != nil {
				return err
			}
			loc := a.env.Location
			return printList(ctx, a, cl, &l, api.OrgPath(org)+"/invitations", nil, func(is []api.Invitation) string {
				rows := make([][]string, 0, len(is))
				for _, i := range is {
					by := ""
					if i.InvitedBy != nil {
						by = i.InvitedBy.FullName()
					}
					rows = append(rows, []string{i.ID, i.Email, i.Role, by, render.Time(i.ExpiresAt, loc, "2006-01-02 15:04")})
				}
				return render.Table([]string{"ID", "EMAIL", "ROLE", "INVITED BY", "EXPIRES"}, rows)
			}, "No pending invitations in "+render.Safe(org)+".")
		},
	}
	l.add(c)
	return c
}

func newInvitationsResendCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "resend <id>",
		Short:   "Send it again, with a fresh link and seven more days",
		Example: "  collector invitations resend Nvt7Kq2XpAb2",
		Args:    argsBetween(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, org, err := a.orgClient(ctx)
			if err != nil {
				return err
			}
			var inv api.Invitation
			raw, err := cl.Send(ctx, http.MethodPost, api.OrgPath(org)+"/invitations/"+api.Seg(args[0])+"/resend", nil, nil, &inv)
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(raw)
			}
			a.out("Sent again to %s; it expires %s.", render.Safe(inv.Email), render.Time(inv.ExpiresAt, a.env.Location, "2006-01-02 15:04"))
			return nil
		},
	}
}

func newInvitationsRevokeCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "revoke <id>",
		Short:   "Revoke it: its link stops working",
		Example: "  collector invitations revoke Nvt7Kq2XpAb2",
		Args:    argsBetween(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cl, org, err := a.orgClient(ctx)
			if err != nil {
				return err
			}
			if _, err := cl.Send(ctx, http.MethodDelete, api.OrgPath(org)+"/invitations/"+api.Seg(args[0]), nil, nil, nil); err != nil {
				return err
			}
			a.out("Revoked.")
			return nil
		},
	}
}
