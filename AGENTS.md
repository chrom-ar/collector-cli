# Working in this repository

`collector` is the command-line client for Collector (`../clt`), in Go.
It replaces the TypeScript CLI in `../collector-js/packages/cli`.

**`SPEC.md` is the brief**: read it before writing anything. This file is
the short list of rules to follow while implementing it. Go 1.27.4 is
installed.

## The product, in one paragraph

A developer, their coding agent or CI manages Collector from the
terminal. `collector login` signs in by browser approval. `projects`,
`upload-tokens`, `server-keys` and `origins` set a project up. `issues`
and `logs` (with a live `--follow`) read what broke and fix its status.
`upload` sends source maps. Version 2 adds members, invitations,
organizations, the audit log, speed and signs of life. Output is
human-readable by default, `--json` for agents and `--markdown` for an
AI, with stable exit codes.

## Rules

### Process

- **Tests first.** Write the failing test and watch it fail for the
  right reason, then write the code.
- **Port behaviour, not code.** The TypeScript CLI's tests
  (`../collector-js/packages/cli/test`) are the checklist for version 1.
  Every case there gets a Go test or a written reason why not.
- **Run the whole gate** (`make check`) before saying something is done.
  Report what ran and what it printed.
- **Try it for real.** The final check of a command is the built binary
  against a local Collector (`http://localhost:4001`). Say plainly what
  was and wasn't tried.
- **Ask before deciding the product.** SPEC section 10 lists the open
  decisions, each with a recommendation. Follow it to keep moving, say
  that you did, and don't invent new product behaviour. Ask questions
  with options and a recommended default.

### Franco's working rules

- **Never commit or run git-modifying commands** (`add`, `commit`,
  `branch`, `tag`, `push`) unless asked. End every change with a
  suggested commit subject of 50 characters or less, and the `git add`
  command for Franco to run.
- **Never stage `docs/`** (specs and plans). Leave it out of `git add`
  and commit suggestions, without mentioning it.
- **Latest dependencies.** Add each at its latest version.
- **CHANGELOG under `## Unreleased`.** Bump versions only when Franco
  cuts a release. On a release, give him the commands to run; never
  publish yourself.

### Go

- The standard library first.
- Tools run through the `tool` directive.
- Everything goes under `internal/`.
- `context.Context` comes first, and every request has a timeout.
- Wrap errors and inspect them with `errors.Is`/`As`.
- No package-level state, and no `os.Exit` outside `main`.
- The gate is `gofmt`, `go vet`, `staticcheck` and `golangci-lint`.

The one extra dependency is the WebSocket client (SPEC 8.2).

### Safety

- **A token never appears** in output, logs, errors, `--verbose`, test
  fixtures or commits. The token type redacts itself.
- **`COLLECTOR_UPLOAD_TOKEN` is read only from the environment.**
- **Text from the server is untrusted.** Neutralize control characters
  before printing it to a terminal (SPEC 4.7).
- Tests never touch the developer's real config, keychain or network.
- Never point tests or experiments at `app.clt.chrom.ar`. Use a local
  Collector.

### Output

- Results go to stdout; everything else goes to stderr.
- `--json` is Collector's JSON as is, a public contract.
- Exit codes as in SPEC 4.6. They are tested.
- Every command's `--help` has an example.

### The server

- `../clt/docs/api.md` is the binding contract. Where SPEC.md
  disagrees, the contract wins.
- New endpoints (SPEC 7.3) are built in `../clt`, by its `AGENTS.md`,
  by the Clt session. Don't work around a missing endpoint from the CLI.
  Agree the shape with the Clt session, and have `docs/api.md` updated,
  before writing the command.
- Local credentials for a local Collector are fine to use in testing.
  Coordinate with the Clt session for seeded data and test tokens.

## Suggested order

1. Skeleton: module, `cmd/collector`, `--version`, `--help`, the gate, CI.
2. `internal/api`: the client, its errors, retries, timeouts and `308`s,
   against `httptest` and the first fixtures.
3. `internal/auth`: the device flow with a fake clock, and token storage,
   including reading the TypeScript CLI's credentials file.
4. `login`, `logout`, `whoami`, end to end with `testscript`.
5. `internal/target`, then `projects`, `dsn`, `upload-tokens`,
   `server-keys` and `origins`.
6. `issues`, all verbs, with `--markdown`.
7. `logs`, then `internal/channel` and `internal/follow` for `--follow`
   (SPEC 4.3, the hardest part).
8. `internal/sourcemaps`, `upload` and `test`.
9. Releases: GoReleaser, the Homebrew tap, the npm packaging.
10. Version 2, endpoint by endpoint with the Clt session (SPEC 10.7 for
    the order).
