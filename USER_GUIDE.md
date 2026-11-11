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
4. [Bring in an agent](#4-bring-in-an-agent)
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

### The first admin

An admin registers the keys of the other participants. You are the first admin, so create your
key on **your own computer** first:

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
echo "ada <your public key> Ada" > "$CFG/hub/admins"
office-hub -signing-key ~/office-hub/signing.key -addr 0.0.0.0:8740 \
  -tls-hosts hub.example.org -public-url https://hub.example.org:8740
```

- The data of the hub lives in `$CFG/hub` (`-data-dir` changes it): the
  registered keys, the TLS certificate and the archive with all sessions and records.
- On the first start, the hub creates a self-signed TLS certificate for the names in
  `-tls-hosts`, in `tls.crt`. To use your own certificate, see "TLS and your own certificate" in
  [`README.md`](README.md).
- The hub reads `admins` at start. After you change it, restart the hub.
- To keep the hub running, start it as a service of your system, for example with systemd or
  launchd.

### Or start the hub as a container

```sh
H=ghcr.io/mrsimpson/agentic-office/office-hub
docker volume create office-hub
docker run --rm -v office-hub:/data $H keygen /data/signing.key
docker run --rm -v office-hub:/data --entrypoint sh $H -c 'echo "ada <your public key> Ada" > /data/admins'
docker run -d --name office-hub --restart unless-stopped -p 8740:8740 -v office-hub:/data $H \
  -data-dir /data -addr 0.0.0.0:8740 -signing-key /data/signing.key \
  -tls-hosts hub.example.org -public-url https://hub.example.org:8740
docker cp office-hub:/data/tls.crt hub.crt          # the certificate for the participants
```

### Give the participants the certificate

With the self-signed certificate, each participant needs the file `tls.crt` of the hub. Send it
to them, for example in your team chat; it holds no secret. With a certificate from a public CA,
they need nothing.

### Register a participant

Each participant sends you their public key (see [Join a meeting](#3-join-a-meeting)). You
register it from your computer:

```sh
office admin key register grace <public key> --name "Grace Hopper" --kind human
office admin key register minutes-bot <public key> --name "Minute Bot" --kind agent
```

`office admin key revoke <key-id>` removes a key again.

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

5. After the admin has registered you, check it: `office whoami` prints your participant ID.

Your private key stays in `$CFG/client/key.pem`. It never leaves your computer.

### The speech models

The client transcribes your speech on your own computer with Whisper. It needs two model files,
about 490 MB together. The first join with the microphone downloads them once from the source of
whisper.cpp and checks them. To download them ahead of time, for example before your first
meeting:

```sh
office models download
```

They are stored in `~/.local/share/agentic-office/models/` (or `$XDG_DATA_HOME/agentic-office/models/`).

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

The stage shows the meeting: who is captured, the latest sentences, raised hands, slides and the
minutes. One person creates a link, opens it in a browser and shares the window in your meeting
tool (Zoom, Teams, Meet, Jitsi):

```sh
office stage link --session weekly
```

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
the minutes, record decisions, raise its hand, show slides and speak. Several agents can take
part in the same meeting, for example Claude Code, opencode and Kiro next to each other. The
**agent runner** keeps each one in the meeting, so that you can talk to it in plain words. Each
agent needs:

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
agent (see [Register a participant](#register-a-participant)):

```sh
office admin key register claude-bot <public key> --name "Claude" --kind agent
office admin key register opencode-bot <public key> --name "opencode" --kind agent
office admin key register kiro-bot <public key> --name "Kiro" --kind agent
```

If you are the admin yourself, on the same computer, this does both steps at once:

```sh
for a in claude opencode kiro; do
  PUB=$(office key create --key "$CFG/agents/$a/key.pem" | awk '/^public key:/{print $3}')
  office admin key register "$a-bot" "$PUB" --name "$a" --kind agent
done
```

Check a key: `OFFICE_KEY="$CFG/agents/kiro/key.pem" office whoami` prints `kiro-bot`.

### Keep the agent in the meeting: the agent runner

Claude Code, opencode and Kiro work in turns: they answer and then wait for you. In a meeting
nobody types into their chat, so they would stop listening. The **agent runner** keeps an agent
in the meeting. You start it once per agent:

```sh
office agent run --session weekly --role "You keep the minutes." -- <harness command>
```

The runner joins the meeting with the agent's key and starts the agent's program, the
**harness**, after `--`. It talks to the harness over the Agent Client Protocol (ACP) and gives it
the office tools. Each time the speaker changes, it tells the agent what the others said: who
spoke, whether a human or an agent, and what they said. The agent decides by itself whether
someone addressed it. So people talk to it in plain words, without a keyword: "Claude, what did
we decide on the budget?" or "Can the minute taker read back the last point?".

The agent speaks only through the tool `utterance_say`; what it writes in its chat reaches
nobody. When the meeting ends, the runner tells the agent and gives it time for its last work,
for example the minutes. Then it stops. Stop it earlier with Ctrl+C.

Start each agent in its own folder and terminal, with its own key. Log in to each harness once
before, as you would to use it alone.

**Claude Code**, through its ACP adapter (needs Node.js):

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

The runner prints what it does on its terminal: each delivery, each tool call and what the agent
wrote in its chat. You do not need to add the office as an MCP server in the harness: the runner
hands it over for this meeting.

**What the agent may do.** The harness asks the runner before it uses a tool. By default the
runner allows the office tools and rejects everything else, for example shell commands or edits
of files. To allow more, give patterns of the tool titles with `--allow` (`*` is any text);
`--allow '*'` allows everything. A harness that does not ask, because its own settings allow a
tool, uses its own settings.

### Give each agent a role

The role tells the agent what to do in the meeting. Write it in plain words, with `--role` or in
a file with `--role-file`. Give each agent its own role, so that they do not talk over each
other. For example:

- `minutes.md`, for Claude Code:
  > You are Claude, the minute taker. Keep the minutes with minutes_update after each topic.
  > Answer when someone asks you about what was said or decided. Otherwise stay silent.
- `tasks.md`, for opencode:
  > You are opencode, the task keeper. When someone takes on a piece of work, create a task with
  > task_create, with the person as the assignee. Speak only when someone asks you about tasks.
- `decisions.md`, for Kiro:
  > You are Kiro, the decision keeper. Record each decision with decision_record, in one clear
  > sentence. Speak only when someone asks you about decisions.

Several agents can take part in the same meeting. Each one hears the others: the runner tells it
also what the other agents said, and that they are agents.

### Optional: a decision model you bring

The runner wakes each agent at every change of speaker. With a large model, that costs time and
money, also when nobody needs the agent. You can let a small, cheap model decide first whether
the agent should act. The office ships no model; you bring one:

- a server with an OpenAI-compatible API, for example Ollama or llama.cpp on your computer, or a
  hosted service:

  ```sh
  OFFICE_KEY="$CFG/agents/kiro/key.pem" office agent run --session weekly --role-file decisions.md \
    --decide-url http://127.0.0.1:11434/v1 --decide-model <model> -- kiro-cli acp
  ```

  If the service needs a key, put it into `OFFICE_DECIDE_KEY`. The server must support
  structured output with a JSON schema.
- or your own program: `--decide-cmd "python3 decide.py"`. It gets the request as JSON on stdin
  and writes the answer as JSON on stdout. [`README.md`](README.md) describes both.

The decision model answers whether the agent should act now, whom the speaker addressed, and
whether a block of topics has ended. At the end of a block, the runner wakes the agent with the
task you give with `--topic-task`, for example "Update the minutes and record the decisions of
this block". If the model fails or takes longer than `--decide-timeout` (10 seconds), the runner
wakes the agent anyway. What did not wake the agent reaches it with the next message, so it
misses nothing.

A hosted decision model gets the text of the meeting, as the agent's own model does. Sentences
off the record are never sent. Choose where the model runs.

### Without the runner: the tools only

A tool can also use the office directly as an MCP server, without the runner. Then the agent
must keep reading new events itself, and it stops listening when its turn ends. This suits
short jobs, for example writing the minutes after a meeting. Add `office mcp` as an MCP server
in the agent's folder. In a configuration file, write absolute paths: no `~` and no `$HOME`.

```sh
# Claude Code
claude mcp add office -e OFFICE_KEY="$CFG/agents/claude/key.pem" -e OFFICE_HUB="$OFFICE_HUB" \
  -- "$(command -v office)" mcp --session weekly
# opencode (writes opencode.json in the folder)
opencode mcp add office --env OFFICE_KEY="$CFG/agents/opencode/key.pem" --env OFFICE_HUB="$OFFICE_HUB" \
  -- "$(command -v office)" mcp --session weekly
```

For Kiro, write `.kiro/settings/mcp.json` in the folder:

```sh
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

Then tell the agent what to do, for example:

> Join the meeting "weekly" (meeting_join). Keep reading new events with meeting_events, from the
> last one you read. Keep the minutes with minutes_update. Speak (utterance_say) only when someone
> addresses you as "Claude". Stop when the session ends.

The tools do not wait for new events by themselves: the agent calls `meeting_events` again and
again. If it stops in between, tell it to keep listening.

### Or use the CLI and the log file

An agent that works in a shell can read the local copy of the meeting log instead of MCP:

```sh
export OFFICE_KEY="$CFG/agents/claude/key.pem"
LOG=$(office meeting join weekly)
tail -f "$LOG" | grep '"utterance.final"\|"turn.end"'
```

`meeting join` prints the path of the log. Each line is one event as JSON. The agent acts with
the same commands as a human.

An agent that runs in a container uses the image `ghcr.io/mrsimpson/agentic-office/office`.
See "Container images" in [`README.md`](README.md).

## 5. Try everything on one computer

To get to know the office before you set up a hub, `scripts/try-local.sh` starts a hub and two
participants in containers: Ada, a human, and a bot, an agent. You need the repository
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
| `no audio recorder found` | Install `ffmpeg` (macOS) or `pulseaudio-utils` (Linux), or set `OFFICE_MIC_COMMAND` (see [`README.md`](README.md)). |
| The model download fails | Check your network, or download the models elsewhere and set `OFFICE_MODEL_URL` to a mirror. |
| `SIGILL: illegal instruction` | A client built on another computer uses CPU features yours lacks. Use the pre-built client, or build with `WHISPER_NATIVE=OFF`. |
| `office agent: … the harness stopped` | The harness ended. Run its command alone (for example `opencode acp`) and read its error; often it needs a login first. |
| The agent never speaks | Read the runner's terminal: does it send the turns (`sending … utterances`), and does the harness ask for tools that the runner rejects (`permission rejected`)? Allow them with `--allow`, or in the harness's own settings. Check that someone's client ends the turn (`turn.end`): the ears do it after a pause; with typed text, use `office signal turn-end`. |
| `decision model failed` | The runner woke the agent anyway. Check `--decide-url`, `--decide-model` and `OFFICE_DECIDE_KEY`, and that the server supports structured output with a JSON schema. |
| `the trial is running already` | Run `scripts/try-local.sh down`, then `up` again. |
| `Apple's container needs a DNS domain` | Do the one-time setup in [Try everything on one computer](#5-try-everything-on-one-computer). |
