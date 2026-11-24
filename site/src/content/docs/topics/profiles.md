---
title: Profiles and moving topics
description: Run topics under several Claude Code profiles, and move a topic between them.
---

`topic` honours **`CLAUDE_CONFIG_DIR`**, exactly as `claude` does. Set it and everything follows:
the registry, session records, transcripts, and the profile the launched session itself runs
under.

```bash
CLAUDE_CONFIG_DIR=~/.claude-work topic list
CLAUDE_CONFIG_DIR=~/.claude-work ./install.sh   # install into that profile too
```

Topics are per-profile, and the tool can see across all of them:

```bash
topic profiles                        # every profile, and how many topics it holds
topic list --all                      # every topic in every profile
topic move "<name>" --to work         # move a topic into another profile
topic move "<name>" --to default --dry-run
```

`--to` takes a profile's tag (`work` for `~/.claude-work`), `default`, or a path. `topic list`
names the profile whenever it is not the default, so a short list cannot be mistaken for missing
topics.

## What keeps profiles apart

- **Sessions are launched with the profile pinned** on the command line. A detached tmux session
  does not reliably inherit the environment (the tmux server may long predate it), and a session
  started under the wrong profile writes its transcript where `topic` cannot see it, breaking
  `adopt`, `prune` and resume in ways that look like data loss.
- **tmux session names are namespaced** for non-default profiles (`work/Fantasy Images`), because
  tmux is machine-global. Without that, two profiles running a topic of the same name would fight
  over one session. The default profile keeps bare names.
- **Each profile has its own Dispatcher and agent.** The Dispatcher is `Dispatcher (work)`, the
  launchd agent `io.github.verveguy.claude-dispatcher-work`, logging to
  `~/Library/Logs/claude-dispatcher-work.log`, with `CLAUDE_CONFIG_DIR` baked in. launchd starts
  with a bare environment, so without that every agent would manage the default profile.
- **The default profile runs with `CLAUDE_CONFIG_DIR` unset, never set to `~/.claude`.** They are
  not equivalent: unset reads `~/.claude.json`, set reads `~/.claude/.claude.json`, a different
  file that has never completed onboarding. `topic`, `topic cycle` and `install.sh` all
  special-case this. See [Mechanics](/claude-topics/reference/mechanics).

## Moving a topic

`move` takes the whole topic: registry entry, handoff docs, briefs, and **every transcript it can
still resume**, the current session and every retired one in its `history`. The transcripts are
the part that is easy to forget: `topic up` resumes by UUID and `claude --resume` only looks
inside its own profile's `projects/`, so a registry moved on its own arrives unresumable.

- A topic that is **up** is put down, moved, and brought back up on the far side. Both halves are
  lossless, so the session continues where it left off, now in the new profile. `--stay-down`
  leaves it down.
- It refuses to move the topic you are *running the command from*, since putting that down kills
  the command mid-move. Run it from another session; that is what the Dispatcher is for.
- It refuses to overwrite a topic of the same name in the destination, and to move into a profile
  that has not completed first-run setup.
- `--copy` leaves the original in place (the topic must be down). Resuming both copies afterwards
  forks the transcript.
- The destination records `movedFrom` and `movedAt`, since after a move the old profile has no
  trace of where the topic went.
- It re-mints the topic's Remote Control bridge so it shows under the destination's account; see
  [Rename and Remote Control names](/claude-topics/topics/rename). `--keep-bridge` opts out.
- Stored [claude arguments](/claude-topics/topics/claude-args) travel with it, with a warning if they
  name channel sources the destination must also have configured.

If a live move fails to restart, the move itself has still succeeded: the topic is down,
resumable, and reported as such.

### Per-project approvals

Whether a directory is trusted, and whether its CLAUDE.md may import files from outside it, is
recorded per profile, so a topic can arrive somewhere it is not allowed to start. `move` checks
first: if the source profile approved external imports for that directory and the destination
has not, it stops and tells you how to answer it.

- `--carry-approvals` copies your existing trust and external-import answers for that one
  directory into the destination. It deliberately leaves `allowedTools` behind: copying a
  permission allowlist into a profile that never granted it would be a silent escalation. It
  writes to the destination's `.claude.json`, which running sessions write too, so prefer it when
  that profile is quiet.
- `--force` moves anyway and leaves the topic down.

## Creating a profile

`topic` will not answer a new profile's first-run prompts (theme, login): those are your choices.
Create it by hand first, then install into it:

```bash
CLAUDE_CONFIG_DIR=~/.claude-new claude     # pick a theme, /login, exit
CLAUDE_CONFIG_DIR=~/.claude-new ~/dev/claude-topics/install.sh
```

Run [`topic cycle down`](/claude-topics/running/cycle) before logging a profile in or out, or its
agent restarts the Dispatcher underneath you.
