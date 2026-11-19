# Changelog

What changed in each release of `collector`, for the people who use it.

## 0.4.0

- `collector version` prints the version and checks now whether a newer
  one is out, with the command to upgrade; `--json` too.
- `threats show` prints the requests behind a detection as `logs` prints
  them, with the request id to look up in `collector logs --request`.

## 0.3.0

- `threat-rules add` and `rewrite` show the suggested name and
  description, the rule restated in one sentence, and the newest records
  its conditions match.
- At the prompt, type a change instead of `y` to tell Collector more;
  without a terminal, `--continue <token>` goes on from an earlier
  check, and with no words turns that version on.
- `--name` and `--description` replace the suggested ones.

## 0.2.0

- `threats` lists, shows and resolves what the threat rules caught.
- `threat-rules` lists the rules, writes your own in plain words
  (`add`, `rewrite`, checked and previewed before they turn on), turns
  them on and off, and sets a default's threshold.

## 0.1.2

- The new-version notice says how to upgrade the way collector was
  installed: Homebrew, apt, dnf, zypper, `go install` or the install
  script.

## 0.1.1

- `login` names the token "collector-cli on <host>", without the
  version, which went stale after an upgrade.

## 0.1.0

The Collector CLI, now one binary that needs no Node. It replaces the
TypeScript `@chrom-ar/collector-cli`, with the same commands and exit
codes, a few changes and a few new commands.

- `login`, `logout`, `whoami`. The token is kept in the system keychain
  (or a file only you can read where there's none). Sign in again: the
  TypeScript CLI's tokens aren't carried over.
- `projects list|create|show`, `dsn`, `upload-tokens`, `server-keys`,
  `origins` and `members list`.
- `issues list|show|events|resolve|ignore|reopen|assign`, with
  `--markdown` for an AI, and the new `issues event <id> <uuid>` for one
  event in full. `issues list` searches with words or `--search`; `-q` is
  now `--quiet`, as everywhere.
- `logs`, with `--follow` to stream new records live. Changes from the
  TypeScript CLI: `--since`/`--until` reject impossible dates, `--follow`
  refuses `--cursor` and `--all`, a line shows its date only when it
  differs from the line above, and request ids print whole.
- `upload` (source maps) and `test` (one test event). There's no Vite
  plugin: Vite builds use `define` and hidden source maps, then
  `collector upload --delete`.
- New: `orgs list|show|edit`; `members invite|role|remove|leave` and
  `invitations list|resend|revoke`; `projects edit|mute|unmute`; `audit`,
  the organization's audit log; `speed` (Web Vitals, the slowest pages and
  requests) and `signals` (when each environment was last heard from).
  Removing someone and leaving ask first, or take `--yes`.
- New: `collector open` opens the web app's page for a project, issue or
  log query; `collector api` makes a raw, authenticated request;
  `collector completion` for bash, zsh, fish and PowerShell.
- Hints (`More: --cursor …`, "only shown once") go to stderr, so stdout
  holds only results. `--json` errors are `{"error": {"code", "message"}}`
  on stderr.
- Install with `curl -fsSL https://packages.chrom.ar/collector/install.sh | sh`,
  Homebrew, `go install`, or Chroma's apt and dnf repositories at
  packages.chrom.ar.
- `collector use acme/shop` sets the organization and project commands
  use when nothing else says; after `login`, a line says where to go next.
- Text from the server is made safe for the terminal: line breaks show as
  ⏎ and escape sequences can't get through.
