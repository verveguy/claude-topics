---
title: Commands
description: Every topic command and flag, and the environment variables it reads.
---

`topic help <command>`, or `<command> --help`, prints a command's help and never acts. Names with
spaces need quotes.

## Topics

| Command | Flags | Does |
|---|---|---|
| `up "<name>" [dir]` | `--seed-from <file>`, `--claim`, `-- <claude args>` | resume the topic's session, or start one. [Basics](/claude-topics/topics/basics) |
| `down "<name>"` | | put it down, resumable |
| `down-all` | `--include-self`, `--dry-run`, `--remember` | put every live topic in this profile down (`--remember` marks them for `cycle up`) |
| `list` (`ls`) | `--all` (first) | this profile's topics, or every profile's |
| `status "<name>"` | | one topic's registry entry and lineage |
| `whoami` | | the topic this session is in (from its tmux session name) |
| `set-args "<name>"` | `-- <args>`, `--clear` | the [extra claude arguments](/claude-topics/topics/claude-args) every launch adds |
| `forget "<name>"` | `--purge` | stop tracking it; `--purge` removes its docs too |
| `prune` | `--yes` / `-y`, `--purge` | find dead topics; `--yes` removes them |
| `ui` | `--list`, `--attach [ID]`, `--quit [ID]`, `--quit --all`, `--add-session P`, `--remove-session P`, `--no-attach` | [browse topics](/claude-topics/topics/ui) |

## Hand off, fork, adopt

| Command | Flags | Does |
|---|---|---|
| `handoff-path "<name>"` | | where to write the handoff doc |
| `handoff-swap "<name>" [doc]` | `--focus "<text>"` | retire the session, start a fresh one seeded with the doc. [Hand off](/claude-topics/topics/handoff) |
| `fork-path "<new>"` | | where to write the fork brief |
| `fork "<new>" [dir]` | `--from "<parent>"`, `--brief <file>`, `--no-inherit-args`, `-- <claude args>` | split a thread into its own topic. [Fork](/claude-topics/topics/fork) |
| `adopt "<name>"` | `--pid`, `--dir`, `--title`, `--session-id <uuid>`, `--probe <token>`, `--exclude <uuid>`, `--dry-run` | bring a session `topic` did not launch under management. [Adopting](/claude-topics/topics/adopt) |

## Profiles and names

| Command | Flags | Does |
|---|---|---|
| `profiles` | | every profile, and its topic count |
| `move "<name>" --to <profile>` | `--dry-run`, `--stay-down`, `--copy`, `--carry-approvals`, `--force`, `--keep-bridge` | move a topic into another profile. [Profiles](/claude-topics/topics/profiles) |
| `rename "<old>" "<new>"` | `--dry-run` | rename it everywhere. [Rename](/claude-topics/topics/rename) |
| `rebridge "<name>"` / `--all` | `--down-only`, `--dry-run` | re-mint its Remote Control identity |

## Keeping it running

| Command | Flags | Does |
|---|---|---|
| `ensure-dispatcher` | | start the Dispatcher if it is down, sync live sessions, run the daemon keeper. [Dispatcher](/claude-topics/running/dispatcher) |
| `restore ["<name>"…]` | `--dry-run`, `--discard`, `--all-profiles` | bring back what a crash took down. [After a crash](/claude-topics/running/restore) |
| `cycle down` / `up` / `profiles` | `--dry-run` | everything down and back, every profile with an agent. [Cycling](/claude-topics/running/cycle) |
| `daemons` | | this profile's daemons and their state. [Daemons](/claude-topics/running/daemons) |
| `daemon add "<name>" <dir> <command…>` | | register one |
| `daemon stop` / `start` / `remove "<name>"` | | stop on purpose, start again, or drop it |
| `ensure-daemons` | | just the keeper pass |
| `doctor` | | whether this binary can hold a macOS permission decision. [Code signing](/claude-topics/running/signing) |

## Slack

| Command | Does |
|---|---|
| `slack setup` | create the Slack home, manifest and hub daemon in this profile |
| `slack link "<topic>" <channel ID or link>` | route a channel to a topic in this profile |
| `slack unlink "<topic>"` | stop routing to it |
| `slack status` | the hub, each link, and who is listening |
| `slack hub [--home <dir>]` | run the hub (normally as the `Slack Hub` daemon) |
| `slack shim` | the per-session channel server, run by `claude` |

The commands typed in Slack are in [In Slack](/claude-topics/slack/in-slack).

## Internal

These are run by `topic` itself, or are debugging helpers that read and write the same state the
commands above do. They are not part of the supported surface.

| Command | Used by |
|---|---|
| `session-ended` | the exit hook chained after `claude` in each pane: records a session that ended by itself |
| `get`, `set`, `sessions`, `find-titled`, `transcript-title`, `transcript-cwd` | debugging |

## Environment

| Variable | Effect |
|---|---|
| `CLAUDE_CONFIG_DIR` | the profile; unset means the default (`~/.claude`). Never set it to `~/.claude` |
| `CLAUDE_TOPICS_ROOT` | the registry, instead of `<profile>/topics` (the tests use it) |
| `CLAUDE_DISPATCHER_NAME` | the Dispatcher's topic name, instead of `Dispatcher` / `Dispatcher (<tag>)` |
| `CLAUDE_DISPATCHER_DIR` | the Dispatcher's working directory, instead of `<profile>/dispatcher` |
| `CLAUDE_TOPIC` | **set by** `topic` in every session it launches: the topic's name |
| `TOPIC_UI_SOCKET` | pin `topic ui` to one fixed instance |
| `NO_COLOR` | no colours in `topic ui` |
| `TOPIC_TMUX` | Windows: the tmux to run |
| `TOPIC_SLACK_HOME` | the Slack home, instead of the owning profile's `channels/slack` |
| `TOPIC_SIGN_IDENTITY`, `TOPIC_IDENTIFIER`, `TOPIC_OUT` | build: codesign identity, signing identifier, output path |
| `CLAUDE_TOPICS_REPO` | `topic-bootstrap`: where to clone the repo |
