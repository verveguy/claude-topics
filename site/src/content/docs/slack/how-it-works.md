---
title: How the Slack bridge works
description: The hub, the shim, and what the hub remembers.
---

## The hub

One **hub** per machine, the `Slack Hub` [daemon](/claude-topics/running/daemons), holds the Slack
connection. It uses Socket Mode, an outbound WebSocket, so nothing on the machine is exposed.
Socket Mode spreads an app's events across its open connections rather than copying them to each,
so the hub has to be the only process connected to Slack. It routes each channel to its topic by
`links.json`, applies the channel's [mode](/claude-topics/slack/modes) and
[trust](/claude-topics/slack/trust), and makes the Slack calls the topics ask for.

## The shim

The shim is a Claude Code **channel**: an MCP server over stdio, declaring `claude/channel` and
`claude/channel/permission`, registered as a user-scope MCP server in each profile that has a
linked topic. It finds its topic with `topic whoami` (retrying while a brand-new topic's registry
entry is written) and registers with the hub over `hub.sock`. In a session whose topic has no
channel, it lists no tools.

Inbound messages wait until Claude Code has fetched the shim's tool list, because it only accepts
channel notifications after that. They arrive as
`<channel source="slack" channel="…" ts="…" user="…" user_id="…" directs="…" …>`, with
`thread_ts`, `addressed`, `held` and `dm` where they apply.

The shim's tools:

| Tool | Does |
|---|---|
| `reply` | post to the channel, a thread (`thread_ts`), or a DM the topic notified in (`channel`) |
| `edit_message` | edit one of its messages; edits notify no one |
| `react` | add an emoji reaction |
| `read_history` | read recent messages in the channel or a thread, labelled with who can direct |
| `channel_members` | who is in the channel, for mentions |
| `notify` | DM the allowed users |

The shim also gives Claude instructions on how to use them: Slack's mrkdwn rather than Markdown,
how catch-up is laid out, answering DMs in the DM, and the rules for who it acts for.

## What the hub remembers

The hub keeps its conversation state in `state.json` in the Slack home, so a restart loses none of
it:

- catch-up not yet sent,
- the threads the bot is in,
- which topic sent each notify DM, and which last notified you in each DM,
- messages held for a topic that is not listening.

When the hub starts, it forgets threads and notify DMs more than 30 days old.

## Files

Everything is in the Slack home, `<profile>/channels/slack/`:

| File | Holds |
|---|---|
| `.env` | tokens and allowed users; only you edit it |
| `slack-app-manifest.yaml` | the app manifest, rewritten by `topic slack setup` |
| `links.json` | channel → topic, with each channel's mode and trusted users |
| `state.json` | the hub's conversation state |
| `hub.sock` | the hub's socket for shims |
