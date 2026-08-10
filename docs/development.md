# Development

How `topic` is built, tested and laid out.

## Implementation

`topic` is a single Go binary (`cmd/topic`). It began as ~1,750 lines of bash with 23
inline `python3 -c` blocks, and was migrated command by command behind the CLI
contract the test suite pins down — the tests never changed, which is what made each
step safe to take and easy to verify.

Two of this project's bugs came from bash itself: `pipefail` swallowing a failure so
`forget` silently did nothing, and `die`/`exit` inside `$( )` aborting a script with
no output at all. Those classes are gone. The transcript-title search also became
**~14× faster** (5s → 0.35s over 800MB) by scanning candidates in parallel instead of
shelling out per file.

What shell was good at — driving `tmux`, `claude` and `launchctl` — is now `os/exec`,
which turned out to be no worse. The startup-prompt heuristics that key on Claude
Code's on-screen text live in one place, `startupPatterns` in `session.go`: they have
changed several times and are the first thing to suspect when a launch misbehaves.

A build step is the cost. It is affordable because a broken binary cannot strand you:
tmux sessions outlive the tool entirely, so `ssh home -t tmux attach -t "<topic>"`
still reaches every running session, and `go build -o` leaves the previous working
binary in place if compilation fails.


## Tests

```bash
go test ./test/                                              # hermetic, seconds
TOPIC_TEST_PROFILE=~/.claude-personal go test ./test/        # + live sessions
go test ./test/ -short                                       # hermetic only
```

The suite drives the **CLI as a black box** — it runs `bin/topic` and asserts on exit
codes, output and on-disk state. That is deliberate: it tests the contract rather than
the implementation, so it keeps working as commands are migrated from bash to Go, and
serves as the specification and acceptance criteria for that migration.

Two tiers:

- **Hermetic** — a sandboxed registry (`CLAUDE_TOPICS_ROOT`) and config dir, no session
  ever launched. Covers each command's contract, plus one regression test per defect
  found on 2026-08-09 documenting the bug it locks out.
- **Live** — really launches Claude Code, because session initialisation (trust
  prompts, first-run setup, Remote Control bridges) is where the hard-won behaviour
  lives and cannot be faked. Needs `TOPIC_TEST_PROFILE` pointing at a **real
  logged-in** profile: credentials are per-profile, so a throwaway config dir is not
  logged in. The registry stays sandboxed, so live tests cannot touch real topics;
  they launch with no seed prompt where possible, so little or nothing is billed, and
  they clean up their local state completely.

  One thing they cannot clean up: each launch registers a **Remote Control session in
  the cloud**, which the CLI cannot delete. A `ZZ Test …` entry lingers in the Claude
  UI per launched session until deleted there — the same ghosting described under
  non-obvious mechanics. That is the main reason the live tier is opt-in: run it when
  you are changing session handling, not on every save. Includes `handoff-swap` and `fork` end to end, and the refusal to
  automate a profile that has not completed first-run setup.

Coverage is 17 of 18 commands (all but `ensure-dispatcher`, which launches the
Dispatcher on the developer's own machine) and every flag that changes behaviour
rather than wording. `topic cycle`'s `down`/`up` are deliberately untested: they
unload launchd agents, so a test run would stop the real Dispatchers.


## Layout

```
.claude-plugin/            marketplace manifest, so the plugin is installable
cmd/topic/                 the tool itself, in Go (built to bin/topic)
test/                      Go tests driving the CLI as a black box
plugin/                    the `topics` Claude Code plugin -> ~/.claude/skills/topics
  skills/handoff/          how to hand a topic to a fresh session
  skills/fork/             how to split a thread into its own topic
  skills/topic-manage/     pick up / put down / adopt / Dispatcher
launchd/                   Dispatcher agent template (__HOME__ substituted at install)
install.sh                 symlink installer
```

Runtime state lives outside the repo, in `~/.claude/topics/<slug>/`:
`topic.json` (name, dir, current session UUID, generation, history) and
`handoffs/*.md`.
