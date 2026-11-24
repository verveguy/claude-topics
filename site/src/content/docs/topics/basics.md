---
title: The basics
description: up, down, list, status, forget, prune, and what a topic is made of.
---

```bash
topic up     "<name>" [dir]   # pick up: resume the topic's session, or start one
topic down   "<name>"         # put down: terminate, but stay resumable
topic list                    # this profile's topics: UP, ADOPTED or down (alias: ls)
topic list --all              # every topic in every profile
topic status "<name>"         # dir, session UUID, generation, lineage, history
topic whoami                  # which topic is this session in?
topic down-all                # put every live topic in this profile down
topic forget "<name>"         # stop tracking one (keeps its handoff docs)
topic prune                   # which topics are dead? --yes removes them
```

## Up and down

Each topic is backed by a stable session UUID, so `up` after `down` is a genuine `--resume`:
**lossless**. `up` on a topic that has never run starts a new session with that UUID.

Topics are **singletons**: at most one live session per topic, enforced by an exact-match tmux
check plus a lock file. `topic up` on a live topic is a no-op, not an error.

`down` sends the session `/exit`, gives it a few seconds to leave, then kills its tmux session.
Before it does, it re-reads the session's live record, so a session that ran `/clear` (which
starts a new session ID inside the same process) is recorded under its current ID rather than
resumed from before the `/clear`.

`down-all` puts every live topic in the profile down, skipping the session you run it from unless
you pass `--include-self`. `--dry-run` shows what it would do.

## Seeding a new topic

```bash
topic up "<name>" [dir] --seed-from <file>
```

`--seed-from` starts a brand-new topic with a document as its first prompt: a handoff doc, an
`ideas/` note, an issue write-up. It is ignored, with a warning, if the topic already has a
session to resume. To split a thread out of the topic you are *in*, use
[fork](/claude-topics/topics/fork), which wraps this and records the lineage.

## Two access paths

Every session is launched with **both** `--remote-control "<name>"` and `--name "<name>"`:

| Path | Reach |
|---|---|
| tmux | local attach, SSH attach, survives disconnect |
| Remote Control | claude.ai and the Claude app |

Other Claude sessions on the machine reach a topic by name through `ListAgents` and
`SendMessage` either way: that comes from `--name`, not Remote Control.

Every launch also exports **`CLAUDE_TOPIC=<name>`**, so the session, the MCP servers it starts
and its child processes know which topic they serve. A topic name is stable across sessions and
restarts in a way a session ID or PID is not, which is what a channel subscriber such as Fabrik
needs to key on.

**Without Remote Control.** Some organizations turn Remote Control off. Claude Code then prints
"Remote Control is disabled by your organization's policy" and starts the session anyway, with
no bridge. `topic` keeps passing `--remote-control`, so a topic picks it up if the policy ever
changes, and treats that notice as the session being ready. Everything else works unchanged,
minus the phone and claude.ai path.

## Status and list

`topic list` shows each topic as **UP**, **ADOPTED** (running outside tmux; see
[Adopting](/claude-topics/topics/adopt)) or **down**, marks those with extra claude arguments
`[+args]`, names the profile when it is not the default, and ends with a line when there are
topics to [restore](/claude-topics/running/restore). `--all` must come first and covers every
profile.

`topic status "<name>"` prints the registry entry: directory, current session UUID,
generation, claude arguments, lineage (`forkedFrom`, `forks`, `movedFrom`) and how many retired
sessions it has. If the live session is no longer the recorded one (after a `/clear`), it says
so.

## Getting rid of topics

`forget` is the disposal path for one topic you have decided you are done with. It refuses while
the topic is up, keeps the handoff documents unless you pass `--purge` (they are often the only
surviving record of what a topic was about), and never touches the session itself: it stays
resumable with `claude --resume <uuid>`.

`prune` is the disposal path for topics that are dead whether you decided so or not: down, with
no session ID or no transcript left in the profile's `projects/`, so `up` could never resume
them. It is a **dry run by default**. Live and merely put-down topics are never touched.

```bash
topic prune                   # what is dead?
topic prune --yes             # remove them (-y), keep their handoff docs
topic prune --yes --purge     # remove them and their handoff docs
```

## Learning a command

`topic help <command>`, or `topic <command> --help`, prints that command's help and never acts.
[Commands](/claude-topics/reference/commands) lists every one.
