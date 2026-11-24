---
title: topic ui
description: Browse topics side by side with their sessions.
---

```bash
topic ui
```

A topic list on the left, grouped by working directory, and the selected topic's tmux session on
the right.

- **Enter** on a **down** topic picks it up first, in the background, so the list stays usable
  while it starts. Pick several in a row and the right pane lands on the last one you chose.
- An **adopted** topic is refused until you exit the original session.
- **Enter** (or a double-click) on a directory heading folds or unfolds its group. Folds are kept
  on the instance's tmux server, so they survive a reload of the picker.
- **Ctrl-r** refreshes the list. The filter prompt sits at the bottom of the list pane. The mouse
  works.
- Status is a coloured arrow beside each name: green ▲ up, red ▼ down, yellow ◆ adopted (running
  outside tmux). `NO_COLOR` turns the colours off.

`fzf` is optional. With it you get type-to-filter and a list that updates in place, without the
left pane blanking and redrawing. Without it the left pane is a numbered menu
(`brew install fzf`).

## Instances and keys

Every `topic ui` is its own instance on its own tmux server (`-L topics-ui-<id>`) with prefix
`C-a`, so two terminal windows give two independent UIs, each with its own folds. Inside the right
pane `C-b` still drives the topic's own tmux.

| Key | Does |
|---|---|
| `C-a h` / `C-a l` | move between the panes |
| `C-a d` | detach; the instance keeps running |
| `C-a q`, or `Ctrl-q` in the list (`q` in the numbered menu) | close this instance, with no prompt |

Closing a UI closes only the UI: the topics keep running, and its leftover socket is swept on the
next `--list` or launch.

```bash
topic ui --list            # running instances, and whether a window is attached
topic ui --attach [ID]     # reattach (ID may be left out when only one runs)
topic ui --quit [ID]       # close one
topic ui --quit --all      # close every instance
topic ui --no-attach       # build the layout without attaching (what the tests use)
```

If an instance's picker had exited, `--attach` restarts it rather than leaving "Pane is dead".
`TOPIC_UI_SOCKET=<name>` pins one fixed instance instead of making a new one per launch. Listing
finds instances through tmux's socket directory, so it is not available on Windows.

## Other sessions

Other tmux sessions, such as a [daemon's](/claude-topics/running/daemons), can be listed too:

```bash
topic ui --add-session "fabrik*"      # a session name or a glob; quote it
topic ui --remove-session "fabrik*"
```

Patterns are kept in `<profile>/topics/ui-sessions`, one per line. Matching sessions that are
running show last, under `other sessions`, with a green ●; topics' own sessions are never
repeated there.

## Rebuilding

The picker re-executes itself when the `topic` binary is rebuilt, so a new build shows up without
closing the UI. Layout and key bindings are set when an instance is created: those need it closed
and a fresh `topic ui`.
