---
title: Windows
description: Running topic on Windows, with MSYS2's tmux and Task Scheduler.
---

From PowerShell, with Go and MSYS2 (`pacman -S tmux`) installed:

```powershell
git clone https://github.com/verveguy/claude-topics.git $HOME\dev\claude-topics
cd $HOME\dev\claude-topics; .\install.ps1 -DispatcherName "Dispatcher (<this machine>)"
```

`install.ps1`:

- Builds `bin\topic.exe` with plain `go build` (Windows has no signing step) and copies it to
  `~\.local\bin\topic.exe`. Re-run it after changing `cmd\`.
- Junctions the plugin into the profile's `skills\topics`. `-Force` replaces a real directory
  there.
- Registers a Task Scheduler task, `\claude-topics\claude-dispatcher` (`claude-dispatcher-<tag>`
  for a non-default profile), that runs `topic ensure-dispatcher` at logon and every five
  minutes. The rendered task definition is kept in `%LOCALAPPDATA%\claude-topics\tasks\`, and the
  log is `%LOCALAPPDATA%\claude-topics\claude-dispatcher[-<tag>].log`.
- Bakes `-DispatcherName` into the task as `CLAUDE_DISPATCHER_NAME`. Give each machine's
  Dispatcher its own name: they all advertise it to Remote Control.
- Refuses install paths containing whitespace or cmd metacharacters.

`.\install.ps1 -Uninstall` removes the task, its definition and the junction, and removes
`topic.exe` when uninstalling the default profile.

## How it differs

- **The Dispatcher comes back once you log in after a reboot, not before.** The task runs as you,
  at logon, so an unattended machine sitting at the login screen starts nothing.
- **Topics run in MSYS2's tmux** with a bash pane. Attach with `tmux attach -t "<name>"` from an
  MSYS2 shell. `TOPIC_TMUX` points `topic` at a tmux other than MSYS2's default.
- **`topic cycle`** disables and enables the tasks (`schtasks /Change /DISABLE|/ENABLE`) where
  macOS unloads and loads launchd agents.
- **Daemons are stopped by Ctrl-C, not by process.** `cycle down` presses Ctrl-C in each running
  daemon's pane and waits; orphan detection is not available.
- **`topic ui --list`** is not available: it finds instances through tmux's socket directory.
- `install.ps1` has no default-profile guard and no `--all`.
