---
title: Trust
description: Who a topic takes direction from, and what that does and does not protect.
---

A message to a topic is an instruction to a Claude session running as you, on your machine. So the
bridge distinguishes three kinds of people.

| | Run `/topics`, answer permission prompts, DM a topic | Direct the topic in a channel | Heard by the topic |
|---|---|---|---|
| **Allowed users** (`SLACK_ALLOWED_USERS`) | yes | in every linked channel | yes |
| **Trusted** in a channel (`/topics trust`) | no | in that channel only | in that channel |
| **Everyone else** | no | no | only in `team` and `listen` [modes](/claude-topics/slack/modes) |

```text
/topics trust @dana @lee      # in this channel, the topic takes direction from them too
/topics untrust @lee
/topics status                # shows who is trusted here
```

Trusting someone in one channel gives them nothing in another. Allowed users are never stored as
trusted; they already direct everywhere. A trusted user can do anything an allowed user can *say*
in that channel, including starting a down topic by replying `up`, but cannot run `/topics` or
answer permission prompts.

## What "cannot direct" means

In team and listen modes, "cannot direct" is an **instruction to Claude, not a sandbox.** Someone
persuasive could still talk a topic into acting. What actually stops them:

- Anything that needs a permission prompt waits for an allowed user to press Allow.
- The topic's Slack tools reach only its own channel and the DMs it has sent with `notify`.

Tools your settings already allow are not covered by either. Keep team and listen modes for people
you would let watch over your shoulder, and use a sandboxed topic for anyone else.

A topic also refuses, whoever asks, to change who may use the channel: editing the Slack `.env` or
`links.json`, adding allowed users, trusting someone, or approving a pending permission. It tells
them to ask you in the terminal.

## Channels and invites

- Inviting the bot creates nothing. In an unlinked channel it tells an allowed inviter how to link
  it, and tells anyone else it only takes topics from allowed users. Nothing is created until an
  allowed user runs `/topics new` or `/topics link`.
- The hub holds the Slack tokens; sessions never see them.
- The hub's socket is `hub.sock` in the Slack home: mode 0600, in a 0700 directory.
