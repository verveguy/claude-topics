---
title: In Slack
description: The /topics commands, held messages, receipts, permission prompts and DMs.
---

## /topics

| Command | Does |
|---|---|
| `/topics new <name> [dir] [--profile p]` | create a topic, link this channel to it, and start it |
| `/topics link <name> [--profile p]` | link this channel to an existing topic, found in whichever profile has it |
| `/topics unlink` | stop routing this channel |
| `/topics up` · `down` · `restart` | for this channel's topic |
| `/topics stop` | interrupt what the topic is doing (Escape in its session) |
| `/topics status` | the topic, whether it is running and listening, the mode, and who is trusted |
| `/topics mode all\|mention\|team\|listen` | how the channel addresses the topic: see [Modes](/claude-topics/slack/modes) |
| `/topics trust @someone…` · `untrust @someone…` | who else the topic takes direction from, in this channel only: see [Trust](/claude-topics/slack/trust) |
| `/topics help` | this list |

- Only allowed users (`SLACK_ALLOWED_USERS`) can run `/topics`.
- Names need no quotes: `/topics new Fantasy Economics`. A word starting with `/`, `~` or `./` is
  the directory.
- `dir` defaults to `~/dev/.scratch/slack-topics/<slug>`, where the slug is the name lowercased
  with runs of other characters turned into `-` (`fantasy-economics`).
- `--profile` (or `-p`) takes what `topic move --to` takes: a tag (`work`), `default`, or a path.
  It defaults to the hub's own profile.

A slash command never appears in the channel, so it never reaches Claude: control and
conversation stay apart. (Slack's built-in `/topic` sets a channel's description, hence the
plural.)

## A topic that isn't listening

When a message addressed to a topic arrives while the topic is down, or running without the Slack
channel loaded, it is **held**, up to 20 per channel. The channel is told which (at most every ten
minutes):

- **Down:** reply `up` and the topic starts and gets the held messages. Only someone the topic
  takes direction from can start it this way; anyone else is told it will get the message when it
  is next started.
- **Running without the channel:** it needs a relaunch (`/topics restart`).

Held messages are delivered once the session can take them, marked `held="true"`. Messages that
were not addressed to the topic (in mention, team and listen modes) are not held; they reach it as
catch-up. DMs are never held: you are told the topic is not listening.

## Receipts

| | |
|---|---|
| 👀 | delivered to the topic |
| 📥 | held for it |
| ⏳ | starting it |
| ✅ | started, or a permission allowed by a typed answer |
| ❌ | a permission denied by a typed answer |

## Permission prompts

When the topic needs a tool approved, the prompt is posted in its channel with **Allow** and
**Deny** buttons; the buttons are replaced with the outcome once answered. Replying `yes <id>` or
`no <id>` (or `y` / `n`) also works. Only allowed users can answer. A prompt still pending when the
session disconnects is dropped.

## DMs and notify

`notify` lets a topic DM the allowed users, all of them: "the build is green", "I need a decision".
Your reply comes back to the topic:

- A reply in the thread under a notify goes to the topic that sent it.
- Any other DM goes to the topic that last notified you there.
- A DM before any topic has notified you gets an explanation instead.

Only allowed users can reach a topic by DM. A topic can reply, edit and react in a DM it has
notified you in, as well as in its own channel.

## Mentions

A topic's messages cannot ping a crowd: `@channel`, `@here`, `@everyone` and user groups are
defanged, and a mention of someone who is not in the channel becomes their plain name. Long replies
are split into several messages.
