---
title: Rename and Remote Control names
description: Rename a topic everywhere, and fix a session that shows the wrong name or account in the Claude UI.
---

## Five names, one topic

A *session* carries its name in five places, and nothing keeps them in step:

| Where | Set by |
|---|---|
| the tmux session | `tmux new-session -s` |
| the peer name in `ListAgents` | `--name` |
| the name in the Claude UI | `--remote-control`, pinned to a bridge |
| the transcript's `custom-title` | the session itself, appended on each rename |
| the topic registry | `topic` |

They drift apart the moment anything renames one of them in isolation. `/rename` in the Claude
UI, for instance, changes only the cloud name, leaving every local command still using the old
one.

This is the problem a **topic** exists to solve. The topic name is authoritative; the five above
are just how a session happens to present itself, and keeping them consistent is the tool's job:

```bash
topic rename "<old>" "<new>"            # --dry-run shows the plan
```

That updates all five: the registry directory and `name`, the tmux session, the peer name, the
Claude UI name (via a bridge re-mint, below) and the transcript's `custom-title`, so
[adopt](/claude-topics/topics/adopt) can still find it later. `forkedFrom` and `forks` references in
other topics are repointed too, so lineage survives. A live topic is put down and brought back up
under the new name.

## Remote Control bridges

A session's Remote Control identity, meaning the name shown in the Claude UI *and the account it
appears under*, is pinned to a bridge recorded inside its transcript. Claude Code rejoins that
bridge on resume, so `--remote-control "<name>"` is silently ignored for an existing session.

That is why a topic [moved between profiles](/claude-topics/topics/profiles) would otherwise keep
advertising its old profile's account under a hostname-derived name
(`bretts-mac-studio-lan-…`). `move` and `rename` drop those bridge records so the next launch
mints a fresh bridge in the current profile's account, named after the topic. `move
--keep-bridge` opts out. For a topic that is already in the right place:

```bash
topic rebridge "<name>"          # or --all; --down-only skips live topics; --dry-run
```

`rebridge --all` skips the session you run it from.

**The cost of a re-mint:** the *cloud* conversation restarts. The local transcript keeps
everything and the session resumes with its full history, but the Claude UI shows the topic from
the re-mint onwards. Use `--keep-bridge` when continuous cloud history matters more than a
correct name and account. The transcript is copied to `<profile>/topics/<slug>/backups/` before
it is edited, since editing one is unsupported.

**The old cloud session is archived, if the topic is up.** Before `move`, `rename` or `rebridge`
drop a bridge, they disconnect it from inside the running session (`/remote-control` →
*Disconnect this session*). Claude Code then archives the cloud session itself, with its own
credentials, while it is still running as the source account. A topic that is **down** has no
process to disconnect: its old session stays listed in the Claude UI, and `topic` prints its URL
so you can archive it by hand. So prefer bringing a topic up before re-minting it.
