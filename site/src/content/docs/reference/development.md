---
title: Development
description: How topic is built, tested and laid out.
---

## Building

```bash
make build      # bin/topic, signed (scripts/build.sh)
make test       # build, then the black-box suite
./install.sh    # build and install the copy into this profile; --all for every profile
```

Always build through `make build` or `./install.sh`, never a bare `go build`: see
[Code signing](/claude-topics/running/signing). `make doctor` runs `doctor` against `bin/topic`, and
`make clean` removes the build.

## Implementation

`topic` is a single Go binary (`cmd/topic`). It began as about 1,750 lines of bash with 23 inline
`python3 -c` blocks, and was migrated command by command behind the CLI contract the black-box
suite pins down. The tests never changed, which is what made each step safe to take and easy to
verify.

Two of the project's early bugs came from bash itself: `pipefail` swallowing a failure so `forget`
silently did nothing, and `die`/`exit` inside `$( )` aborting a script with no output at all. Those
classes are gone. The transcript-title search also became about 14× faster (5s → 0.35s over 800MB)
by scanning candidates in parallel instead of shelling out per file.

What shell was good at (driving `tmux`, `claude`, `launchctl` and `schtasks`) is `os/exec`, which
turned out to be no worse. Platform differences sit behind `platform_unix.go` and
`platform_windows.go`. The startup-prompt heuristics that key on Claude Code's on-screen text live
in one place, `startupPatterns` in `session.go`: they have changed several times and are the first
thing to suspect when a launch misbehaves.

A broken build cannot strand you: tmux sessions outlive the tool entirely, so
`ssh home -t tmux attach -t "<topic>"` still reaches every running session, and the build only
replaces `bin/topic` once the new one is compiled and signed.

## Tests

```bash
go test ./cmd/topic/                                         # unit tests
make test                                                    # build, then black-box (hermetic + live if configured)
go test ./test/ -short                                       # black-box, hermetic only (needs bin/topic built)
TOPIC_TEST_PROFILE=~/.claude-personal go test ./test/        # + live sessions
go test -race ./...                                          # everything
```

**Unit tests** in `cmd/topic/` cover the parts that can be driven in-process: argument handling,
the daemon keeper and orphan detection, the Slack hub and shim (against a fake Slack), startup
pattern matching, session sync, restore, and the UI's rows.

**The black-box suite** in `test/` drives the **CLI as a black box**: it runs `bin/topic` and
asserts on exit codes, output and on-disk state. That tests the contract rather than the
implementation. Two tiers:

- **Hermetic:** a sandboxed registry (`CLAUDE_TOPICS_ROOT`) and config dir, and no Claude session
  ever launched (the UI tests start private tmux servers, and skip without tmux). Covers each
  command's contract, plus regression tests documenting the bugs they lock out.
- **Live:** really launches Claude Code, because session initialisation (trust prompts, first-run
  setup, Remote Control bridges) is where the hard-won behaviour lives and cannot be faked. Needs
  `TOPIC_TEST_PROFILE` pointing at a **real logged-in** profile: credentials are per-profile, so a
  throwaway config dir is not logged in. The registry stays sandboxed, so live tests cannot touch
  real topics; they launch with no seed prompt where possible, so little or nothing is billed, and
  they clean up their local state. Covers handoff-swap and fork end to end, rebridge, claude
  arguments surviving down and up, and the refusal to automate a profile that has not completed
  first-run setup.

  One thing they cannot clean up: each launch registers a **Remote Control session in the cloud**,
  which the CLI cannot delete. A `ZZ Test …` entry lingers in the Claude UI per launched session
  until deleted there. That is the main reason the live tier is opt-in: run it when you are changing
  session handling, not on every save.

Not covered by either: `cycle down`/`up` and `ensure-dispatcher`, which act on the real launchd
agents and Dispatchers, and `doctor`.

## Layout

```
cmd/topic/                 the tool, in Go, with its unit tests (built to bin/topic)
test/                      the black-box suite
plugin/                    the topics plugin, linked to <profile>/skills/topics
  skills/handoff/          how to hand a topic to a fresh session
  skills/fork/             how to split a thread into its own topic
  skills/topic-manage/     pick up / put down / adopt / move / the Dispatcher / Slack
  bin/topic-bootstrap      reports or installs the CLI, for plugin-only installs
.claude-plugin/            marketplace manifest, so the plugin is installable
launchd/                   the macOS agent template
windows/                   the Task Scheduler task template
scripts/build.sh           the only supported build: compile, sign, verify
install.sh, install.ps1    installers for macOS and Windows
Makefile                   build, test, install, doctor, clean
site/                      this documentation site (Astro + Starlight)
```

## This site

`site/` is [Astro](https://astro.build) and [Starlight](https://starlight.astro.build), published
to GitHub Pages at `v3rv.com/claude-topics` by `.github/workflows/docs.yml` on each push to `main`
that touches it.

```bash
cd site
pnpm install
pnpm dev        # http://localhost:4321/claude-topics
pnpm build      # dist/, and checks every internal link
```

Pages are markdown in `site/src/content/docs/`. The sidebar is declared in `astro.config.mjs`; add
new pages there. Internal links carry the base: `/claude-topics/topics/handoff`. The build fails on
a broken one.
