---
title: Extra claude arguments
description: Per-process claude flags, such as MCP channel pushes, stored on a topic and applied to every launch.
---

Some `claude` settings are per-process and only take effect at launch. The motivating one is MCP
channels: for [Fabrik](https://github.com/handarbeit/fabrik) to push updates into a session, the
session must be started with `--dangerously-load-development-channels server:fabrik` (or
`plugin:fabrik@fabrik`) while channels are in research preview. Without it Claude Code silently
drops the pushes and only the tools work. Starting `claude` by hand and adopting it only lasts
until the first `topic down`.

So a topic can carry a list of extra arguments, stored as `claudeArgs` in its `topic.json` and
added to **every** launch: `up` (new or resume), handoff, fork, `move`, `rename` and `restore`.

```bash
topic up "Fabrik work" ~/dev/fabrik -- --dangerously-load-development-channels server:fabrik
topic set-args "Fabrik work" -- <args...>   # replace the list (from the next launch)
topic set-args "Fabrik work" --clear        # remove it
```

- The stored args go before `topic`'s own; `--resume` / `--session-id` and a seed prompt stay
  last. Arguments `topic` owns (`--resume`, `--session-id`, `--name`, `--remote-control`,
  `--continue` and their short forms) are refused.
- `up` on a topic that is already up still stores new `--` arguments; they apply from the next
  launch.
- A fork inherits its parent's args; `--no-inherit-args` turns that off, and `-- <args...>` on
  the fork replaces them.
- `topic status` shows them and `topic list` marks the topic `[+args]`.
- `move` carries them and warns that any channel source they name must also be configured in the
  destination profile.
- A running session is unaffected by `set-args`; `down` then `up` applies the change.
- [Linking a topic to Slack](/claude-topics/slack/setup) adds `server:slack` to its channels.

## The development-channels confirmation

`--dangerously-load-development-channels` makes Claude Code show a confirmation ("I am using this
for local development / Exit") on **every** launch, and no setting pre-answers it. Since storing
the flag is your standing answer, `topic` presses the first option itself, but only when the
topic's stored `claudeArgs` contain that flag **and** the channels listed on screen are exactly
the stored ones. Any other screen is left for you.

`--channels` needs no confirmation, but accepts only plugins on Anthropic's allowlist, which only
an org admin can extend.

A topic with no `claudeArgs` launches with the command line it always had.
