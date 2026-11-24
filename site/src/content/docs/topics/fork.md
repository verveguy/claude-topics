---
title: Fork
description: Split a thread out of a topic into a topic of its own.
---

A topic that has grown two subjects should be two topics. `fork` takes one thread out of the
topic you are in and gives it its own name, session and context, while the parent keeps running.

Say `/fork "<Topic Name>" <what to extract>` and the `fork` skill drives it. The session writes
the brief itself, since only it holds the context. Underneath:

```bash
# from inside the session being split
p=$(topic fork-path "Fantasy Images")   # where the brief goes
# ...write the brief to $p...
topic fork "Fantasy Images" --from "Fantasy Economics"
```

`--brief <file>` names a brief somewhere else instead of the newest one in `briefs/`.

## What fork adds to `--seed-from`

- **Lineage is recorded both ways.** The child gets `forkedFrom` and `seededFrom`; the parent
  gains the child in `forks`. `topic status` shows both.
- **The child inherits the parent's working directory** unless you pass one. Without that it
  would land in `$PWD`, which, when you fork remotely through the Dispatcher, is the
  Dispatcher's directory rather than the work.
- **`--from` defaults to the calling topic**, via `topic whoami`. Run outside any topic with no
  `--from`, a fork records no parent and starts in `$PWD`.
- **The child inherits the parent's [claude arguments](/claude-topics/topics/claude-args).**
  `--no-inherit-args` turns that off; `-- <args...>` replaces them.
- **A fork refuses a name that already has a session.** It only ever creates a new topic.

## Briefs

Briefs live permanently in `<profile>/topics/<slug>/briefs/`, separate from `handoffs/`, because
they are a different genre. A handoff doc looks back over a topic's own history. A brief looks
forward and is the child's origin document, so it has to carry everything: the child has no
shared history to fall back on.

## The parent keeps the thread

Forking does **not** remove the thread from the parent's context; it only stops the parent being
the only place it lives. [Hand the parent off](/claude-topics/topics/handoff) afterwards if it
should start clean. `topic fork` prints that command rather than running it, since it would
terminate the session you are sitting in.
