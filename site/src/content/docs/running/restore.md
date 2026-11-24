---
title: After a crash
description: Bring back every topic that was up when the machine crashed or rebooted.
---

When the machine crashes or reboots, the Dispatcher comes back at login, but nothing else does.
`topic restore` brings back every topic that was up when it went down:

```bash
topic restore --dry-run    # what would come back
topic restore              # bring them all back, one at a time
topic restore "<name>"     # just these
topic restore --discard    # leave them down, and stop being told about them
topic restore --all-profiles
```

`topic list` says when there is something to restore, and so does the Dispatcher's log the first
time it starts after a reboot. Asked from inside a topic ("restore my topics", to the Dispatcher)
it runs in the background and logs to `<profile>/topics/restore.log`, since each launch waits for
its session to start.

## How it knows

The registry records it as sessions come and go:

- Every launch records the topic as **up**.
- `topic down` records it as **down**.
- A session that ends by itself (`/exit`, or Claude crashing) records itself as **exited**,
  through a command chained after `claude` on the line typed into its pane.
- A crash, a reboot or `tmux kill-server` kills that shell before it can run anything, so those
  topics are left recorded as up with nothing running them: exactly the ones to bring back.

[`topic cycle down`](/claude-topics/running/cycle) also marks the topics it puts down, so
`cycle up` restores them the same way.

Sessions started by a `topic` older than this have no exit hook, so a topic you `/exit`ed from one
of them looks crashed. Check `--dry-run` the first time, and `--discard` what should stay down.
