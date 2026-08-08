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

topic up "<name>" [dir] --seed-from <file>    # start a NEW topic from a brief
```

`--seed-from` seeds a brand-new topic with a document — a handoff doc, an `ideas/`
note, an issue write-up. It is what makes a **split** possible: hand one thread off
to the existing topic, and start a second topic from its own brief, each in its own
working directory. Ignored (with a warning) if the topic already has a session to
resume.

`forget` is the disposal path. It refuses while the topic is up, keeps the handoff
documents unless you pass `--purge` (they are often the only surviving record of what
a topic was about), and never touches the session itself — it stays resumable with
`claude --resume <uuid>`.

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

A session started outside `topic` can be brought under management — but it's a
**two-step with a real hazard**.

```bash
# 1. identify it (running sessions record their name + UUID on disk)
topic adopt "<name>" --session-id <uuid>
#    or, if the UUID is unknown: send it a unique token, then
topic adopt "<name>" --probe <token> --exclude <your-own-session-uuid>

# 2. the original must EXIT first, then:
topic up "<name>" --claim
```

`adopt` is non-destructive — it only records the UUID and directory.

**Why `--claim` exists:** the adopted session is still running outside tmux.
Starting it under `topic` would resume the same session UUID in a *second* process —
two writers on one transcript, which corrupts it. Liveness can't be detected
reliably (the transcript isn't held open, and an idle session doesn't write), so
`topic up` refuses until you assert the original is gone.

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
  this?" question.
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
