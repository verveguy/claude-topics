---
name: topic-manage
description: Pick up, put down, list, adopt, rename, and move long-running Claude Code topics, including across Claude Code profiles. Use when the user says "pick up <topic>", "put down <topic>", "set aside", "resume <topic>", "start a session on <topic>", "what topics are up", "adopt this session", "rename this topic", "move it to my other profile", "which profile is it in", asks why a session shows the wrong name or account in the Claude UI, or asks to manage sessions remotely from their phone. For handing a topic to a fresh session to clear context, use the `handoff` skill instead; for splitting one topic into two, use `fork`.
---

# Managing topics

A **topic** is a stable name that outlives the Claude session beneath it. The user
picks topics up and puts them down — often from their phone while travelling —
without accumulating week-old cluttered context.

Backed by the `topic` CLI (a Go binary, installed at `~/.local/bin/topic`). Run
`topic --help` for the full surface; every command below is part of it.

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

**A session that has exited** — one `claude --resume` lists — is adopted the same
way. `topic` falls through to matching the name against the `custom-title` records in
each transcript, which is where `--resume` gets its names. Only a session's *last*
title counts, since renames stamp new ones.

```bash
topic adopt "Fantasy UX"                      # not running, still resumable
topic adopt "<name>" --title "<resume name>"  # topic name differs from the title
```

Such a topic is registered `down`, not `adopted` — there is no process to claim, so
`topic up "<name>"` just resumes it. The scan reads every transcript and takes a few
seconds; that is why it is the fallback.

Use `--probe` only when the target is running but **unnamed** — nothing to match on:

1. `ListAgents`, then `SendMessage` the target a unique token, e.g.
   `ADOPT-PROBE-<random>`, asking it to simply acknowledge and change nothing.
2. Register it, excluding your own transcript (yours also contains the token,
   because you sent it):
   ```bash
   topic adopt "<name>" --probe <token> --exclude <your-own-session-uuid>
   ```

A session adopting *itself* already knows its UUID: `topic adopt "<name>"
--session-id <uuid>`.

`adopt` is non-destructive — it only records the UUID, directory, and pid. Use
`--dry-run` when you are *identifying* a session rather than adopting it: it reports
the match and registers nothing.

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

## Splitting a topic

If a topic has grown two subjects, split it: `topic fork` gives one thread its own
topic, seeded with a brief, while the parent keeps running. The `fork` skill has the
procedure. `topic whoami` names the topic the current session is sitting in.

## Renaming, and Remote Control names

A topic's name lives in five places: the registry, the tmux session, the peer name in
`ListAgents`, the name in the Claude UI, and the transcript's title. `topic rename
"<old>" "<new>"` changes all of them; nothing else does.

- **`/rename` in the Claude UI changes only the cloud name.** If a user reports a
  session whose UI name does not match anything local, that is why — the topic is
  still registered under its old name, and that is the name every `topic` command
  needs.
- A session's UI name and account are pinned to its Remote Control *bridge*, so a
  topic moved between profiles keeps the old account and a hostname-derived name until
  re-minted. `move` and `rename` do that automatically; `topic rebridge "<name>"|--all
  [--down-only]` fixes one already in place.
- **Re-minting restarts the cloud conversation.** The local transcript keeps
  everything and the session resumes with full history, but the Claude UI shows the
  topic from the re-mint onwards and the old entry is orphaned — listed, renameable,
  connected to nothing. Tell the user this before doing it in bulk; an orphan showing
  different content from the live session is exactly how it presents.

## Shutting everything down

`topic down-all` puts every live topic in the current profile down, skipping the
calling session. `topic cycle down` / `topic cycle up` does it across every profile
and handles the launchd agents, which otherwise restart Dispatchers underneath you.
Both are lossless and both take `--dry-run`; show the user the dry run first.

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
- State lives in `<profile>/topics/<slug>/topic.json`.
- **Profiles:** `topic` follows `CLAUDE_CONFIG_DIR` like `claude` does. If the user
  runs more than one profile, topics are per-profile and do not appear in each
  other's `topic list` — that is expected, not missing data. `topic list` names the
  profile when it is not the default. Non-default profiles get their tmux sessions
  prefixed (`work/Fantasy Images`), which is the name to use with `tmux attach`.
  - `topic profiles` — every profile and its topic count; `topic list --all` — every
    topic across all of them. Reach for these before telling a user a topic is
    missing; it is probably in another profile.
  - `topic move "<name>" --to <profile>` moves a topic between profiles, transcripts
    included. It requires the topic to be **down** first. Show the user
    `--dry-run` output before running it for real.
- Do not adopt Fabrik workers or other automation — only the user's own interactive
  sessions. Fabrik worktree sessions live under `.fabrik/worktrees/`.
