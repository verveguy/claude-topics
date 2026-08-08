---
name: topic-manage
description: Pick up, put down, list, and adopt long-running Claude Code topics. Use when the user says "pick up <topic>", "put down <topic>", "set aside", "resume <topic>", "start a session on <topic>", "what topics are up", "adopt this session", or asks to manage sessions remotely from their phone. For handing a topic to a fresh session to clear context, use the `handoff` skill instead.
---

# Managing topics

A **topic** is a stable name that outlives the Claude session beneath it. The user
picks topics up and puts them down — often from their phone while travelling —
without accumulating week-old cluttered context.

Backed by `~/.local/bin/topic`. Run `topic --help` for the full surface.

## Core verbs

```bash
topic list                        # what is up, what is down
topic up   "<name>" [dir]         # pick up: resume the session, or start one
topic down "<name>"               # put down: terminate, but stay resumable
topic status "<name>"             # dir, session UUID, generation, history
```

`up` after `down` is **lossless** — it resumes the stored session UUID, so the topic
remembers everything. Put down freely; it is not a destructive act.

Topics are singletons. `topic up` on a live topic is a no-op, not an error.

## Picking up

If the user names a topic that does not exist yet, `topic up "<name>" <dir>` creates
it. Ask for the working directory if it is not obvious — it is recorded once and
reused on every later pick-up.

After starting a topic you may want to message it. Two rules:

1. **Call `ListAgents` first.** `SendMessage` resolves against the last `ListAgents`
   snapshot; a freshly launched session is unreachable until you refresh it.
2. **Expect to need the `[ref]` suffix** — send `"Fantasy Economics [9e4a24]"` when
   the bare name is rejected. The error message tells you the ref.

## The Dispatcher

`Dispatcher` is an always-up topic whose job is running `topic` commands for the
others. It exists to solve bootstrapping: you cannot run `topic up` remotely if
nothing is up. It is kept alive by launchd (at login, plus a check every 5 minutes).

If it is ever down, any session can revive it:
```bash
topic ensure-dispatcher
```

When the user asks you — as the Dispatcher — to act on another topic, just run the
`topic` command and report the resulting `topic list`.

## Adopting an existing session

A session started outside topic management (a plain `claude` invocation) can be
brought under management. It is a **two-step** process with a real hazard.

**Step 1 — register it.** Every live session publishes its own
`~/.claude/sessions/<pid>.json` with the authoritative name → UUID → cwd mapping, so
no probe is needed. Just name the topic:

```bash
topic adopt "<name>"                 # a live session already named <name>
topic adopt "<name>" --pid <pid>     # or point straight at one
topic adopt "<name>" --dir <dir>     # or narrow by working directory
```

If that leaves more than one candidate, the command lists the live sessions with the
`--pid` to use — it never guesses. Prefer this path; it needs no round trip.

Use `--probe` only when the target has **no live record** (it has exited):

1. `ListAgents`, then `SendMessage` the target a unique token, e.g.
   `ADOPT-PROBE-<random>`, asking it to simply acknowledge and change nothing.
2. Register it, excluding your own transcript (yours also contains the token,
   because you sent it):
   ```bash
   topic adopt "<name>" --probe <token> --exclude <your-own-session-uuid>
   ```

A session adopting *itself* already knows its UUID: `topic adopt "<name>"
--session-id <uuid>`.

`adopt` is non-destructive — it only records the UUID, directory, and pid.

**Step 2 — claim it.** The adopted session is still running outside tmux. Starting it
under topic management would resume the same session UUID in a *second* process — two
writers on one transcript, which corrupts it. The user must exit the original first.

```bash
topic up "<name>"           # adopted via a live record — pid on file, checked for you
topic up "<name>" --claim   # adopted via --probe / --session-id — you are asserting it exited
```

Adoption through a live record stores the pid, so `topic up` refuses on its own while
that process is alive and needs no assertion once it is gone. The other paths have no
pid to check: **never pass `--claim` unless the user has confirmed the original
session has exited.**

## Removing topics

- `topic forget "<name>"` — stop tracking a topic you are done with. Refuses while it
  is up; keeps handoff docs unless `--purge`; the session stays resumable.
- `topic prune` — find topics that are *dead*: down, with no transcript left, so they
  could never be resumed. Dry run by default; `topic prune --yes` removes them
  (add `--purge` to drop their handoff docs too). Always show the user the dry run
  before running `--yes`.

## Notes

- Sessions are launched with both `--remote-control "<name>"` and `--name "<name>"`.
  Both are required: `--remote-control` alone leaves the session advertising an
  auto-derived `<dir>-<hash>` name that cannot be addressed by topic name.
- State lives in `~/.claude/topics/<slug>/topic.json`.
- Do not adopt Fabrik workers or other automation — only the user's own interactive
  sessions. Fabrik worktree sessions live under `.fabrik/worktrees/`.
