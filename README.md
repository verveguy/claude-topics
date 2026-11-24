# claude-topics

Pick up, put down, and hand off long-running Claude Code work — from anywhere,
including your phone.

**Documentation: [v3rv.com/claude-topics](https://v3rv.com/claude-topics)**

One Go binary plus a Claude Code plugin:

| Piece | What it is |
|-------|------------|
| `topic` | Manage **topics**: stable names that outlive the session beneath them |
| `topics` plugin | Skills (`handoff`, `fork`, `topic-manage`) so Claude knows the procedures |

## The problem this solves

You leave Claude Code sessions running for a week so you can continue the dialogue
from your phone while travelling. Two things go wrong:

1. **Context rots.** A week-old session is cluttered with detail that no longer
   matters, but you don't want to lose the thread.
2. **You can't start new work remotely.** If you need a session that isn't already
   running, you're stuck until you're back at the machine.

`topic` fixes both by making the unit of work a *topic* rather than a session: the
name stays stable while the session underneath is resumed, retired, or replaced.

```bash
topic up     "<name>" [dir]   # pick up: resume the topic's session, or start one
topic down   "<name>"         # put down: terminate, but stay resumable
topic list                    # what's up, what's down
topic status "<name>"         # dir, session UUID, generation, lineage
ssh home -t tmux attach -t "<name>"   # attach to any topic over SSH
```

Each topic runs in a detached tmux session with Remote Control, so it is reachable
from a terminal, over SSH, from claude.ai and from the Claude app. Putting one down is
lossless: `up` resumes the same session.

On top of that:

- **[Hand off](https://v3rv.com/claude-topics/topics/handoff)**: `/handoff` retires a
  cluttered session and starts a fresh one under the same name, seeded with a handoff doc.
- **[Fork](https://v3rv.com/claude-topics/topics/fork)**: `/fork` splits a thread out
  into a topic of its own.
- **[The Dispatcher](https://v3rv.com/claude-topics/running/dispatcher)**: an always-up
  topic per profile that runs `topic` commands for you, so you can start work from your
  phone.
- **[Profiles](https://v3rv.com/claude-topics/topics/profiles)**: topics follow
  `CLAUDE_CONFIG_DIR`, and `topic move` carries one between profiles.
- **[Daemons](https://v3rv.com/claude-topics/running/daemons)**,
  **[cycling](https://v3rv.com/claude-topics/running/cycle)** and
  **[restore](https://v3rv.com/claude-topics/running/restore)**: keep non-Claude processes
  up, take everything down and back cleanly, and bring back what a crash took down.
- **[Slack](https://v3rv.com/claude-topics/slack/setup)**: link a channel to a topic and
  talk to it there, alone or with your team.
- **[`topic ui`](https://v3rv.com/claude-topics/topics/ui)**: browse topics side by side
  with their sessions.

## Getting it

```bash
git clone https://github.com/verveguy/claude-topics.git ~/dev/claude-topics
cd ~/dev/claude-topics && ./install.sh
topic --help
```

`install.sh` builds a code-signed `bin/topic`, copies it to `~/.local/bin/topic`, links
the plugin into the profile, and installs the launchd agent that keeps the Dispatcher up.
Re-run it after changing anything under `cmd/` (`./install.sh --all` for every profile).
**Build with `make build` or `./install.sh`, never a bare `go build`**:
[here's why](https://v3rv.com/claude-topics/running/signing).

Windows, other profiles, and what it assumes:
[Install](https://v3rv.com/claude-topics/start/install).

## Documentation

The docs live in [`site/`](site/) and are published to
[v3rv.com/claude-topics](https://v3rv.com/claude-topics). Start with
[Non-obvious mechanics](https://v3rv.com/claude-topics/reference/mechanics) when
something misbehaves, and [Development](https://v3rv.com/claude-topics/reference/development)
to work on it.

## License

MIT — see [LICENSE](LICENSE).
