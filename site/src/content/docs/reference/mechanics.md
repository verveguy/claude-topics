---
title: Non-obvious mechanics
description: How Claude Code actually behaves, learned the hard way while building topic.
---

These are facts about **Claude Code** rather than about this tool, so they are worth knowing even
if you never run it, and worth not re-deriving if you do. Read this first when something here
misbehaves.

- **`--remote-control` alone does not name a session.** A session started with only
  `--remote-control "X"` advertises itself to peers as an auto-derived `<dir>-<hash>` and is
  unaddressable by name. You need `--name "X"` as well. `topic` passes both.
- **`SendMessage` resolves against the last `ListAgents` snapshot.** A freshly launched session is
  unreachable until you call `ListAgents` again. Call it first, then send, and expect to need the
  `[ref]` suffix.
- **Remote Control names live in the cloud, not on disk.** When such a session dies, its entry can
  linger in `ListAgents` as a ghost that accepts messages and never answers. Sessions launched by
  `topic` don't have this problem: `--name` writes the name to the live-session record.
- **`<profile>/sessions/<pid>.json`** is the authoritative live-session record: `name`,
  `sessionId`, `cwd`, `tmux`. It is the best starting point for any "which session is this?"
  question, and what `topic adopt` reads. Records are **not** removed when a session exits, so
  always re-check the pid before trusting one.
- **`/clear` starts a new session ID inside the same process.** The live record's `sessionId`
  follows it; nothing else tells you. A registry that only records the ID a session was launched
  with therefore drifts, and resuming the recorded ID brings back the conversation from before the
  `/clear`. `topic` re-reads the live record before `down`, `move`, `rename`, `rebridge` and
  `handoff-swap`, and `ensure-dispatcher` sweeps every live topic every five minutes. The
  pre-`/clear` ID is kept in history.
- **`CLAUDE_CONFIG_DIR=~/.claude` is not the same as leaving it unset.** The *directories* are
  identical (same `projects/`, `sessions/`, `topics/`), but the **config file** is not: unset reads
  `~/.claude.json`, set reads `~/.claude/.claude.json`. Those carry onboarding state, account,
  `userID` and `machineID`, so setting it to the default directory silently selects a *second
  identity over the same data*, typically one that has never completed onboarding. That is why the
  failure is so quiet: transcripts and sessions resolve correctly either way. Treat "default
  profile" as "variable absent", never "variable set to the default".
- **A profile that has not completed first-run setup cannot be automated.** It opens on the theme
  picker (or a login prompt), which `topic` deliberately will not answer: theme and account are your
  choices. It detects that text, kills the session it just started, and says so, rather than
  leaving a session parked at a prompt that would look up. Create a profile by hand first:
  `CLAUDE_CONFIG_DIR=~/.claude-new claude`.
- **An exiting session keeps writing briefly.** `topic down` returns once claude has left the pane,
  but the process can still flush a final `file-history-snapshot` record afterwards, which recreates
  the transcript at its old path after a move. Wait for the file to stop changing before relocating
  it.
- **Remote Control identity is pinned to a bridge, and the bridge lives in the transcript.** Which
  name a session shows in the Claude UI, and which account it appears under, are fixed when its
  bridge session is created and recorded as `{"type":"bridge-session",...}`. Claude Code rejoins
  that bridge on resume, so `--remote-control "<name>"` is ignored for any session that already has
  one. That is what `topic rebridge` fixes, and what `move` and `rename` do for you.
- **`/rename` in the Claude UI is cloud-only.** It changes the Remote Control display name and
  nothing else: the transcript's `custom-title`, the session record, the tmux session and the topic
  registry all keep the old name. Peers then address the session by its new cloud name while every
  local command still uses the old one. `topic rename` changes all five at once.
- **A session's name survives its process, inside its own transcript.** Transcripts carry
  `{"type":"custom-title","customTitle":"..."}` records; that is the name `claude --resume` lists. A
  **rename appends another one**, so the *last* record is the current name. Transcripts also record
  their own `cwd`, which is the right way to recover a session's directory: de-mangling the
  `projects/` directory name is ambiguous for any hyphenated path (`-Users-me-dev-claude-topics`).
- **Session start-up detection is text-matching.** `topic` polls the pane for prompts it knows
  (folder trust, development channels, external imports, onboarding, a resume that finds no
  conversation) and for a readiness banner (`Welcome back`, `remote-control is active`, `Remote
  Control is disabled`). After 20 seconds with no banner it warns and carries on. Claude Code's
  startup text has changed several times; suspect this first if launches misbehave.
- **A tmux server's environment is not your shell's.** A detached session inherits the environment
  of the tmux server, which may long predate the shell that asked for it. That is why `topic`
  passes the profile on the command line of every session it launches, rather than relying on
  `CLAUDE_CONFIG_DIR` being inherited.
- **Killing a tmux pane does not kill what runs in it.** A process that ignores SIGHUP keeps
  running after `tmux kill-server`, headless, holding whatever locks it held. That is why
  `cycle down` stops daemons by process. See [Daemons](/claude-topics/running/daemons#orphans).
