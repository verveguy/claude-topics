---
title: Install
description: Build topic, install it and its plugin, and start the Dispatcher.
---

## macOS

```bash
git clone https://github.com/verveguy/claude-topics.git ~/dev/claude-topics
cd ~/dev/claude-topics && ./install.sh
topic --help
```

`install.sh` does four things:

1. Builds `bin/topic` through `scripts/build.sh`, which code-signs it (see
   [Code signing](/claude-topics/running/signing)).
2. **Copies** it to `~/.local/bin/topic`, replacing the old copy atomically.
3. Links the `topics` plugin into the profile, at `<profile>/skills/topics`. That one is a
   **symlink back into the repo**, so editing a skill takes effect at once.
4. Installs and loads the profile's launchd agent, which starts the
   [Dispatcher](/claude-topics/running/dispatcher) straight away and checks on it every five
   minutes.

**Re-run it after changing anything under `cmd/`.** The installed binary is a copy, so a
rebuild alone does not reach it.

The binary is copied rather than linked because launchd runs it at login and it becomes the
responsible process for every tmux session. It has to live on the boot volume: if the repo is on
an external drive, a symlink into it puts the Dispatcher behind macOS's Removable Volumes
protection, which can stop launchd starting it and stop every session touching the repo. It is
also the file a Full Disk Access grant names, so it should not move when the repo does.

:::caution[Build with make build or ./install.sh]
Never a bare `go build`. It produces a binary that works and then, days later, buries you in
macOS permission dialogs you cannot get rid of. [Code signing](/claude-topics/running/signing)
explains why.
:::

### More than one profile

`install.sh` installs into the profile `CLAUDE_CONFIG_DIR` names:

```bash
CLAUDE_CONFIG_DIR=~/.claude-work ./install.sh   # add a profile
./install.sh --all                               # later: reinstall every profile that has an agent
```

With `CLAUDE_CONFIG_DIR` unset, `install.sh` targets the default profile (`~/.claude`) only if
that is plainly meant: it already has an agent, or nothing is installed yet. On a machine where
other profiles are installed and the default is not, it refuses rather than add a Dispatcher for
a profile you do not use. Pass `--default` if you do mean it.

`--force` replaces a `~/.local/bin/topic` or plugin directory that `install.sh` did not put
there, keeping the old one as `.bak`.

### Uninstall

```bash
./install.sh --uninstall
```

This unloads and removes the profile's launchd agent and the plugin link. It removes
`~/.local/bin/topic` only when no other profile's agent still uses it. It deliberately leaves
`<profile>/topics/` alone: that is your registry, handoff docs and briefs, not the tool.

## Windows

From PowerShell, with Go and MSYS2 (`pacman -S tmux`) installed:

```powershell
git clone https://github.com/verveguy/claude-topics.git $HOME\dev\claude-topics
cd $HOME\dev\claude-topics; .\install.ps1 -DispatcherName "Dispatcher (<this machine>)"
```

[Windows](/claude-topics/running/windows) has what it installs and how it differs.

## What it assumes

- **macOS or Windows** for the Dispatcher: launchd on macOS, Task Scheduler on Windows.
  Everything else (up, down, fork, handoff, move, rename, adopt) is tmux and Claude Code only,
  so it would work on Linux given a systemd-user equivalent of the agent, which nobody has
  written.
- `tmux`, `claude`, `go`, `git`, and `~/.local/bin` on your `PATH`. On macOS also `codesign`,
  from the Xcode command line tools: the build stops rather than install a binary that macOS
  will nag about forever. `install.sh` also uses `python3` for the default profile's agent.
- A Claude Code profile that has completed first-run setup (theme and login). `topic` will not
  answer those prompts for you: it detects them, stops, and says so. Create a profile by hand
  first: `CLAUDE_CONFIG_DIR=~/.claude-new claude`.
- Optionally `fzf`, for type-to-filter in [`topic ui`](/claude-topics/topics/ui).

Nothing else is machine-specific. The launchd label (`io.github.verveguy.claude-dispatcher`,
with `-<tag>` for a non-default profile) is anchored to the repo's GitHub account, and every
path derives from `$HOME` and `CLAUDE_CONFIG_DIR` at install time. The agent's `PATH` also
includes `/opt/homebrew/bin` and `/usr/local/bin`, where Homebrew puts `tmux`.

## Just the skills

The skills are also published as a plugin marketplace, for another profile or a machine where
the CLI is already installed. See [Skills](/claude-topics/reference/skills).
