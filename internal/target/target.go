// Package target chooses the organization and project a command works
// on: --org/--project, COLLECTOR_ORG/COLLECTOR_PROJECT, or the only one
// there is. When it's ambiguous, the error lists the choices.
package target

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/chrom-ar/collector-cli/internal/api"
)

// Errors that are usage mistakes: the person must say which, or gave
// something that isn't an issue.
var (
	ErrAmbiguous = errors.New("say which")
	ErrBadIssue  = errors.New("not an issue")
)

// API is what choosing needs from Collector.
type API interface {
	Me(ctx context.Context) (*api.Me, []byte, error)
	Projects(ctx context.Context, org string, q api.Query, all bool) (*api.Page[api.Project], error)
}

// Given is what a source (flags, environment) named.
type Given struct{ Org, Project string }

// Resolver chooses, asking Collector only when nothing named it, and only
// once.
type Resolver struct {
	API  API
	Flag Given // --org, --project
	Env  Given // COLLECTOR_ORG, COLLECTOR_PROJECT
	// Default is what collector use stored; its project belongs to its
	// org only.
	Default Given

	org string
}

// Org is the organization.
func (r *Resolver) Org(ctx context.Context) (string, error) {
	if g := first(r.Flag.Org, r.Env.Org, r.Default.Org); g != "" {
		return g, nil
	}
	if r.org != "" {
		return r.org, nil
	}
	me, _, err := r.API.Me(ctx)
	if err != nil {
		return "", err
	}
	slugs := make([]string, 0, len(me.Organizations))
	for _, o := range me.Organizations {
		slugs = append(slugs, o.Slug)
	}
	org, err := pick("organization", "you're in", slugs, "--org")
	r.org = org
	return org, err
}

// Project is the project in org.
func (r *Resolver) Project(ctx context.Context, org string) (string, error) {
	if g := first(r.Flag.Project, r.Env.Project); g != "" {
		return g, nil
	}
	if r.Default.Project != "" && org == r.Default.Org {
		return r.Default.Project, nil
	}
	page, err := r.API.Projects(ctx, org, api.Query{"limit": "100"}, false)
	if err != nil {
		return "", err
	}
	slugs := make([]string, 0, len(page.Items))
	for _, p := range page.Items {
		slugs = append(slugs, p.Slug)
	}
	return pick("project", org+" has", slugs, "--project")
}

// Both is the organization and its project.
func (r *Resolver) Both(ctx context.Context) (org, project string, err error) {
	if org, err = r.Org(ctx); err != nil {
		return "", "", err
	}
	project, err = r.Project(ctx, org)
	return org, project, err
}

// Explicit reports whether both were named, so nothing need be asked.
func (r *Resolver) Explicit() bool {
	org := first(r.Flag.Org, r.Env.Org, r.Default.Org)
	return org != "" && (first(r.Flag.Project, r.Env.Project) != "" || (r.Default.Project != "" && org == r.Default.Org))
}

func first(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

type choiceError struct{ msg string }

func (e *choiceError) Error() string        { return e.msg }
func (e *choiceError) Is(target error) bool { return target == ErrAmbiguous }

func pick(kind, where string, slugs []string, flag string) (string, error) {
	switch len(slugs) {
	case 0:
		return "", &choiceError{fmt.Sprintf("%s no %ss", where, kind)}
	case 1:
		return slugs[0], nil
	}
	return "", &choiceError{fmt.Sprintf("%s %d %ss; pass %s %s (or %s)", where, len(slugs), kind, flag, slugs[0], strings.Join(slugs[1:], ", "))}
}

// IssueRef is an issue named by id, or by its URL, which also names the
// organization and project.
type IssueRef struct{ ID, Org, Project string }

var (
	issueID   = regexp.MustCompile(`^[A-Za-z0-9]{6,32}$`)
	issuePath = regexp.MustCompile(`^/o/([^/]+)/p/([^/]+)/issues/([A-Za-z0-9]+)/?$`)
)

type issueError struct{ msg string }

func (e *issueError) Error() string        { return e.msg }
func (e *issueError) Is(target error) bool { return target == ErrBadIssue }

// ParseIssue reads a bare id or an issue URL on the Collector at origin.
func ParseIssue(ref, origin string) (IssueRef, error) {
	if issueID.MatchString(ref) {
		return IssueRef{ID: ref}, nil
	}
	u, err := url.Parse(ref)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return IssueRef{}, &issueError{fmt.Sprintf("%q isn't an issue id or an issue URL", ref)}
	}
	if o := u.Scheme + "://" + strings.ToLower(u.Host); o != origin {
		return IssueRef{}, &issueError{fmt.Sprintf("that issue is on %s, but the Collector in use is %s; pass --url %s", o, origin, o)}
	}
	m := issuePath.FindStringSubmatch(u.EscapedPath())
	if m == nil {
		return IssueRef{}, &issueError{fmt.Sprintf("%q isn't an issue URL (expected …/o/<org>/p/<project>/issues/<id>)", ref)}
	}
	org, err1 := url.PathUnescape(m[1])
	project, err2 := url.PathUnescape(m[2])
	if err1 != nil || err2 != nil {
		return IssueRef{}, &issueError{fmt.Sprintf("%q isn't an issue URL", ref)}
	}
	return IssueRef{ID: m[3], Org: org, Project: project}, nil
}
