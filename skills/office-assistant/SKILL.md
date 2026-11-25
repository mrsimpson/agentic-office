---
name: office-assistant
description: How an assistant helps a human with the agentic office. Use it to connect to a hub, work with the tenant repository, set up agents, and run meetings. Use it for any task with `office`, `office agent run`, meetings, agent roles or the tenant repository.
---

# Office assistant

This skill is for an assistant that works for one human, for example a coding agent on the
human's computer.

Do not guess commands or flags. Read `office help` and `office help <command>`.
For the first setup, follow https://mrsimpson.github.io/agentic-office/llms.txt.

## Rules

- Never print, send or check in a private key (`key.pem`). Paths, public keys and key IDs are not
  secret. Never read the clipboard into the chat.
- Ask the human before you install software, change a hub, or write outside the project folder.
- The human confirms certificate fingerprints. You never do.
- Do not speak for the human (`say`, `signal turn-end`). If a test needs it, say that you did it
  and what you said.
- After each step, check the result (`office whoami`, `office config show`) before you go on.
- **Before any change** (create or register an agent, push, start or end a session), find out where
  you work: run `pwd` and `office config show` and read the lines `hub` and `tenant`. Do not rely
  on memory. If the tenant does not fit the task, or is unclear, ask first.

## Connection

- Put the hub in the **project file** `.office/config.yaml`, not in the user profile. Use
  `hub-profile` and a `hubs:` mapping. Do not use `office hub add` for this.
- A profile is needed because an old `client/hub.crt` in the user folder is otherwise the only
  certificate the client trusts.
- Keep the admin key in its own file. Use it only for single admin commands, for example
  `OFFICE_KEY=<admin key> office admin ...`.
- A system admin is never a member of a tenant. The human works with a separate user key.
- Tenant IDs, user IDs and session names are never used again. `remove` only ends them. Choose the
  name with care.

## Tenant repository

- Run `office sync init` in an empty project folder. The main directory mirrors `main` on the hub.
  A commit there is refused.
- To change files: `git worktree add --detach ../<name>`, commit, `git push origin HEAD:main`, then
  `office sync` in the main directory, then remove the worktree.
- The hub accepts only `.md` and `.yaml`/`.yml` files and no hidden files. Keep local files
  (`.office/`, key paths) out of the repository. List them in `.git/info/exclude`.
- If `office sync` stops because of a file with local changes, check that the change is also in the
  commit, then discard the local copy and run `office sync` again.

## Agents

- Give each agent its own key, register it with `admin user create --kind agent`, and add it with
  `admin member set <tenant> <id>`.
- Name an agent after its role, not after its harness.
- For an agent that the whole tenant uses, create a **team agent** instead (next section).
- In the repository: `agents/<name>/role.md` (the role) and `agents/<name>/agent.yaml` (a
  description, for people only).
- Local, not in the repository: `agents/<name>/.office/config.yaml` with the hub profile, `key:`,
  `agent.harness`, `agent.role-file` and `wake.strategy: turn-end`.
- **Never put the session in the configuration.** Give it at the start:
  `cd agents/<name> && office agent run --session <session>` (or set `OFFICE_SESSION`).
- By default the runner allows the harness only the office tools. Only the human may allow more
  (`agent.allow`). Allow as little as possible: start the agent without it, read the rejected titles
  in the log (`permission rejected`), then add narrow patterns. A pattern checks only the title of
  a call, not the path or a chained command. Tell the human about this limit.
- When you start a runner in the background, write its log to `/tmp/agent-<name>.log` and read it
  (`joined <session>`).

## Team agents

- A team agent belongs to the tenant, not to a person, and every member can start it. Create it
  with `office team-agent create <id>` (tenant admin only). It has no key and no `key:` or
  `agent.role-file` in the local configuration.
- Its definition is the file `agents/<id>.yaml` with `schema: office.agent.v1`, `role`, and
  optionally `allow` and `wake.strategy`. It holds no command, folder, address or key. Do not use
  the same name as a personal agent folder `agents/<id>/`.
- Change a definition only when the human asks. Show the change before you push it. Everyone who
  starts the agent next must confirm it again.
- Start it with `office agent run --team-agent <id> --session <session>`. Show the definition to
  the human and ask. Pass `--confirm <version>` only after the human said yes. Never confirm for
  them.
- For a server without a human, a tenant admin registers a key for the team agent
  (`office key create` on the server, then `office admin key add <id> <public key>`). The server
  starts the agent with that key and `--confirm <version>`. Never print or send that key.
- A team agent with a token starts no agent and changes no definition; the hub refuses it. This
  is on purpose. Tell the human to change the definition with their own key.

## Sessions

- A new meeting gets a new name. You can join an ended session, but it replays its history, and
  agents write their old notes again. Do not reuse a name.
- **Naming rule:** a session is called `<topic>-<YYYYMMDD>-<HHMM>`, with a timestamp to the minute,
  for example `status-20261119-1004`. Use lowercase letters, digits and hyphens. Create the
  timestamp, do not guess it: `date +%Y%m%d-%H%M`. Give the same name to everyone, and start the
  agents with `--session <name>`.
- **Banner after every start.** As soon as a session runs (a runner logged `joined <session>`, or
  you joined), print one paragraph with a large, bold text that shows the tenant and the session.
  Read both values from `office config show` and the runner log, not from memory:

  ```
  # **TENANT: example**
  # **SESSION: status-20261119-1004**
  ```

  This applies to every session, also to tests and to joining again. After you start another agent
  in the same session, print the banner again with a third line `# **AGENT: <name>**`.
- `meeting end` ends the meeting for everyone, and the runners stop by themselves. `meeting leave`
  only ends your own participation. There is no command to delete a session. You can remove the
  minutes and note files with a push.
- A typed sentence wakes an agent with the `turn-end` strategy only after `office signal turn-end`.
  Speech does not need it.
- Microphone: the human starts `office meeting join <session> --mic --language <language> --fast`
  in their own terminal.
- A stage link (`office stage link --session <session>`) and an admin link (`office admin link`) give
  access for a short time. Give them only to the human, and ask them not to share them.
