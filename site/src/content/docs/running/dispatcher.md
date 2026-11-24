---
title: The Dispatcher
description: The always-up topic that runs topic commands for the others, so you can start work from your phone.
---

The Dispatcher is an always-up topic whose only job is running `topic` commands for the others.
It solves the bootstrapping problem: you can't `topic up` remotely if nothing is up.

From your phone, you talk to the Dispatcher: *"pick up Fantasy Economics"*, *"put down Fantasy
Images"*, *"what topics are up?"*, *"restore my topics"*.

## One per profile

Each profile has its own: `Dispatcher` in the default profile, `Dispatcher (<tag>)` in
`~/.claude-<tag>`. Set `CLAUDE_DISPATCHER_NAME` to name it something else; on Windows,
`install.ps1 -DispatcherName` bakes that into the task, and every machine's Dispatcher should have
its own name, since they all advertise it to Remote Control.

It runs in `<profile>/dispatcher/` unless its registry entry or `CLAUDE_DISPATCHER_DIR` says
otherwise (falling back to a directory under `$TMPDIR`, then `$HOME` with a warning).
Deliberately not `$HOME`: an unqualified search from there walks every TCC-protected folder you
own and raises permission dialogs blaming `topic`. Where it runs does not constrain where it
starts topics.

## Kept alive

The profile's agent runs `topic ensure-dispatcher` at login and every five minutes: a launchd
agent on macOS (`RunAtLoad` plus `StartInterval` 300), a Task Scheduler task on Windows.
Supervision goes through that check rather than launchd's `KeepAlive` because the real process
lives inside detached tmux, which launchd cannot watch.

Each `ensure-dispatcher` run, idempotent and safe to run by hand from any session:

1. **Starts the Dispatcher** if it is not up, and says when there are topics to
   [restore](/claude-topics/running/restore).
2. **Fails loudly if the Dispatcher is signed out.** If its pane shows "Not logged in / Run
   /login", the run exits with an error, so the log says why your phone cannot reach it.
3. **Syncs every live topic's session ID**, so a session that ran `/clear` is resumed from after
   it, not before.
4. **Runs the daemon keeper**, restarting this profile's registered
   [daemons](/claude-topics/running/daemons) that are down.

```bash
topic ensure-dispatcher                               # any session can revive it
tail -f ~/Library/Logs/claude-dispatcher-<tag>.log    # claude-dispatcher.log for the default profile
```

On Windows the log is `%LOCALAPPDATA%\claude-topics\claude-dispatcher[-<tag>].log`.

## Logging in and out

The agent will restart a Dispatcher you put down within five minutes, so put everything down with
[`topic cycle down`](/claude-topics/running/cycle) before logging a profile in or out, and
`topic cycle up` afterwards.

## Without Remote Control

If the account's organization turns Remote Control off, the Dispatcher still runs and still
starts topics; it serves local sessions and SSH rather than your phone.
