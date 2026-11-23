package target

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrom-ar/collector-cli/internal/api"
)

type fakeAPI struct {
	orgs, projects []string
	meCalls        int
	projCalls      []api.Query
}

func (f *fakeAPI) Me(context.Context) (*api.Me, []byte, error) {
	f.meCalls++
	me := &api.Me{}
	for _, o := range f.orgs {
		me.Organizations = append(me.Organizations, api.Organization{Slug: o})
	}
	return me, nil, nil
}

func (f *fakeAPI) Projects(_ context.Context, _ string, q api.Query, _ bool) (*api.Page[api.Project], error) {
	f.projCalls = append(f.projCalls, q)
	p := &api.Page[api.Project]{}
	for _, s := range f.projects {
		p.Items = append(p.Items, api.Project{Slug: s})
	}
	return p, nil
}

func TestOrg(t *testing.T) {
	ctx := context.Background()
	one := &fakeAPI{orgs: []string{"acme"}}
	if got, _ := (&Resolver{API: one, Flag: Given{Org: "flag"}, Env: Given{Org: "env"}}).Org(ctx); got != "flag" {
		t.Errorf("flag: %s", got)
	}
	if got, _ := (&Resolver{API: one, Env: Given{Org: "env"}}).Org(ctx); got != "env" {
		t.Errorf("env: %s", got)
	}
	if one.meCalls != 0 {
		t.Error("asked /me with the org given")
	}
	r := &Resolver{API: one}
	if got, _ := r.Org(ctx); got != "acme" {
		t.Errorf("only: %s", got)
	}
	_, _ = r.Org(ctx)
	if one.meCalls != 1 {
		t.Errorf("/me asked %d times, want once", one.meCalls)
	}

	_, err := (&Resolver{API: &fakeAPI{orgs: []string{"a", "b", "c"}}}).Org(ctx)
	if !errors.Is(err, ErrAmbiguous) || err.Error() != "you're in 3 organizations; pass --org a (or b, c)" {
		t.Errorf("several: %v", err)
	}
	_, err = (&Resolver{API: &fakeAPI{}}).Org(ctx)
	if !errors.Is(err, ErrAmbiguous) || err.Error() != "you're in no organizations" {
		t.Errorf("none: %v", err)
	}
}

func TestProject(t *testing.T) {
	ctx := context.Background()
	one := &fakeAPI{orgs: []string{"acme"}, projects: []string{"web"}}
	if got, _ := (&Resolver{API: one, Flag: Given{Project: "p"}}).Project(ctx, "acme"); got != "p" {
		t.Errorf("flag: %s", got)
	}
	if got, _ := (&Resolver{API: one, Env: Given{Project: "q"}}).Project(ctx, "acme"); got != "q" {
		t.Errorf("env: %s", got)
	}
	if len(one.projCalls) != 0 {
		t.Error("listed projects with the project given")
	}
	if got, _ := (&Resolver{API: one}).Project(ctx, "acme"); got != "web" {
		t.Errorf("only: %s", got)
	}
	if one.projCalls[0]["limit"] != "100" {
		t.Errorf("query = %v", one.projCalls[0])
	}
	_, err := (&Resolver{API: &fakeAPI{projects: []string{"web", "api"}}}).Project(ctx, "acme")
	if err == nil || err.Error() != "acme has 2 projects; pass --project web (or api)" {
		t.Errorf("several: %v", err)
	}
	_, err = (&Resolver{API: &fakeAPI{}}).Project(ctx, "acme")
	if err == nil || err.Error() != "acme has no projects" {
		t.Errorf("none: %v", err)
	}
}

func TestBoth(t *testing.T) {
	r := &Resolver{API: &fakeAPI{orgs: []string{"acme"}, projects: []string{"web"}}}
	org, project, err := r.Both(context.Background())
	if err != nil || org != "acme" || project != "web" {
		t.Errorf("got %s/%s, %v", org, project, err)
	}
}

func TestIssueRef(t *testing.T) {
	const origin = "https://app.clt.chrom.ar"
	got, err := ParseIssue("2C7ZffP787kc", origin)
	if err != nil || got != (IssueRef{ID: "2C7ZffP787kc"}) {
		t.Errorf("bare: %+v, %v", got, err)
	}
	got, err = ParseIssue(origin+"/o/acme/p/web/issues/2C7ZffP787kc?x=1", origin)
	if err != nil || got != (IssueRef{ID: "2C7ZffP787kc", Org: "acme", Project: "web"}) {
		t.Errorf("url: %+v, %v", got, err)
	}
	for _, bad := range []string{
		"http://localhost:4001/o/a/p/b/issues/X",
		origin + "/o/acme/p/web",
		"not an id!",
		"",
	} {
		if _, err := ParseIssue(bad, origin); !errors.Is(err, ErrBadIssue) {
			t.Errorf("ParseIssue(%q) = %v, want ErrBadIssue", bad, err)
		}
	}
	_, err = ParseIssue("http://localhost:4001/o/a/p/b/issues/X", origin)
	if err == nil || err.Error() != "that issue is on http://localhost:4001, but the Collector in use is "+origin+"; pass --url http://localhost:4001" {
		t.Errorf("other Collector: %v", err)
	}
}

// A stored default (collector use) comes after flags and the environment,
// before asking Collector.
func TestDefaultComesAfterFlagsAndEnv(t *testing.T) {
	ctx := context.Background()
	f := &fakeAPI{orgs: []string{"a", "b"}, projects: []string{"x", "y"}}
	r := &Resolver{API: f, Default: Given{Org: "a", Project: "y"}}
	org, project, err := r.Both(ctx)
	if err != nil || org != "a" || project != "y" {
		t.Errorf("default: %s/%s, %v", org, project, err)
	}
	if f.meCalls != 0 || len(f.projCalls) != 0 {
		t.Error("asked Collector with a default set")
	}
	if !r.Explicit() {
		t.Error("a default org and project count as named")
	}
	r = &Resolver{API: f, Flag: Given{Org: "b"}, Env: Given{Project: "x"}, Default: Given{Org: "a", Project: "y"}}
	if org, project, _ := r.Both(ctx); org != "b" || project != "x" {
		t.Errorf("flag and env: %s/%s", org, project)
	}
	// A default project belongs to its default org only.
	r = &Resolver{API: f, Flag: Given{Org: "b"}, Default: Given{Org: "a", Project: "y"}}
	if _, err := r.Project(ctx, "b"); err == nil || !strings.Contains(err.Error(), "b has 2 projects") {
		t.Errorf("another org: %v", err)
	}
}

// The order: --org/--project, COLLECTOR_ORG/COLLECTOR_PROJECT, the
// project file, collector use, then the only one there is.
func TestPrecedence(t *testing.T) {
	ctx := context.Background()
	f := &fakeAPI{orgs: []string{"a", "b"}, projects: []string{"x", "y"}}
	file := Given{Org: "f", Project: "fp"}
	def := Given{Org: "d", Project: "dp"}
	cases := []struct {
		name              string
		r                 Resolver
		org, project      string
		orgFrom, projFrom Layer
	}{
		{"file beats use", Resolver{File: file, Default: def}, "f", "fp", FromFile, FromFile},
		{"env beats file", Resolver{Env: Given{Org: "e", Project: "ep"}, File: file, Default: def}, "e", "ep", FromEnv, FromEnv},
		{"flag beats env", Resolver{Flag: Given{Org: "g"}, Env: Given{Org: "e", Project: "ep"}, File: file}, "g", "ep", FromFlag, FromEnv},
		{"file project needs its org", Resolver{Flag: Given{Org: "d"}, File: file, Default: def}, "d", "dp", FromFlag, FromDefault},
		{"org-only file, use's project for that org", Resolver{File: Given{Org: "d"}, Default: def}, "d", "dp", FromFile, FromDefault},
	}
	for _, c := range cases {
		c.r.API = f
		org, orgFrom, project, projFrom := c.r.Named()
		if org != c.org || project != c.project || orgFrom != c.orgFrom || projFrom != c.projFrom {
			t.Errorf("%s: Named() = %s (%v), %s (%v)", c.name, org, orgFrom, project, projFrom)
		}
		gotOrg, gotProject, err := c.r.Both(ctx)
		if err != nil || gotOrg != c.org || gotProject != c.project {
			t.Errorf("%s: Both() = %s/%s, %v", c.name, gotOrg, gotProject, err)
		}
	}
	if f.meCalls != 0 || len(f.projCalls) != 0 {
		t.Error("asked Collector though something named them")
	}
	r := Resolver{API: f}
	if org, from, project, pfrom := r.Named(); org != "" || from != FromNone || project != "" || pfrom != FromNone {
		t.Errorf("nothing named: %s %v %s %v", org, from, project, pfrom)
	}
}

func TestFindProjectFile(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "app", "web", "src")
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, path, err := FindFile(deep); err != nil || path != "" {
		t.Fatalf("none: %q, %v", path, err)
	}
	if err := WriteFile(filepath.Join(root, "app"), Given{Org: "acme", Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	g, path, err := FindFile(deep)
	if err != nil || path != filepath.Join(root, "app", FileName) || g.Org != "acme" || g.Project != "shop" {
		t.Fatalf("found %+v at %q, %v", g, path, err)
	}
	// The nearest wins.
	if err := WriteFile(filepath.Join(root, "app", "web"), Given{Org: "acme"}); err != nil {
		t.Fatal(err)
	}
	if g, _, _ := FindFile(deep); g.Org != "acme" || g.Project != "" {
		t.Errorf("nearest: %+v", g)
	}
}

func TestParseProjectFile(t *testing.T) {
	g, err := parseFile("# Collector\norg = \"acme\"   # the org\n\nproject='my shop'\n")
	if err != nil || g.Org != "acme" || g.Project != "my shop" {
		t.Errorf("parsed %+v, %v", g, err)
	}
	for _, bad := range []string{
		"org = acme\n",
		"team = \"acme\"\n",
		"org = \"acme\"\norg = \"other\"\n",
		"project = \"shop\"\n",
		"[collector]\norg = \"acme\"\n",
		"org = \"\"\n",
	} {
		if _, err := parseFile(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	// What WriteFile writes reads back, quotes and all.
	dir := t.TempDir()
	want := Given{Org: `a"b`, Project: `c\d`}
	if err := WriteFile(dir, want); err != nil {
		t.Fatal(err)
	}
	if g, _, err := FindFile(dir); err != nil || g.Org != want.Org || g.Project != want.Project {
		t.Errorf("round trip: %+v, %v", g, err)
	}
}
