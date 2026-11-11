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

Run the hub as a binary (on Linux or macOS) or as a container with Docker or Podman. **On a Mac,
use the binary.** It runs directly on macOS, without a virtual machine. Apple's `container`
is not a good base for a hub yet: in our tests its service stopped answering, and `container exec`
and `container system stop` hung (see [Troubleshooting](#6-troubleshooting)).

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

With Docker or Podman (`podman` instead of `docker`):

```sh
H=ghcr.io/mrsimpson/agentic-office/office-hub
docker volume create office-hub
docker run --rm -v office-hub:/data $H keygen /data/signing.key
docker run --rm -v office-hub:/data --entrypoint sh $H -c 'echo "ada <your public key> Ada" > /data/admins'
docker run -d --name office-hub --restart unless-stopped -p 8740:8740 -v office-hub:/data $H \
  -data-dir /data -addr 0.0.0.0:8740 -signing-key /data/signing.key \
  -tls-hosts hub.example.org -public-url https://hub.example.org:8740
docker cp office-hub:/data/tls.crt hub.crt
```

The last command copies the certificate for the participants. `--restart unless-stopped` starts
the hub again after a restart of the server. When you pass flags to the image, pass all of them,
as in the example.

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
part in the same meeting, for example Claude Code, opencode and Kiro next to each other. Each
agent needs:

- its **own participant ID and key**, so that everyone sees who said or recorded what;
- its **own folder**, where its tool keeps the connection to the office;
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

### Connect the agent

Each tool starts `office mcp` as its MCP server. The examples use the meeting `weekly`; the
agent can still join another one. In a configuration file, write absolute paths: no `~` and no
`$HOME`. The `cat <<EOF` in the Kiro example fills them in for you.

**Claude Code**

```sh
mkdir -p ~/office-agent-claude && cd ~/office-agent-claude
claude mcp add office -e OFFICE_KEY="$CFG/agents/claude/key.pem" -e OFFICE_HUB="$OFFICE_HUB" \
  -- "$(command -v office)" mcp --session weekly
claude
```

**opencode**

```sh
mkdir -p ~/office-agent-opencode && cd ~/office-agent-opencode
opencode mcp add office --env OFFICE_KEY="$CFG/agents/opencode/key.pem" --env OFFICE_HUB="$OFFICE_HUB" \
  -- "$(command -v office)" mcp --session weekly
opencode mcp list
opencode
```

`opencode mcp add` writes the server into `opencode.json` in the folder. Inside opencode,
`/mcps` shows whether it is connected.

**Kiro**

```sh
mkdir -p ~/office-agent-kiro/.kiro/settings && cd ~/office-agent-kiro
cat > .kiro/settings/mcp.json <<EOF
{
  "mcpServers": {
    "office": {
      "command": "$(command -v office)",
      "args": ["mcp", "--session", "weekly"],
      "env": {
        "OFFICE_KEY": "$CFG/agents/kiro/key.pem",
        "OFFICE_HUB": "$OFFICE_HUB"
      },
      "autoApprove": ["meeting_events", "meeting_log", "whoami"]
    }
  }
}
EOF
kiro-cli mcp list workspace
kiro-cli chat
```

In the Kiro IDE, open the folder `~/office-agent-kiro` instead. `autoApprove` lets the tools that
only read run without a question each time. Tools that write, such as `utterance_say`, still ask;
add them to the list, or use `"*"`, to let the agent act alone.

### Tell the agent what to do

Give each agent its own task, so that they do not talk over each other. For example:

- **Claude Code**, the minute taker:
  > Join the meeting "weekly" (meeting_join). Keep reading new events with meeting_events, from
  > the last one you read. Keep the minutes with minutes_update. Speak (utterance_say) only when
  > someone addresses you as "Claude". Stop when the session ends.
- **opencode**, the task keeper:
  > Join the meeting "weekly". Keep reading new events with meeting_events. When someone takes
  > on a piece of work, create a task with task_create. Speak only when someone addresses you as
  > "opencode". Stop when the session ends.
- **Kiro**, the decision keeper:
  > Join the meeting "weekly". Keep reading new events with meeting_events. Record each decision
  > with decision_record. Speak only when someone addresses you as "Kiro". Stop when the session
  > ends.

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
| `no audio recorder found` | Install `ffmpeg` (macOS) or `pulseaudio-utils` (Linux), or set `OFFICE_MIC_COMMAND` (see [`README.md`](README.md)). |
| The model download fails | Check your network, or download the models elsewhere and set `OFFICE_MODEL_URL` to a mirror. |
| `SIGILL: illegal instruction` | A client built on another computer uses CPU features yours lacks. Use the pre-built client, or build with `WHISPER_NATIVE=OFF`. |
| `the trial is running already` | Run `scripts/try-local.sh down`, then `up` again. |
| `Apple's container needs a DNS domain` | Do the one-time setup in [Try everything on one computer](#5-try-everything-on-one-computer). |
| Apple's `container` hangs: `container exec`, `logs` or `system stop` do not return, or the port takes connections but answers nothing | Stop the `container` services with launchd, then start them again: `launchctl list \| awk '/com\.apple\.container\./ {print $3}' \| while read s; do launchctl bootout "gui/$(id -u)/$s"; done`, then `container system start`. If that does not help, restart the Mac. |
| `bind: address already in use` for port 8740 | Another hub runs, for example `office-trial-hub` from `scripts/try-local.sh`. Find it with `lsof -i :8740`; remove the trial with `scripts/try-local.sh down`. |
