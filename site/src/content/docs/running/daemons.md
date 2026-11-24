---
title: Daemons
description: Keep long-running non-Claude processes up in their own tmux sessions, alongside your topics.
---

Long-running processes that are not Claude topics, but live like them in their own detached tmux
session (Fabrik engines, Pruefer reviewers), can be registered as **daemons**. The Dispatcher's
five-minute check then keeps them up too.

```bash
topic daemon add "liminis-daemon" ~/dev/liminis-project daemon-env fabrik --auto-upgrade
topic daemons                        # each daemon and its state
topic daemon stop "liminis-daemon"   # stop on purpose; the keeper leaves it down
topic daemon start "liminis-daemon"  # and back
topic daemon remove "liminis-daemon" # drop it from the registry (its session is untouched)
topic ensure-daemons                 # just the keeper pass, by hand
```

## Registering one

`topic daemon add <name> <dir> <command...>`. The name is its tmux session's name; the directory
must exist.

The command is either **one** argument, a complete shell line stored verbatim
(`topic daemon add x ~/d "sh -c 'fabrik --name \"a b\"'"`), or **several**, an argv that is
shell-quoted word by word so it means exactly what you passed. Adding an existing name replaces
its command and keeps it stopped if it was.

The registry is per profile (`<profile>/daemons.json`), because the agent that runs the check is
per profile. Daemon session names are not namespaced by profile. A daemon's own `.env` pins the
Claude profile its workers use, so register it under any profile whose agent is loaded.

The command runs in a login shell, followed by
`echo "[topic] daemon <name> exited with status $?"`, so an exit is visible in the pane: daemons
have exited without logging why.

## States, and what the keeper does

`topic daemons` shows each daemon as:

| State | Meaning | Keeper |
|---|---|---|
| running | the pane is running something other than its shell | leaves it |
| husk | the pane is back at a shell | types the command in again |
| missing | no tmux session | creates it with the command |
| orphaned, pid N | the pane is gone or a husk, but the process it was running is still alive | stops it, then starts the daemon |
| (stopped on purpose) | `daemon stop` | leaves it down |

A pane counts as a husk only when its shell is in the foreground *and* has no child processes, so
a daemon run by a wrapper script that doesn't `exec` is not mistaken for one. The flip side: a husk
whose shell still has a leftover background job counts as running and is not restarted.

### Orphans

The keeper remembers which process it last saw running each daemon: pid and start time, in
`<profile>/daemon-pids.json`, so a reused pid is never mistaken for the daemon. If that process is
still alive when its pane is gone or back at a shell, it is an **orphan**. It was left running when
its pane went away, for example by `tmux kill-server`, and a fresh copy would die on its lock.

The keeper sends it SIGTERM, waits up to a minute for it to exit, and only then starts the daemon
in its pane. One that will not exit is reported ("Stop it by hand: kill N") and the start is
skipped. A daemon orphaned before the keeper's first look at it (within five minutes of starting)
has no pid on record and is not detected.

Orphan detection is macOS (and Unix) only. On Windows the process cannot be inspected.

## Stopping

`topic daemon stop` marks the daemon stopped and presses Ctrl-C in its pane once; it does not
wait. [`topic cycle down`](/claude-topics/running/cycle) is different: it stops each daemon's
process and waits, without marking it stopped, so `cycle up` brings it back.
