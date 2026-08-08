# claude-topics

Pick up, put down, and hand off long-running Claude Code work — from anywhere,
including your phone.

Two tools plus a Claude Code plugin:

| Piece | What it is |
|-------|------------|
| `topic` | Manage **topics**: stable names that outlive the session beneath them |
| `topics` plugin | Skills (`handoff`, `topic-manage`) so Claude knows the procedures |

## The problem this solves

You leave Claude Code sessions running for a week so you can continue the dialogue
from your phone while travelling. Two things go wrong:

1. **Context rots.** A week-old session is cluttered with detail that no longer
   matters, but you don't want to lose the thread.
2. **You can't start new work remotely.** If you need a session that isn't already
   running, you're stuck until you're back at the machine.

`topic` fixes both, by making the unit of work a *topic* rather than a session — the name stays stable while the session underneath is resumed, retired,
or replaced.

## Install

```bash
git clone <this repo> ~/dev/claude-topics
cd ~/dev/claude-topics
./install.sh
```

Everything installs as a **symlink back into the repo**, so editing a script here
takes effect immediately. Requires `tmux`, `claude`, `python3`, and `~/.local/bin`
on your `PATH`.

`./install.sh --uninstall` removes the links and the launchd agent. It deliberately
leaves `~/.claude/topics/` alone — that's your registry and handoff docs, not the tool.

## `topic` — the tool

```bash
topic up     "<name>" [dir]   # pick up: resume the topic's session, or start one
topic down   "<name>"         # put down: terminate, but stay resumable
topic list                    # what's up, what's down
topic status "<name>"         # dir, session UUID, generation, retired sessions
topic forget "<name>"         # stop tracking it (keeps handoff docs)
topic prune                   # show topics that are dead; --yes removes them

topic up "<name>" [dir] --seed-from <file>    # start a NEW topic from a brief
```

`--seed-from` seeds a brand-new topic with a document — a handoff doc, an `ideas/`
note, an issue write-up. It is what makes a **split** possible: hand one thread off
to the existing topic, and start a second topic from its own brief, each in its own
working directory. Ignored (with a warning) if the topic already has a session to
resume.

`forget` is the disposal path for one topic you have decided you are done with. It
refuses while the topic is up, keeps the handoff documents unless you pass `--purge`
(they are often the only surviving record of what a topic was about), and never
touches the session itself — it stays resumable with `claude --resume <uuid>`.

`prune` is the disposal path for topics that are dead whether you decided so or not:
down, with no transcript left in `~/.claude/projects`, so `up` could never resume
them. It is a **dry run by default** — it prints what it would remove and stops.
`--yes` carries it out (via `forget`, so handoff docs survive unless you add
`--purge`). Live and merely-put-down topics are never touched.

```bash
topic prune                   # what is dead?
topic prune --yes             # remove them, keep their handoff docs
topic prune --yes --purge     # remove them and their handoff docs
```

```bash
ssh home -t tmux attach -t "<name>"       # attach to any topic over SSH
```

Each topic is backed by a stable session UUID, so `up` after `down` is a genuine
`--resume` — **lossless**. Putting a topic down is not destructive; do it freely.

Topics are singletons: `topic up` on a live topic is a no-op, not an error.

### Two access paths

Every session is launched with **both** `--remote-control "<name>"` and
`--name "<name>"`, giving two independent ways in:

| Path | Reach |
|------|-------|
| tmux | local attach, SSH attach, survives disconnect |
| Remote Control | claude.ai / phone, **and** other Claude sessions via `ListAgents` / `SendMessage` |

### Hand off — clearing context without losing the thread

Retires the current session and starts a fresh one under the same topic name, seeded
with a handoff document:

```bash
p=$(topic handoff-path "<name>")   # 1. where the doc goes
# 2. the session writes its handoff doc to $p
topic handoff-swap "<name>" --focus "the thing to pick up first"
```

In practice you don't run these by hand — you say `/handoff` (or
`/handoff <focus>`) and the `handoff` skill drives it.

`--focus` scopes what the successor starts on. Off-focus work is still recorded as
open threads, so nothing is silently dropped: an omitted thread is indistinguishable
from a finished one.

Handoff docs are kept permanently in `~/.claude/topics/<slug>/handoffs/`. Retired
session UUIDs are archived in the topic's `history`, so an old session is still
resumable in an emergency.

### The Dispatcher

An always-up topic whose only job is running `topic` commands for the others. It
solves the bootstrapping problem: you can't `topic up` remotely if nothing is up.

Kept alive by launchd — at login, plus a check every 5 minutes calling
`topic ensure-dispatcher` (idempotent). Supervision goes through that check rather
than `KeepAlive` because the real process lives inside detached tmux, which launchd
cannot watch directly.

```bash
topic ensure-dispatcher              # any session can revive it
tail -f ~/Library/Logs/claude-dispatcher.log
```

From your phone, you talk to the Dispatcher: *"pick up Fantasy Economics"*,
*"put down Fantasy Images"*, *"what topics are up?"*

### Adopting an existing session

A session started outside `topic` can be brought under management. Every live
session publishes `~/.claude/sessions/<pid>.json` — the authoritative
`name` → `sessionId` → `cwd` mapping — so adoption normally needs no probe and no
round trip:

```bash
topic adopt "<name>"                 # matches a live session whose name is <name>
topic adopt "<name>" --pid 61222     # or point straight at one
topic adopt "<name>" --dir ~/dev/x   # or narrow by working directory
```

Candidates are narrowed by `--pid`, then `--dir`, then by a name equal to the topic
name; if that doesn't leave exactly one, `topic` lists the live sessions with the
`--pid` needed to disambiguate. Records linger after a session exits, so liveness is
re-checked against the pid.

**A session that has exited can be adopted too** — anything `claude --resume` lists.
Its name is in its own transcript, as `custom-title` records, which is where
`--resume` reads it from. Same command; `topic` falls through to this automatically
when nothing live matches:

```bash
topic adopt "Fantasy UX"                      # a resumable session, not running
topic adopt "<name>" --title "<resume name>"  # topic name differs from the title
```

Sessions get **renamed**, and every rename stamps a new title, so only the *last* one
counts — a session titled "Fantasy UX" early on and "Fantasy OpenAI" now is not a
match. If several sessions genuinely share the title, `topic` lists them newest-first
with the `--session-id` to pick one. There is no process to claim, so the topic lands
in the ordinary `down` state and `topic up` simply resumes it.

The scan greps every transcript (~5s over 800MB here), so it is deliberately the
fallback, not the first thing tried.

`--probe` remains for a session that is running but *unnamed* — nothing to match on
in either direction. A running session doesn't hold its transcript open, so the probe
path sends it a unique token and finds which transcript that token landed in:

```bash
topic adopt "<name>" --probe <token> --exclude <your-own-session-uuid>
```

`--session-id <uuid>` takes a UUID you already know — a session adopting *itself*, or
your pick from an ambiguous list. It works for live and exited sessions alike:
`topic` checks whether that UUID is currently running and registers it accordingly.

`adopt` is non-destructive — it only records the UUID, directory, and pid.

**Then claim it.** The adopted session is still running outside tmux. Starting it
under `topic` would resume the same session UUID in a *second* process — two writers
on one transcript, which corrupts it.

```bash
topic up "<name>"           # after adoption via a live record
topic up "<name>" --claim   # after --probe / --session-id adoption
```

This step only applies to a session that is actually **running**. Adopting a
resumable, exited session needs no claim at all — it is already `down`.

Adoption through a live record stores the pid, so `topic up` can *see* the original:
it refuses outright while that process is alive (`--claim` will not override it), and
needs no assertion once it's gone. The probe and `--session-id` paths have no pid to
check, so they still fail closed until you assert the original has exited with
`--claim`.

## Non-obvious mechanics

Learned the hard way; don't re-derive them.

- **`--remote-control` alone does not name a session.** A session started with only
  `--remote-control "X"` advertises itself to peers as an auto-derived `<dir>-<hash>`
  and is unaddressable by name. You need `--name "X"` as well. `topic` passes both.
- **`SendMessage` resolves against the last `ListAgents` snapshot.** A freshly
  launched session is unreachable until you call `ListAgents` again. Call it first,
  then send — and expect to need the `[ref]` suffix.
- **Remote Control names live in the cloud, not on disk.** When such a session dies,
  its entry can linger in `ListAgents` as a ghost that accepts messages and never
  answers. Sessions launched by `topic` don't have this problem: `--name` writes the
  name to `~/.claude/sessions/<pid>.json`.
- **`~/.claude/sessions/<pid>.json`** is the authoritative live-session record —
  `name`, `sessionId`, `cwd`, `tmux`. Best starting point for any "which session is
  this?" question, and what `topic adopt` reads. Records are **not** removed when a
  session exits, so always re-check the pid before trusting one.
- **A session's name survives its process, inside its own transcript.** Transcripts
  carry `{"type":"custom-title","customTitle":"..."}` records — that is the name
  `claude --resume` lists. A **rename appends another one**, so the *last* record is
  the current name and earlier ones are names since abandoned. They also record their
  own `cwd`, which is the right way to recover a session's directory: de-mangling the
  `~/.claude/projects` slug is ambiguous for any hyphenated directory
  (`-Users-me-dev-claude-topics`).
- **`pipefail` + `set -e` turns a missing directory into a silent abort.** `ls`/`find`
  over an absent path exits non-zero inside a command substitution, and the script
  dies with no output and nothing done. `topic forget` failed exactly this way for
  every topic that had no handoff docs — it looked like a no-op. Terminate such
  pipelines with `|| true`.
- **Session start-up detection is text-matching.** `topic` polls the pane
  for the trust prompt or a readiness banner (`Welcome back` / `remote-control is
  active`). Claude Code's startup text has changed twice already — suspect this first
  if launches hang for the full timeout.

## Layout

```
bin/topic                  topic manager
plugin/                    the `topics` Claude Code plugin -> ~/.claude/skills/topics
  skills/handoff/          how to hand a topic to a fresh session
  skills/topic-manage/     pick up / put down / adopt / Dispatcher
launchd/                   Dispatcher agent template (__HOME__ substituted at install)
install.sh                 symlink installer
```

Runtime state lives outside the repo, in `~/.claude/topics/<slug>/`:
`topic.json` (name, dir, current session UUID, generation, history) and
`handoffs/*.md`.
