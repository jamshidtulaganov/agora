---
name: agora-telegram
description: "Use when you need to reach the humans on a task in Telegram rather than in Agora — posting a status line mid-run, sending a finished artifact to the team's group, or asking for a decision only a person should make (deploy, merge, anything with a blast radius). Documents the three commands (agora telegram chats / send / ask), what authorizes them (the chats already bound to you), why there is no edit or delete, and how `ask` blocks and what its exit codes mean. Does NOT cover the weekly autopilot report, which the platform posts on its own without you asking."
user-invocable: false
allowed-tools: Bash(agora *)
---

# Speaking in Telegram

You can post to the Telegram groups your bot is bound to, and you can ask those
groups a question and wait for an answer. Nothing else — no editing, no
deleting, no reaching a group you were not given.

Every claim here is pinned to source in
`references/telegram-source-map.md`.

## You do not hold the token

The bot token stays on the server. You name a chat and the server sends. This
is why there is no way to "just call the Bot API" — and why you should not go
looking for the token. If it ever ends up in your context, do not use it and do
not write it anywhere.

## Where you may speak

```
agora telegram chats
```

Lists the chats bound to you, marking the default. You cannot guess a chat id
and you should not hardcode one — a group can be rebound, and the id changes
under you.

Naming a chat you are not bound to fails. It does not fall back to the default:
a message delivered to the wrong room is worse than one not delivered.

## Posting

```
agora telegram send "Deploy started, ~4 minutes."
agora telegram send --chat -1004336001519 "QA passed on the sprint branch."
cat report.md | agora telegram send --stdin
```

Use stdin for anything long or multi-line; a shell argument mangles newlines
and quoting.

**There is no edit and no delete.** What you post is part of that room's
record. Write it as if it cannot be taken back, because it cannot.

### When to post at all

Post when a human's next action depends on it:

- a long run passing a milestone they are waiting on
- a result that changes what someone should do next
- a failure they need to know about before their next step

Do not narrate. A group that gets a message for every step stops reading them,
and then the one message that mattered is missed too. If nobody's behaviour
changes on reading it, it does not need to be sent.

## Asking for a decision

```
agora telegram ask "Deploy sd-bridge to staging?" \
  --option "Deploy" --option "Not now"
```

Blocks until someone taps a button, then prints the chosen option on stdout.

- **Exit 0** — someone chose; stdout is their choice, exactly as you offered it.
- **Non-zero** — nobody answered before the timeout. This is a STOP, not a
  default. Do not proceed as if the answer were yes; report that you asked and
  nobody decided.

The first tap wins, and only someone allowed to instruct you can answer. Once
answered, the buttons are replaced with the outcome so the room can see what
was decided and by whom.

Use it for decisions that are expensive or hard to reverse — deploying,
merging, deleting, anything touching production. Do not use it for things you
can determine yourself; a question a human has to answer costs them an
interruption, and asking one you could have resolved trains them to ignore the
next.

Give real alternatives. "Deploy" / "Not now" is a decision. "OK" / "Cancel" on
something you were going to do anyway is a formality that buys nothing.

## What the platform does without you

When an autopilot run completes, the platform posts your write-up to the group
by itself. You do not need to send it — doing so posts it twice.
