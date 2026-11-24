---
title: Your first topic
description: Pick a topic up, reach it from anywhere, put it down and pick it up again.
---

## Pick it up

```bash
topic up "Fantasy Economics" ~/dev/fantasy
```

This starts a Claude Code session in `~/dev/fantasy`, inside a detached tmux session named
after the topic, and records the topic in the registry. The directory is recorded once and
reused on every later pick-up, so next time `topic up "Fantasy Economics"` is enough.

## Reach it

Every topic has two ways in:

| Path | How |
|---|---|
| tmux | `tmux attach -t "Fantasy Economics"`, locally or `ssh home -t tmux attach -t "Fantasy Economics"` |
| Remote Control | the session appears in claude.ai and the Claude app under the topic's name |

In a non-default profile the tmux name is prefixed with the profile's tag
(`work/Fantasy Economics`); `topic up` prints the exact attach command.

## Put it down, pick it up

```bash
topic down "Fantasy Economics"
topic up   "Fantasy Economics"
```

`down` ends the session but keeps its session ID, so `up` is a genuine `claude --resume`: the
conversation comes back whole. Putting a topic down is not destructive; do it freely.

## From your phone

You cannot run `topic up` remotely if nothing is running. That is what the
[Dispatcher](/claude-topics/running/dispatcher) is for: an always-up topic whose job is running
`topic` commands for you. Open it in the Claude app and say *"pick up Fantasy Economics"* or
*"what topics are up?"*.

## When it gets cluttered

After a week, say `/handoff` in the topic. The session writes a handoff document, retires, and a
fresh session starts under the same name, seeded with it. See
[Hand off](/claude-topics/topics/handoff). If the topic has grown a second subject, split it with
`/fork` instead: [Fork](/claude-topics/topics/fork).

## Ask Claude

The `topics` plugin teaches Claude these procedures, so in any session you can say "pick up
Fantasy Images in ~/dev/fantasy", "put down everything", "hand this off", or "move this topic to
my work profile".
