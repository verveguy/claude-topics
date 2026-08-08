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
brought under management, but it is a **two-step** process with a real hazard.

To adopt, we need the session's UUID. A running session does not hold its transcript
open, so we identify it by probe:

1. `ListAgents`, then `SendMessage` the target a unique token, e.g.
   `ADOPT-PROBE-<random>`, asking it to simply acknowledge and change nothing.
2. Register it, excluding your own transcript (yours also contains the token,
   because you sent it):
   ```bash
   topic adopt "<name>" --probe <token> --exclude <your-own-session-uuid>
   ```
   If you do not know your own session UUID, run without `--exclude`; the command
   lists the ambiguous matches rather than guessing.

`adopt` is non-destructive — it only records the UUID and directory.

**The hazard.** The adopted session is still running outside tmux. Starting it under
topic management would resume the same session UUID in a *second* process — two
writers on one transcript, which corrupts it. So `topic up` refuses on an adopted
topic until you assert the original is gone:

```bash
# 1. user exits the original session (/exit in its window)
# 2. then, and only then:
topic up "<name>" --claim
```

Never pass `--claim` unless the user has confirmed the original session has exited.

## Notes

- Sessions are launched with both `--remote-control "<name>"` and `--name "<name>"`.
  Both are required: `--remote-control` alone leaves the session advertising an
  auto-derived `<dir>-<hash>` name that cannot be addressed by topic name.
- State lives in `~/.claude/topics/<slug>/topic.json`.
- Do not adopt Fabrik workers or other automation — only the user's own interactive
  sessions. Fabrik worktree sessions live under `.fabrik/worktrees/`.
