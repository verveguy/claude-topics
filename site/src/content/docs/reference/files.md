---
title: Files
description: Where topic keeps its state.
---

Everything lives in the profile (`~/.claude` for the default, `~/.claude-<tag>` otherwise), never
in the repo. Uninstalling leaves all of it in place.

## Per topic: `<profile>/topics/<slug>/`

The slug is the name lowercased, with runs of other characters turned into `-`.

| Path | Holds |
|---|---|
| `topic.json` | the registry entry, below |
| `topic.json.lock` | serialises writes to it |
| `handoffs/*.md` | [handoff docs](/claude-topics/topics/handoff), kept permanently |
| `briefs/*.md` | [fork briefs](/claude-topics/topics/fork), kept permanently |
| `backups/` | transcript copies made before a bridge re-mint edits one |
| `swap.log` | a detached `handoff-swap`'s output |

`topic.json` fields include `name`, `dir`, `sessionId`, `generation`, `state` (`up`, `down`,
`exited`, `adopted`), `history` (retired session UUIDs), `claudeArgs`, `forkedFrom`, `forks`,
`seededFrom`, `movedFrom` / `movedAt`, `renamedFrom`, `adoptedAt` / `adoptedPid`,
`lastPickedUp`, and the mark `cycle down` leaves for `cycle up`.

## Per profile

| Path | Holds |
|---|---|
| `topics/.locks/<slug>.lock` | the singleton lock taken while a topic starts; a lock older than five minutes is reclaimed |
| `topics/restore.log` | a background `restore`'s output |
| `topics/ui-sessions` | non-topic tmux sessions `topic ui` lists, one pattern per line |
| `daemons.json` | registered [daemons](/claude-topics/running/daemons) |
| `daemon-pids.json` | the process last seen running each daemon, for orphan detection |
| `dispatcher/` | the Dispatcher's working directory |
| `channels/slack/` | the [Slack home](/claude-topics/slack/how-it-works#files), in the profile that owns the bridge |
| `skills/topics` | link to the repo's `plugin/` |

Claude Code's own files that `topic` reads: `sessions/<pid>.json` (live-session records),
`projects/<dir>/<uuid>.jsonl` (transcripts), and the profile's `.claude.json` (`~/.claude.json` for
the default profile).

## Per machine

| Path | Holds |
|---|---|
| `~/.local/bin/topic` | the installed, signed copy of the binary |
| `~/Library/LaunchAgents/io.github.verveguy.claude-dispatcher[-<tag>].plist` | each profile's agent |
| `~/Library/Logs/claude-dispatcher[-<tag>].log` | each agent's log |
| `%LOCALAPPDATA%\claude-topics\` | Windows: task definitions and logs |
