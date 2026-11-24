---
title: The topics plugin
description: The skills that teach Claude the procedures, and installing them on their own.
---

The `topics` plugin carries three skills, loaded on demand:

| Skill | Use it by saying |
|---|---|
| `handoff` | "hand off", `/handoff <focus>`, "clear context but keep the thread" |
| `fork` | "fork", `/fork <name> <what>`, "split this out", "make that its own topic" |
| `topic-manage` | "pick up …", "put down …", "what topics are up", "adopt this session", "rename this topic", "move it to my other profile" |

`install.sh` links the repo's `plugin/` into the profile as `<profile>/skills/topics`, so editing a
skill takes effect immediately.

## Just the skills

The skills are also published as a plugin marketplace in the repo, so Claude can be taught the
procedures in another profile, or on a machine where the CLI already exists:

```bash
claude plugin marketplace add verveguy/claude-topics
claude plugin install topics@claude-topics
```

They drive the `topic` CLI, so they are only useful alongside it. The plugin cannot ship the binary
(it is Go, so platform-specific, and committing a build would put megabytes into every clone) and
Claude Code has no postinstall hook. What it does ship is `topic-bootstrap`, which lands on the Bash
tool's `PATH` while the plugin is enabled:

```bash
topic-bootstrap             # what is installed, and what is missing (same as --check)
topic-bootstrap --install   # clone and build it, as in Install
```

The skills point at it when a `topic` command comes back "command not found". It is deliberately not
automatic: installing a binary and a launchd agent is your decision, not a side effect of loading a
skill.
