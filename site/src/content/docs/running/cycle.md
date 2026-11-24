---
title: Cycling everything
description: Take every profile's topics and daemons down cleanly, and bring them back.
---

To log a profile in or out, rotate credentials, upgrade Claude Code or restart the tmux server,
you want everything down and then back. Two commands, because the point is what you do in
between:

```bash
topic cycle down        # disable agents, put every topic down, stop every daemon
# ...log each profile in from a plain session in ~, check /status...
topic cycle up          # re-enable agents, start each Dispatcher, restore the topics
topic cycle profiles    # what it acts on
```

Both take `--dry-run`.

## Which profiles

"Every profile" means every profile **with an installed agent**: the launchd plists in
`~/Library/LaunchAgents` (or the task files on Windows) *are* the list, since each names the
profile it manages, so there is no second list to keep in sync. `topic cycle profiles` shows it. A
profile with topics or daemons but no agent is not touched.

## cycle down

1. **Disable the agents first.** They run `ensure-dispatcher` every five minutes, so a Dispatcher
   put down without unloading its agent comes back underneath you, typically in the middle of
   logging in.
2. **Put every topic down, in every profile**, and mark each to be brought back. Putting topics
   down is lossless. The session you run it from is skipped, since putting it down would kill the
   command mid-run; it is named so you can take it down last by hand.
3. **Stop the daemons by process, and wait.** Each running daemon's own process gets SIGTERM, and
   `cycle down` waits up to a minute for all of them, in parallel, naming any that did not exit and
   ending with an error if any are left. Daemons are not marked stopped; `cycle up` starts them
   again. (On Windows, where the process cannot be inspected, it presses Ctrl-C in the pane and
   waits instead.)

Killing the pane is not enough to stop a daemon: on 2026-11-22 a `tmux kill-server` left twelve
daemons running headless, still holding their locks, so every fresh copy died on start. Step 3 is
why `topic cycle down && tmux kill-server && topic cycle up` is now safe for registered daemons. A
daemon that was *already* orphaned before `cycle down` is caught by the keeper at `cycle up`
instead (see [orphans](/claude-topics/running/daemons#orphans)). Anything else you started in tmux
by hand is yours to stop.

## cycle up

1. Re-enable each agent.
2. Run `ensure-dispatcher` in each profile, which starts the Dispatcher and the daemons.
3. Run [`topic restore`](/claude-topics/running/restore) in each profile: the topics `cycle down` put
   down, plus any others recorded as up but not running.

## Restarting the tmux server

From a terminal **outside tmux**:

```bash
topic cycle down && tmux kill-server && topic cycle up
```
