# User guide

This guide shows how a team uses the agentic office: how to get the software, run a hub, join
meetings as a human with a headset, and bring in agents. For the architecture and every command
in detail, see [`README.md`](README.md).

The office has two programs:

| Program | Who runs it | What it does |
|---|---|---|
| `office-hub` | One person per team, the **operator**, on a server or a computer that everyone can reach | Orders the meeting, keeps the archive, serves the stage |
| `office` | Every **participant**: each human and each agent, on their own computer | Joins meetings, transcribes your speech locally, keeps a local copy of the meeting log, offers the tools (CLI and MCP) |

The examples have comments after `#`. In zsh, the default shell on macOS, run
`setopt interactivecomments` once in your terminal, or leave the comments out; otherwise zsh
takes them for commands or file names.

Contents:

1. [Get the software](#1-get-the-software): pre-built, or built yourself
2. [Run a hub](#2-run-a-hub) (operator, once per team)
3. [Join a meeting](#3-join-a-meeting) (humans)
4. [Bring in an agent](#4-bring-in-an-agent): with the agent runner, roles, a model that decides when to wake the agent, and a configuration file
5. [Try everything on one computer](#5-try-everything-on-one-computer)
6. [Troubleshooting](#6-troubleshooting)

## 1. Get the software

You have two ways: download the pre-built programs, or build them from the source.

| Platform | `office` (client) | `office-hub` |
|---|---|---|
| macOS, Apple silicon | pre-built or built yourself | pre-built, built yourself or container image |
| Linux, x86-64 or ARM64 | pre-built or built yourself | pre-built, built yourself or container image |
| Windows | in WSL 2, as on Linux (the microphone in WSL is not tested) | in WSL 2, or the container image |

### Pre-built

Each release on GitHub has one archive per program and computer, each with a `.sha256` file:

| Computer | Client | Hub |
|---|---|---|
| macOS, Apple silicon | `office-darwin-arm64.tar.gz` | `office-hub-darwin-arm64.tar.gz` |
| Linux, x86-64 | `office-linux-amd64.tar.gz` | `office-hub-linux-amd64.tar.gz` |
| Linux, ARM64 | `office-linux-arm64.tar.gz` | `office-hub-linux-arm64.tar.gz` |

The hub archives exist from release `v0.0.4` on. Download the newest release, for example the
client for a Mac, and check it:

```sh
mkdir -p ~/bin && cd ~/bin
base=https://github.com/mrsimpson/agentic-office/releases/latest/download
curl -fsSLO $base/office-darwin-arm64.tar.gz
curl -fsSLO $base/office-darwin-arm64.tar.gz.sha256
shasum -a 256 -c office-darwin-arm64.tar.gz.sha256
tar -xzf office-darwin-arm64.tar.gz
./office version
```

Put the folder on your `PATH`, or call the programs with their full path. Notes:

- On macOS, a file that you download with a browser is in quarantine. `curl` avoids that; or run
  `xattr -d com.apple.quarantine office` once.
- On Linux, the programs need glibc 2.34 or newer. The client also needs `libstdc++6` and
  `libgomp1`, which most distributions install.
- The hub needs `git`, because its archive is a Git repository.
- The hub also exists as a container image, `ghcr.io/mrsimpson/agentic-office/office-hub`
  (see [Run a hub](#2-run-a-hub)).

### Built yourself

You need Go 1.27 or newer, `make`, `git`, `cmake` and a C/C++ compiler.

macOS:

```sh
xcode-select --install                 # compiler, once
brew install go cmake ffmpeg           # ffmpeg records the microphone
git clone https://github.com/mrsimpson/agentic-office.git && cd agentic-office
make third_party/whisper.cpp whisper build
```

Linux (Debian or Ubuntu):

```sh
sudo apt install cmake g++ make git pulseaudio-utils    # Go from go.dev/dl if yours is older than 1.27
git clone https://github.com/mrsimpson/agentic-office.git && cd agentic-office
make third_party/whisper.cpp whisper build
```

The programs are then in `bin/office` and `bin/office-hub`. This build is optimised for the CPU of
your computer. To copy the client to other computers, build it with
`make whisper build WHISPER_NATIVE=OFF`.

## 2. Run a hub

The operator does this once per team. The hub must be reachable by all participants on one
port, by default 8740. Below, `hub.example.org` stands for the name of your server.

Run the hub as a binary (on Linux or macOS) or as a container with Docker or Podman. **On a Mac,
use the binary.** It runs directly on macOS, without a virtual machine. Apple's `container`
is not a good base for a hub yet: in our tests its service stopped answering, and `container exec`
and `container system stop` hung (see [Troubleshooting](#6-troubleshooting)).

### The first system admin

The hub has two kinds of admins. A **system admin** runs the hub: creates tenants and users and
names other system admins. A **tenant admin** runs one tenant: adds and removes its members. A
**tenant** is one organisation with its own repository; its sessions and records are visible only
to its members.

**A system admin is never a member of a tenant**, and a member is never made a system admin. If
you run the hub and also work in a tenant, use two users with two different keys.

You are the first system admin. Give this user a name that tells it apart from the users who work
in a tenant, for example `root`, and create its key on **your own computer** first:

```sh
office key create        # prints "public key: …"
```

### Start the hub with the binary

On the server. `CFG` is where the programs keep their files: `~/.config/agentic-office` on Linux,
`~/Library/Application Support/agentic-office` on macOS. The first line sets it for both:

```sh
CFG="$([ "$(uname)" = Darwin ] && echo "$HOME/Library/Application Support" || echo "${XDG_CONFIG_HOME:-$HOME/.config}")/agentic-office"
office-hub keygen ~/office-hub/signing.key           # once; keep the file safe and back it up
mkdir -p "$CFG/hub"
office-hub -signing-key ~/office-hub/signing.key -addr 0.0.0.0:8740 \
  -first-system-admin root=<your public key> -tls-hosts hub.example.org -public-url https://hub.example.org:8740
```

- The data of the hub lives in `$CFG/hub` (`-data-dir` changes it): the
  users and keys, the TLS certificate and the archive with all sessions and records.
- On the first start, the hub creates a self-signed TLS certificate for the names in
  `-tls-hosts`, in `tls.crt`. To use your own certificate, see "TLS and your own certificate" in
  [`README.md`](README.md).
- `-first-system-admin` names the first system admin at the first start. The hub ignores it later.
  A system admin is never a member of a tenant: if you also work in a tenant, use a second user
  with another key.
- To keep the hub running, start it as a service of your system, for example with systemd or
  launchd.

### Or start the hub as a container

With Docker or Podman (`podman` instead of `docker`):

```sh
H=ghcr.io/mrsimpson/agentic-office/office-hub
docker volume create office-hub
docker run --rm -v office-hub:/data $H keygen /data/signing.key
docker run -d --name office-hub --restart unless-stopped -p 8740:8740 -v office-hub:/data $H \
  -data-dir /data -addr 0.0.0.0:8740 -signing-key /data/signing.key \
  -first-system-admin root=<your public key> -tls-hosts hub.example.org -public-url https://hub.example.org:8740
docker cp office-hub:/data/tls.crt hub.crt
```

The last command copies the certificate for the participants. `--restart unless-stopped` starts
the hub again after a restart of the server. When you pass flags to the image, pass all of them,
as in the example.

### Give the participants the certificate

With the self-signed certificate, each participant needs the file `tls.crt` of the hub. Send it
to them, for example in your team chat; it holds no secret. With a certificate from a public CA,
they need nothing.

### Create a tenant and register the participants

Create a tenant and name its first tenant admin, a user who is not a system admin. Each participant
sends you their public key (see [Join a meeting](#3-join-a-meeting)). You register it from your
computer, then you or the tenant admin adds the user to the tenant. Registering a user and adding
it to a tenant are two steps, so one user can work in several tenants:

```sh
office admin user create grace <public key> --name "Grace Hopper" --kind human
office admin user create minutes-bot <public key> --name "Minute Bot" --kind agent
office admin tenant create acme grace --name "Acme Corp"          # grace is the first tenant admin
office admin member set acme minutes-bot                          # the tenant admin may do this too
```

`office admin member set <tenant> <user> --role tenant-admin` makes a member a tenant admin.
`office admin member remove <tenant> <user>` ends a membership, and `office admin member list <tenant>`
lists the members. `office admin tenant list` and `office admin tenant remove <tenant>` list and end
tenants, `office admin user list` and `office admin user remove <user>` do the same for users.
Removing ends the validity and keeps the record, so an ID is never used again. A tenant always
keeps at least one tenant admin, and the hub at least one system admin.

`office admin key add <user> <public key>` adds a further key to a user. `office admin key revoke <key-id>` revokes a key.
The last key of a user cannot be revoked.

A system admin can do the keys in the browser too. The admin view lists all users and keys, creates a
user or adds a key with a form, and revokes a key after a confirmation. It shows the tenants and the
system admins, read only. Tenants and memberships stay with the commands above:

```sh
office admin link
```

The command prints a link. Open it in a browser within a few minutes: the link works once. The
page then stays open for one hour. After that, or if you see `Admin link not valid` or `Session
ended`, run `office admin link` again. Only system admins get a link.

## 3. Join a meeting

### Set up once

1. Get the client (see [Get the software](#1-get-the-software)). To speak, you also need a
   program that records the microphone: `ffmpeg` on macOS (`brew install ffmpeg`), `parec` on
   Linux (package `pulseaudio-utils`).
2. Tell the client where the hub is, for example in your `~/.zshrc` or `~/.bashrc`:

   ```sh
   export OFFICE_HUB=https://hub.example.org:8740
   ```

3. Put the certificate of the hub where the client looks for it:

   ```sh
   CFG="$([ "$(uname)" = Darwin ] && echo "$HOME/Library/Application Support" || echo "${XDG_CONFIG_HOME:-$HOME/.config}")/agentic-office"
   mkdir -p "$CFG/client"
   cp hub.crt "$CFG/client/hub.crt"
   ```

4. Create your key and send the public key to your admin:

   ```sh
   office key create
   ```

5. Tell the client which tenant you work in, for example in your `~/.zshrc` or `~/.bashrc`. If you
   are a member of several tenants, choose per command with `--tenant`:

   ```sh
   export OFFICE_TENANT=acme
   ```

6. After the admin has registered you and added you to the tenant, check it: `office whoami` prints
   your participant ID.

Your private key stays in `$CFG/client/key.pem`. It never leaves your computer.

### The speech models

The client transcribes your speech on your own computer with Whisper. It chooses the speech model
by the language you speak:

- German: `large-v3-turbo-german`, a model fine-tuned for German (1.6 GB).
- Other languages: `large-v3-turbo` (1.6 GB).
- With `--fast`, or when your client cannot use a GPU: `small` (490 MB). It is fast on every
  computer, but makes more mistakes. The client says when it switches to `small` by itself. At the
  moment, only the client for Macs with Apple silicon uses the GPU.

Tell the client your language with `--language`, for example `--language german` or
`--language english` (the default is German). The first join with the microphone downloads the
chosen model once and checks it. To see which model the client would use, and to download it ahead
of time, for example before your first meeting:

```sh
office models list --language english
office models download --language english
```

To use a certain model, name it: `--model small`, `--model large-v3-turbo`, or a path to a file.

They are stored in `~/.local/share/agentic-office/models/` (or `$XDG_DATA_HOME/agentic-office/models/`).

### Teach the client your words

Whisper often misspells names and terms of your project. Write them into `.office/vocabulary.txt`
in the directory where you start the client, one per line, the most important first:

```text
# names and terms of our project
Businessplan
agentic-office
Quartalszahlen
```

The client gives these words, and the names of the people in the meeting, to Whisper before each
sentence. Only about 30 terms fit; the client says how many it uses. To use another file, add
`--vocabulary FILE` to `meeting join`.

The client can also write the file for you. It reads the Markdown and text files of your project
and finds names of people, organisations and places, and terms such as `SyncEngine` or `DSGVO`:

```sh
office vocabulary extract docs/
```

The first run downloads a model of about 540 MB. The run can take a few minutes for many
documents. Then check `.office/vocabulary.txt`: delete what is wrong and move the most important
terms to the top.

### Speak in a meeting

Use a headset: the client has no echo cancellation. Join with the name of the meeting, for
example `weekly`:

```sh
office meeting join weekly --mic
```

- Joining with the microphone means that you agree that your speech is transcribed and stored.
- The terminal shows each of your sentences: `final` when Whisper has the text, `sent` when it is
  in the meeting log. Only text leaves your computer, never audio.
- `p` pauses and resumes the microphone. `o` marks your last sentence as off the record: the
  others then see a placeholder instead of the text. `q` leaves.
- On macOS, the system asks once whether your terminal may use the microphone.

Without a microphone, join with `office meeting join weekly` and write instead of speaking:
`office say "I agree"`.

### Show the stage

The stage shows the meeting in two columns:

- **Left:** the artifact in focus, large. The **minutes** are the first artifact: they are in
  focus until someone focuses another artifact. Until there are minutes, the stage asks you to
  bring in an agent that takes them (see [Bring in an agent](#4-bring-in-an-agent)). When a session
  has more than one artifact, tabs appear above, with the minutes first. Below is a log of who
  created, changed or focused which artifact.
- **Right:** the participants with their capture status (captured, paused, not captured), agents
  marked as agents, and a hand with a number for everyone who raised their hand, in the order they
  raised it. Below it is the transcript of the latest sentences.

The stage is dark or light, as your computer is set. One person creates a link, opens it in a
browser and shares the window in your meeting tool (Zoom, Teams, Meet, Jitsi):

```sh
office stage link --session weekly
```

### Work on documents

Anyone in the meeting, human or agent, can write a text document. A document is a Markdown file
under `documents/`. You can show it on the stage:

```sh
office document create documents/plan.md --file plan.md --session weekly   # prints its version
office stage focus documents/plan.md              # the stage shows it; `stage focus minutes` goes back
office document get documents/plan.md             # read it
office document change documents/plan.md --base <version> --file plan.md   # replace it
office document list
```

A change names the version that you read (`--base`). If someone else changed the document in the
meantime, the change fails with `aborted` and tells you the current version: read the document
again and redo your change. Your name is recorded as the author of every change. Never put a
sentence that is off the record into a document.

### More things you can do

```sh
office hand raise --topic "Budget"
office decision record "We rent the new office from January."
office task create "Send the numbers to the bank" --assignee grace
office meeting end
```

`office help` lists all commands. [`README.md`](README.md) describes each one.

## 4. Bring in an agent

An agent is a participant like you. It follows the meeting and acts through tools: it can keep
the minutes, record decisions, raise its hand, write documents, show slides and speak. Several agents can take
part in the same meeting, for example Claude Code, opencode and Kiro next to each other. The
**agent runner** (`office agent run`) keeps each one in the meeting, so that people can talk to
it in plain words. Each agent needs:

- its **own participant ID and key**, so that everyone sees who said or recorded what;
- its **own folder**, where its tool keeps its work and settings;
- the **hub and its certificate**, as for a human (see [Set up once](#set-up-once)). On the
  computer of a human participant, the agent uses the same `OFFICE_HUB` and the same certificate.

### Create and register the keys

On the computer where the agents run, create one key per agent. The example creates keys for
three agents:

```sh
CFG="$([ "$(uname)" = Darwin ] && echo "$HOME/Library/Application Support" || echo "${XDG_CONFIG_HOME:-$HOME/.config}")/agentic-office"
for a in claude opencode kiro; do
  office key create --key "$CFG/agents/$a/key.pem"
done
```

Each `key create` prints a public key. Send them to your admin, who registers each one as an
agent and adds it to the tenant (see
[Create a tenant and register the participants](#create-a-tenant-and-register-the-participants)):

```sh
office admin user create claude-bot <public key> --name "Claude" --kind agent
office admin user create opencode-bot <public key> --name "opencode" --kind agent
office admin user create kiro-bot <public key> --name "Kiro" --kind agent
office admin member set acme claude-bot; office admin member set acme opencode-bot; office admin member set acme kiro-bot
```

If you are the admin yourself, on the same computer, this does both steps at once:

```sh
for a in claude opencode kiro; do
  PUB=$(office key create --key "$CFG/agents/$a/key.pem" | awk '/^public key:/{print $3}')
  office admin user create "$a-bot" "$PUB" --name "$a" --kind agent
  office admin member set acme "$a-bot"
done
```

Check a key: `OFFICE_KEY="$CFG/agents/kiro/key.pem" office whoami` prints `kiro-bot`.

### Start the agent with the runner

Claude Code, opencode and Kiro work in turns: they answer and then wait for you. In a meeting
nobody types into their chat, so they would stop listening. The agent runner keeps them in the
meeting. Start one runner per agent:

```sh
office agent run --session weekly --role "You keep the minutes." -- <harness command>
```

The program after `--` is the **harness**: the program that runs the agent. The runner talks to
it over the Agent Client Protocol (ACP). The runner:

1. joins the meeting with the agent's key (`OFFICE_KEY`) and follows the local log;
2. starts the harness and hands it the office tools for this meeting. You do not add the office
   as an MCP server in the harness;
3. tells the agent its role and how the meeting works;
4. decides when to wake the agent: at each change of speaker, or after each sentence of someone
   else with a model that you bring (see
   [When the runner wakes the agent](#when-the-runner-wakes-the-agent)). When it wakes the agent,
   it sends everything the others said since the last time: who spoke, whether a human or an
   agent, and what they said;
5. at the end of the meeting, gives the agent time for its last work, for example the minutes.
   Then it stops. Stop it earlier with Ctrl+C.

The agent decides by itself whether someone addressed it. So people talk to it in plain words,
without a keyword: "Claude, what did we decide on the budget?" or "Can the minute taker read back
the last point?". The agent does not interrupt a speaker: it answers when the speaker has
finished, raises its hand, or writes. It speaks only through the tool `utterance_say`; what it
writes in its own chat reaches nobody.

Start each agent in its own folder and terminal, with its own key. Log in to each harness once
before, as you would to use it alone. The runner does not log in for you.

**Claude Code**, through its ACP adapter `@agentclientprotocol/claude-agent-acp` (needs Node.js):

```sh
mkdir -p ~/office-agent-claude && cd ~/office-agent-claude
OFFICE_KEY="$CFG/agents/claude/key.pem" office agent run --session weekly --role-file minutes.md \
  -- npx -y @agentclientprotocol/claude-agent-acp
```

**opencode**:

```sh
mkdir -p ~/office-agent-opencode && cd ~/office-agent-opencode
OFFICE_KEY="$CFG/agents/opencode/key.pem" office agent run --session weekly --role-file tasks.md \
  -- opencode acp
```

**Kiro**:

```sh
mkdir -p ~/office-agent-kiro && cd ~/office-agent-kiro
OFFICE_KEY="$CFG/agents/kiro/key.pem" office agent run --session weekly --role-file decisions.md \
  -- kiro-cli acp
```

The runner prints what it does on its terminal, each line starting with `office agent:`: each
wake-up, each tool call, each permission and what the agent wrote in its chat.

**What the agent may do.** The harness asks the runner before it uses a tool. By default the
runner allows the office tools and rejects everything else, for example shell commands or edits
of files. To allow more, give patterns of tool titles with `--allow` (`*` is any text). The
patterns replace the default, so include the office tools, for example
`--allow '*office*' --allow 'read*'`. `--allow '*'` allows everything. A harness that does not
ask, because its own settings allow a tool, follows its own settings.

`--cwd DIR` sets the folder of the agent's session; the default is the current folder.

### Give each agent a role

The role tells the agent what to do in the meeting. Write it in plain words, with `--role TEXT`
or in a file with `--role-file FILE`. Give each agent its own role, so that they do not talk over
each other. For example:

- `minutes.md`, for Claude Code:
  > You are Claude, the minute taker. Keep the minutes with minutes_update after each topic.
  > Answer when someone asks you about what was said or decided. Otherwise stay silent.
- `tasks.md`, for opencode:
  > You are opencode, the task keeper. When someone takes on a piece of work, create a task with
  > task_create, with the person as the assignee. Speak only when someone asks you about tasks.
- `decisions.md`, for Kiro:
  > You are Kiro, the decision keeper. Record each decision with decision_record, in one clear
  > sentence. Speak only when someone asks you about decisions.

All three can run in the meeting `weekly` at the same time. Each agent hears the others: the
runner also tells it what the other agents said, and that they are agents.

### When the runner wakes the agent

Waking a harness with a large model costs time and money. So you choose when the runner wakes
the agent, with `--wake`:

| `--wake` | The runner wakes the agent | You need |
| --- | --- | --- |
| `turn-end` (the default) | at each change of speaker: when someone else ends their turn | nothing |
| `llm` | when a **forwarding judge** says so, after each sentence of someone else | a general language model, for example a small one in Ollama, or your own program |
| `decision-model` | when a **decision model** says so, after each sentence of someone else | a key for a decision model, for example TypeSafe Jev or Cloudflare Clef |

The office ships no model; you bring it. A forwarding judge is the model or program to which the
runner forwards its question, for example a general language model; it answers with yes or no
and a reason. A decision model is a small model made for such questions; it answers with
probabilities.

**With `turn-end`**, the runner says at start that it does not notice a new topic within a
monologue.

**With `llm` or `decision-model`**, the runner asks the model after each sentence of someone
else. It wakes the agent when the model says that:

- someone addressed the agent, by name, by its role or by the context;
- what was said concerns the agent's role, so it should contribute now. This also works in the
  middle of a long monologue, for example when the speaker moves to a new topic;
- a topic or a block of topics has ended. Then the runner also gives the agent the task for the
  end of a topic. Set it with `--topic-task`, for example
  `--topic-task "Update the minutes and record the decisions of this block."`.

What did not wake the agent stays and reaches it with the next wake-up, so it misses nothing.

**When the model fails**, the runner falls back to `turn-end`. A failure is an error, no answer
within `--decide-timeout` (default `10s`), or an answer the runner cannot read.

- After one failure, the runner does not wake the agent. The sentences wait until the speaker
  ends the turn.
- After three failures within five minutes, the runner works as with `turn-end` and says so
  once on its terminal. It then wakes the agent at each change of speaker, and asks the model
  again once after each change of speaker, at the next sentence. When the model answers again,
  the runner switches back and says so.

When you speak, the ears end your turn after a short silence: 1.8 seconds by default. Change it
with `office meeting join --turn-silence 2.5s`. Text that you type with `office say` does not end
your turn: run `office signal turn-end` when you are done.

### Bring a decision model

A decision model answers questions about the meeting with probabilities. The runner speaks its
API, the System One API, with `--wake decision-model`. Give the full URL of the endpoint with
`--decide-url`; the runner appends nothing. Give the model with `--decide-model` and the key in
`OFFICE_DECIDE_KEY`. The runner sends the key as a bearer token and never logs it.

**TypeSafe Jev.** The endpoint is `https://api.typesafe.ai/v1/systemone`; the models are
`jev-latest` and `jev-1.14.0`. Input costs $0.042 per million tokens; output is free.

```sh
export OFFICE_DECIDE_KEY=...   # your TypeSafe key
OFFICE_KEY="$CFG/agents/kiro/key.pem" office agent run --session weekly --role-file decisions.md \
  --wake decision-model --decide-url https://api.typesafe.ai/v1/systemone --decide-model jev-1.14.0 \
  -- kiro-cli acp
```

**Cloudflare Clef (Workers AI).** Cloudflare states that Clef is compatible with the Jev API. The
endpoint has your account ID in it; the models are `clef` and `clef-flash`. Input to Clef costs
$0.24 per million tokens.

```sh
export OFFICE_DECIDE_KEY=...   # a Cloudflare API token for Workers AI
OFFICE_KEY="$CFG/agents/kiro/key.pem" office agent run --session weekly --role-file decisions.md \
  --wake decision-model \
  --decide-url https://api.cloudflare.com/client/v4/accounts/<your account ID>/ai/run/@cf/cloudflare/clef \
  --decide-model clef -- kiro-cli acp
```

**OpenRouter** lists Jev too, but its chat endpoint does not take the parameters that
`--wake llm` needs. Use the System One endpoint with `--wake decision-model` instead.

The runner asks three questions: should the agent act now, has a topic ended, and whom did the
speaker address (a participant or nobody). A probability at or above `--decide-threshold`
(default `0.5`) counts as yes. Raise it if the agent wakes too often; lower it if it misses
questions. The runner prints the probabilities on its terminal, for example
`act 0.93, topic_closed 0.10, addressed kiro-bot (0.80)`.

### Bring a forwarding judge

The runner forwards a short request about the meeting to the forwarding judge, which answers
with JSON. Use `--wake llm` and connect it in one of two ways.

**A server with an OpenAI-compatible API.** It must support structured output with a JSON
schema (`response_format` of type `json_schema`). Give the base URL that ends in `/v1` with
`--decide-url` and the model name with `--decide-model`. The runner posts to
`<URL>/chat/completions`.

With [Ollama](https://ollama.com) on your computer:

```sh
ollama pull qwen3:4b
OFFICE_KEY="$CFG/agents/kiro/key.pem" office agent run --session weekly --role-file decisions.md \
  --wake llm --decide-url http://127.0.0.1:11434/v1 --decide-model qwen3:4b -- kiro-cli acp
```

With the llama.cpp server, which downloads the model from Hugging Face and serves it under the
name you give with `--alias` (run it in its own terminal):

```sh
llama-server -hf ggml-org/Qwen3-4B-GGUF --alias decide --port 8080
```

```sh
OFFICE_KEY="$CFG/agents/kiro/key.pem" office agent run --session weekly --role-file decisions.md \
  --wake llm --decide-url http://127.0.0.1:8080/v1 --decide-model decide -- kiro-cli acp
```

The models are examples. Any model works that the server can run with structured output; try a
small one first. If the server needs a key, put it into `OFFICE_DECIDE_KEY`. The runner sends it
as a bearer token and never logs it.

**Your own program.** Give a shell command with `--decide-cmd`, for example
`--wake llm --decide-cmd "python3 decide.py"`. The runner starts it once per check. It writes a
request as JSON to the program's standard input:

```json
{"session": "weekly",
 "agent": {"participant_id": "kiro-bot", "name": "Kiro", "kind": "agent"},
 "role": "You are Kiro, the decision keeper. …",
 "participants": [{"participant_id": "ada", "name": "Ada", "kind": "human"}],
 "context": [{"event_id": 38, "participant_id": "ada", "name": "Ada", "kind": "human", "text": "…"}],
 "new": [{"event_id": 42, "participant_id": "ada", "name": "Ada", "kind": "human", "text": "Kiro, what did we decide last week?"}],
 "turn_ended": false}
```

`new` are the sentences the agent has not got yet; the last one was just said. `context` are up
to 20 earlier sentences. `turn_ended` says whether the speaker has ended their turn. The program
writes the answer as JSON to its standard output:

```json
{"act": true, "addressed": "kiro-bot", "topic_closed": false, "reason": "Ada asked Kiro by name"}
```

`act` (wake the agent now) and `topic_closed` (a topic has ended) must be there. `addressed` and
`reason` may be empty. [`README.md`](README.md), section "Agent runner", has the details.

**Where the model runs matters.** A forwarding judge or a decision model gets the text of the
meeting, as the agent's own model does. A hosted model sends this text to its provider. A local model keeps it on your
computer. Sentences off the record are never sent.

### Start an agent with a configuration file

The commands above are long. Put the options into a file instead, and start the agent with
`office agent run` alone. The file is YAML. There are two places for it:

- your **user file**, `config.yaml` in your configuration folder, next to your key:
  `~/.config/agentic-office/client/config.yaml` on Linux,
  `~/Library/Application Support/agentic-office/client/config.yaml` on a Mac. Use it for what
  holds for all your projects, for example your decision model;
- the **project file**, `.office/config.yaml` in the folder where you start the command. Use it
  for what belongs to the project, for example the role of the agent.

The project file wins over the user file, key by key. Environment variables win over both
files, and the options on the command line win over everything. So you can still change one
thing for one start, for example `office agent run --wake turn-end`.

Keep one folder per role in the project, each with its own `.office/config.yaml`, and check the
files in with the project. For example, `agents/minutes/.office/config.yaml`:

```yaml
agent:
  session: weekly
  harness: [npx, -y, "@agentclientprotocol/claude-agent-acp"]
  role-file: minutes.md            # next to the folder .office, that is agents/minutes/minutes.md
  topic-task: Update the minutes and record the decisions of this block.
  wake:
    strategy: decision-model
    url: https://api.typesafe.ai/v1/systemone
    model: jev-1.14.0
    key-env: OFFICE_DECIDE_KEY     # the name of the variable with the key, never the key
```

Then:

```sh
cd agents/minutes
export OFFICE_DECIDE_KEY=...      # your key stays in the environment
OFFICE_KEY="$CFG/agents/claude/key.pem" office agent run
```

**Keys never go into a file.** The file only names the environment variable that holds the key
(`key-env`). So nobody finds a key in the repository. If two models need two keys, give them two
variables, for example `key-env: TYPESAFE_KEY` in one file and `key-env: CLOUDFLARE_KEY` in the
other.

A typo stops the command and names the file, the line and the key, for example
`line 6: unknown key agent.wake.modell`. To see what the runner will use and where each value
comes from, run `office config show` in the same folder. It shows whether the key variable is
set, but never the key. The README lists all keys.

### Without the runner: the tools only

A harness can also use the office directly as an MCP server, without the runner. Then the agent
must keep reading new events itself, and it stops listening when its turn ends. This suits short
jobs, for example writing the minutes after a meeting. Add `office mcp` as an MCP server in the
agent's folder. In a configuration file, write absolute paths: no `~` and no `$HOME`. The
`cat <<EOF` in the Kiro example fills them in for you.

```sh
# Claude Code
claude mcp add office -e OFFICE_KEY="$CFG/agents/claude/key.pem" -e OFFICE_HUB="$OFFICE_HUB" \
  -- "$(command -v office)" mcp --session weekly
# opencode (writes opencode.json in the folder)
opencode mcp add office --env OFFICE_KEY="$CFG/agents/opencode/key.pem" --env OFFICE_HUB="$OFFICE_HUB" \
  -- "$(command -v office)" mcp --session weekly
# Kiro
mkdir -p .kiro/settings
cat > .kiro/settings/mcp.json <<EOF
{
  "mcpServers": {
    "office": {
      "command": "$(command -v office)",
      "args": ["mcp", "--session", "weekly"],
      "env": { "OFFICE_KEY": "$CFG/agents/kiro/key.pem", "OFFICE_HUB": "$OFFICE_HUB" },
      "autoApprove": ["meeting_events", "meeting_log", "whoami"]
    }
  }
}
EOF
```

Then start the harness (`claude`, `opencode` or `kiro-cli chat`) and tell the agent what to do,
for example:

> Join the meeting "weekly" (meeting_join). Keep reading new events with meeting_events, from the
> last one you read. Keep the minutes with minutes_update. Speak (utterance_say) only when someone
> addresses you as "Claude". Stop when the session ends.

The tools do not wait for new events by themselves: the agent calls `meeting_events` again and
again. If it stops in between, tell it to keep listening.

An agent that works in a shell can also read the local copy of the meeting log and act with the
same commands as a human:

```sh
export OFFICE_KEY="$CFG/agents/claude/key.pem"
LOG=$(office meeting join weekly)
tail -f "$LOG" | grep '"utterance.final"\|"turn.end"'
```

`meeting join` prints the path of the log. Each line is one event as JSON.

An agent that runs in a container uses the image `ghcr.io/mrsimpson/agentic-office/office`.
See "Container images" in [`README.md`](README.md).

## 5. Try everything on one computer

To get to know the office before you set up a hub, `scripts/try-local.sh` starts a hub and three
participants in containers: `root`, the system admin who runs the hub, Ada, a human and the tenant
admin of the tenant `trial`, and a bot, an agent. You need the repository
(`git clone`), `bash` and one of these container runtimes:

| Platform | Container runtime |
|---|---|
| Linux | Docker or Podman |
| macOS | Docker Desktop, Podman, or Apple's [`container`](https://github.com/apple/container) (Apple silicon, macOS 26) |
| Windows | Docker Desktop with WSL 2; run the script in a WSL 2 shell |

**Apple `container`, once:** containers find the hub by its DNS name, and `container` gives them
names only if you set a DNS domain. Add these lines to `~/.config/container/config.toml`, then run
`container system stop` and `container system start`:

```toml
[dns]
domain = "test"
```

Apple's `container` is fine for this tour, but its service can stop answering (see
[Troubleshooting](#6-troubleshooting)). Docker Desktop or Podman are more reliable on a Mac. A
port that `container` publishes listens on all network interfaces of your Mac, not only on
`localhost`.

**Start and try:**

```sh
scripts/try-local.sh up                              # prints the stage link; open it in a browser
scripts/try-local.sh as bot meeting join weekly
scripts/try-local.sh as ada meeting join weekly
scripts/try-local.sh as ada say "Good morning, everyone"
scripts/try-local.sh as bot hand raise --topic "Budget"
scripts/try-local.sh tail bot                        # what the agent reads; stop with Ctrl+C
```

- `scripts/try-local.sh add grace human` adds a participant.
- `scripts/try-local.sh register me <public key> human` registers a client on your computer. That
  client then joins this hub with `OFFICE_HUB=https://localhost:8740` and the certificate in
  `office-trial/hub.crt`. This is how you try your headset (see [Join a meeting](#3-join-a-meeting)).
- `scripts/try-local.sh down` removes everything again.

| Variable | Default | Meaning |
|---|---|---|
| `OFFICE_RUNTIME` | the first of `docker`, `podman`, `container` | Container runtime |
| `OFFICE_VERSION` | `latest` | Image tag, for example `0.0.3` |
| `OFFICE_PORT` | `8740` | Port of the hub on your computer |
| `OFFICE_TRIAL_DIR` | `./office-trial` | Where the script puts the certificate of the hub |

## 6. Troubleshooting

| Problem | What to do |
|---|---|
| `certificate signed by unknown authority` or `certificate is not standards compliant` | The client lacks the certificate of the hub. Copy `tls.crt` to `$CFG/client/hub.crt` (on macOS `~/Library/Application Support/agentic-office/client/hub.crt`), or set `OFFICE_CA_CERT`. |
| `certificate is valid for …, not …` or `cannot validate certificate for …` | The name or address in `OFFICE_HUB` is not in the certificate. Delete `tls.crt` and `tls.key` in the data directory of the hub and start it with this name in `-tls-hosts`. Then give the participants the new `tls.crt`. |
| `public key is not registered: ask an admin to register SHA256:…` | Send your public key to your admin (`office key show` prints it). Check with `office whoami`. |
| `Admin link not valid` or `Session ended` in the admin view | The link works once and for a few minutes, and the page for one hour. Run `office admin link` again. Only system admins get a link (`permission_denied` otherwise). |
| `the document has changed since you read it` (`aborted`) | Someone changed the document first. Read it again with `office document get` and redo your change with the new version in `--base`. |
| `no audio recorder found` | Install `ffmpeg` (macOS) or `pulseaudio-utils` (Linux), or set `OFFICE_MIC_COMMAND` (see [`README.md`](README.md)). |
| The model download fails | Check your network, or download the models elsewhere and set `OFFICE_MODEL_URL` to a mirror. |
| `SIGILL: illegal instruction` | A client built on another computer uses CPU features yours lacks. Use the pre-built client, or build with `WHISPER_NATIVE=OFF`. |
| `office: the harness stopped …` or `ACP session/new: …` | The harness ended or refused the session. Run its command alone (for example `opencode acp`) and read its error. Often it needs a login first: log in with the harness itself. |
| The agent never speaks | Read the runner's terminal. Does it wake the agent (`waking the agent with …`)? With `--wake turn-end` (the default), or after the model failed, it wakes the agent only when your turn ends: pause for a moment, or run `office signal turn-end` after typed text. Does the runner reject tools that the agent needs (`permission rejected`)? Allow them with `--allow`. |
| `the forwarding judge failed (…)` or `the decision model failed (…)` | The sentences wait for the end of the turn; after three failures the runner switches to `turn-end`. For `--wake llm`, check `--decide-url` (it ends in `/v1`), `--decide-model` and `OFFICE_DECIDE_KEY`, and that the server supports structured output with a JSON schema. For `--wake decision-model`, check the full URL of the endpoint, the model and the key. A slow model needs a larger `--decide-timeout`. |
| `the trial is running already` | Run `scripts/try-local.sh down`, then `up` again. |
| `Apple's container needs a DNS domain` | Do the one-time setup in [Try everything on one computer](#5-try-everything-on-one-computer). |
| Apple's `container` hangs: `container exec`, `logs` or `system stop` do not return, or the port takes connections but answers nothing | Stop the `container` services with launchd, then start them again: `launchctl list \| awk '/com\.apple\.container\./ {print $3}' \| while read s; do launchctl bootout "gui/$(id -u)/$s"; done`, then `container system start`. If that does not help, restart the Mac. |
| `bind: address already in use` for port 8740 | Another hub runs, for example `office-trial-hub` from `scripts/try-local.sh`. Find it with `lsof -i :8740`; remove the trial with `scripts/try-local.sh down`. |
