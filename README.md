# Collector CLI

Manage [Collector](https://app.clt.chrom.ar) from the terminal: sign in,
set projects up, read and settle issues, follow logs live, and upload your
build's source maps. One static binary, for people, their coding agents
and CI.

```sh
collector login
collector issues list
collector issues show 2C7ZffP787kc --markdown > issue.md
```

> It replaces the TypeScript `@chrom-ar/collector-cli` (npm).

## Install

```sh
curl -fsSL https://packages.chrom.ar/collector/install.sh | sh        # macOS and Linux
brew install chrom-ar/tap/collector                         # or Homebrew
go install github.com/chrom-ar/collector-cli/cmd/collector@latest
```

The installer checks the download against the release's checksums and
puts `collector` in `/usr/local/bin` when it can, else `~/.local/bin`
(`COLLECTOR_INSTALL_DIR` picks another). Run it again to upgrade.

On Debian, Ubuntu and derivatives, add Chroma's apt repository once, and
`sudo apt upgrade` brings each new release:

```sh
curl -fsSL https://packages.chrom.ar/chroma.gpg | sudo tee /usr/share/keyrings/chroma.gpg >/dev/null
echo "deb [signed-by=/usr/share/keyrings/chroma.gpg] https://packages.chrom.ar/apt stable main" | sudo tee /etc/apt/sources.list.d/chroma.list
sudo apt update && sudo apt install collector
```

On Fedora, RHEL and CentOS Stream (`sudo dnf upgrade` brings new releases):

```sh
sudo curl -fsSLo /etc/yum.repos.d/chroma.repo https://packages.chrom.ar/rpm/chroma.repo
sudo dnf install collector
```

On openSUSE, `sudo zypper addrepo https://packages.chrom.ar/rpm/chroma.repo`
and `sudo zypper install collector`. Packages and repositories are signed;
see [chrom-ar/packages](https://github.com/chrom-ar/packages).

Every release also has archives and `.deb`, `.rpm` and `.apk` packages for
macOS, Linux and Windows on amd64 and arm64:
[releases](https://github.com/chrom-ar/collector-cli/releases).

## Signing in

```sh
collector login
```

It prints a one-time code and opens Collector in your browser (`--no-browser`
skips that, e.g. over SSH). Check that the code matches, approve, and the
terminal is signed in. For a self-hosted or local Collector, add
`--url http://localhost:4001`; later commands use the Collector you last
signed in to, until you pass another `--url`.

```sh
collector whoami    # who you are, the token's access, your organizations
collector logout    # revokes this machine's token and forgets it
```

The token is kept in the system keychain (macOS Keychain, Windows
Credential Manager, the Secret Service on Linux), or, where there's none,
in a file only you can read. If you used the TypeScript CLI, sign in
again: its tokens aren't carried over.

In CI and for agents, skip `login`: create a token in Collector's user
settings, under "Access tokens" (it can be limited to one organization and
to read-only), and set it as `COLLECTOR_TOKEN`, with `COLLECTOR_URL` for a
Collector other than app.clt.chrom.ar.

## Projects and setup

```sh
collector projects list
collector projects create "Web shop"       # prints the new project's DSN
collector projects show web
collector dsn                              # only the DSN: COLLECTOR_DSN=$(collector dsn)

collector upload-tokens create "GitHub Actions"   # prints the secret once
collector upload-tokens list
collector upload-tokens revoke <id>

collector server-keys create "API servers"  # for sending logs; prints the secret once
collector server-keys list
collector server-keys revoke <id>

collector origins list                      # the project's allowed domains
collector origins allow shop.example.com    # a bare host means https://
collector origins block http://localhost:3000
collector origins forget https://old.example.com

collector projects edit web --name Storefront
collector projects mute web                 # its issue emails off, for you (unmute: back on)
```

## Organizations and people

```sh
collector orgs list
collector orgs show acme
collector orgs edit acme --name "Acme Labs"          # owners; --slug changes its URLs

collector members list
collector members invite ana@example.com --role admin
collector members role bob@example.com admin
collector members remove bob@example.com             # asks first, or --yes
collector members leave acme                         # asks first, or --yes

collector invitations list
collector invitations resend <id>
collector invitations revoke <id>

collector audit                                      # who did what (owners, admins)
```

The organization and project come from `--org` and `--project` (or
`COLLECTOR_ORG` and `COLLECTOR_PROJECT`), else the default you set with
`collector use acme/shop` (per Collector; `collector use --clear` forgets
it). When you're in only one organization, or it has only one project,
you don't need any of them; when it's ambiguous, the error lists the
choices.

## Issues

```sh
collector issues list                                  # unresolved, seen this week
collector issues list --range all is not a function   # words search
collector issues list --env production --release "$GIT_SHA"   # what a deploy introduced
collector issues show 2C7ZffP787kc                     # or paste the issue's URL
collector issues show 2C7ZffP787kc --markdown          # everything, written for an AI
collector issues events 2C7ZffP787kc
collector issues event 2C7ZffP787kc <uuid>             # one event in full (--markdown too)
collector issues resolve 2C7ZffP787kc 9Hk2LmQp4RsT     # also: ignore, reopen
collector issues assign 2C7ZffP787kc me                # an email, me, or none
```

Filters for `issues list`: `--status unresolved|resolved|ignored`,
`--range day|week|month|all`, `--env`, `--platform elixir|javascript|node|other`,
`--release` (the release that introduced the issue) and `--search` (text
in the title, message or culprit; or give it as words). An issue id alone works without
`--org` and `--project`.

## Logs

What your servers sent: log lines, requests and sign-ins, in one timeline,
oldest at the top.

```sh
collector logs --since 15m                             # the last 15 minutes
collector logs --request F3xAbCdEfGh                   # one request: the id on its error
collector logs --ip 203.0.113.9 --since 1d             # what one address did
collector logs --status 5xx --env production
collector logs timeout                                 # words search messages, paths, accounts
collector logs -f --level warning                      # follow new records live
```

Filters: `--since`/`--until` (`15m`, `2h`, `1d` or an ISO time), `--env`,
`--server`, `--service`, `--request`, `--user`, `--ip`, `--account`,
`--status 404|5xx`, `--path`, `--level` (a minimum) and
`--type log,request,sign_in`. One page at a time (`Older: --cursor …` for
the next), or `--all`. `--markdown` gives Collector's text for an AI.
`--json` prints the API's JSON; with `--follow`, one record per line.

`--follow` reads the backlog, then streams new records until Ctrl-C. It
reconnects by itself, filling the gap, if the connection drops.

## Speed and signs of life

```sh
collector speed                       # Web Vitals at p75, the slowest pages and requests
collector speed --period month --device phone
collector speed page /checkout
collector speed request GET /api/cart
collector signals                     # when each environment's servers and browsers were last heard from
```

## Using it with an agent

```sh
collector issues list --env production --json
collector issues show 2C7ZffP787kc --markdown > issue.md
collector logs --request F3xAbCdEfGh --markdown      # the request it came from
claude "Fix the error in issue.md. When the fix is merged, run: collector issues resolve 2C7ZffP787kc"
```

- Give the agent a read-only token, or one limited to one organization,
  through `COLLECTOR_TOKEN`.
- `--json` on any command prints Collector's JSON as is. Lists show one
  page (`--limit` up to 100, then `--cursor`), or every page with `--all`.
  Errors under `--json` are `{"error": {"code", "message"}}` on stderr.
- Results go to stdout; hints, progress and warnings to stderr.
- `collector issues show <id> --markdown` gives the error, the stack trace
  with the code around your app's lines, the request, breadcrumbs and
  tags, written for an AI asked to fix it.
- Collector's messages and the Markdown follow your language in Collector
  (English or Spanish); `--lang en` or `--lang es` (or `COLLECTOR_LANG`)
  asks for one. The CLI's own text is English.
- `collector api <path>` makes a raw, authenticated request under
  `/api/v1`, for anything without a command of its own.
- `collector open issue <id>` prints (and, at a terminal, opens) the
  issue's page in the web app; also `open logs …`, `open speed`,
  `open settings`.

## Source maps

Upload **before you deploy**: events from a release whose maps aren't
uploaded yet stay minified.

```sh
COLLECTOR_UPLOAD_TOKEN=cltu_… COLLECTOR_DSN=https://clt_…@app.clt.chrom.ar \
  collector upload ./dist --release "$GIT_SHA"
```

- An **upload token** (`collector upload-tokens create`, it starts with
  `cltu_`) is read only from `COLLECTOR_UPLOAD_TOKEN`, never from a flag or
  a file. Keep it in your CI's secrets.
- The **DSN** (`--dsn` or `COLLECTOR_DSN`) says where Collector is.
- Or, instead of both, a **personal token** (`collector login`, or
  `COLLECTOR_TOKEN`) of an owner or admin of the project, with `--org` and
  `--project`. An upload token, when set, always wins.
- The **release** is the same string your app reports.

| Option | Default | |
|---|---|---|
| `--release` | `COLLECTOR_RELEASE` | Required. The release your app reports |
| `--dsn` | `COLLECTOR_DSN` | Your project's DSN |
| `--url-prefix` | `/` | The URL path the directory is served at, e.g. `/static/` |
| `--delete` | off | Delete the `.map` files (and the comments pointing at them) after a successful upload |
| `--dry-run` | off | List what would be uploaded, send nothing |

It finds every `.map` file under the directory and pairs it with its bundle
(by the bundle's `sourceMappingURL` comment, the map's `file` field, or the
name). Index maps (Turbopack) are flattened. Maps without `sourcesContent`
get a warning; maps over 20 MB are skipped.

With Vite, set the release at build time and keep the maps out of the
bundle, then upload and delete them:

```ts
// vite.config.ts
export default defineConfig({
  define: { __COLLECTOR_RELEASE__: JSON.stringify(process.env.GIT_SHA) },
  build: { sourcemap: "hidden" },
});
```

```sh
vite build && collector upload ./dist --release "$GIT_SHA" --delete
```

```sh
collector test --dsn "$COLLECTOR_DSN"   # sends one test event, the way a server does
```

## Global flags and environment

| Flag | Environment | |
|---|---|---|
| `--url` | `COLLECTOR_URL` | Which Collector. Default: the one last signed in to, else app.clt.chrom.ar |
| `--org`, `--project` | `COLLECTOR_ORG`, `COLLECTOR_PROJECT` | Which project |
| | `COLLECTOR_TOKEN` | A personal token; always wins over the stored one |
| `--json` | | Collector's JSON as is |
| `--lang en\|es` | `COLLECTOR_LANG` | Language of Collector's messages |
| `-q`, `--quiet` | | Only errors and the essential result |
| `-v`, `--verbose` | `COLLECTOR_DEBUG=1` | Requests and timings on stderr; never tokens |
| `--no-color` | `NO_COLOR` | |

`collector completion bash|zsh|fish|powershell` prints shell completions.

## Exit codes

`0` done; `1` Collector refused or failed (not found, not allowed,
validation, network); `2` a usage mistake; `3` not signed in, a rejected
or revoked token, or a read-only token asked to change something.
`upload` and `test` use only `0`, `1` and `2`.

## Development

```sh
make check          # gofmt, tidy, vet, staticcheck, golangci-lint, tests, govulncheck, cross-builds
make build          # bin/collector
make fixtures-check # the API fixtures match Collector's (../clt)
make fixtures-sync  # copy them over
make update         # latest dependencies and tools, then make check
make release        # tag the next patch version (BUMP=minor|major, or V=1.2.3); CI publishes
```

The tests run against a fake Collector built from fixtures that Collector
renders from its real controllers. `go test -tags live ./cmd/collector`
drives the binary against a local Collector (see `cmd/collector/live_test.go`).
