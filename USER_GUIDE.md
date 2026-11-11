# User guide

This guide is for people who want to try the agentic office on their own computer. In a few
minutes you start a hub and two participants: Ada, a human, and a bot, an agent. You then watch
the meeting on the stage in your browser. After that you can speak into the meeting with your
headset, or connect your own agent.

You do not need to build anything for the first part. The script uses the published container
images. To build from source, see [`README.md`](README.md).

Contents:

1. [Try it in containers](#1-try-it-in-containers) (Linux, macOS, Windows)
2. [Speak with your headset](#2-speak-with-your-headset) (Linux, macOS)
3. [Connect your agent](#3-connect-your-agent)
4. [Troubleshooting](#4-troubleshooting)

## 1. Try it in containers

### What you need

| Platform | Container runtime | Notes |
|---|---|---|
| Linux | Docker or Podman | |
| macOS | Docker Desktop, Podman, or Apple's [`container`](https://github.com/apple/container) | `container` needs Apple silicon and macOS 26, plus a one-time DNS setup (see below) |
| Windows | Docker Desktop with WSL 2 | Run the script in a WSL 2 shell (for example Ubuntu), not in PowerShell |

You also need `bash` and `git`. The images exist for `linux/amd64` and `linux/arm64`. Your
runtime picks the right one.

Get the script:

```sh
git clone https://github.com/mrsimpson/agentic-office.git
cd agentic-office
```

### Apple `container`: set up once

Containers find the hub by its DNS name. Apple's `container` gives each container a name only if
you set a DNS domain. Do this once:

1. Start the service, if it is not running: `container system start`.
2. Add these lines to `~/.config/container/config.toml`:

   ```toml
   [dns]
   domain = "test"
   ```

3. Restart the service:

   ```sh
   container system stop
   container system start
   ```

You do not need `sudo container system dns create`. Your Mac reaches the hub through
`localhost`, not through the DNS name.

The script uses Docker if it finds it, then Podman, then `container`. If you have more than one,
choose one with `OFFICE_RUNTIME`:

```sh
export OFFICE_RUNTIME=container
```

### Start

```sh
scripts/try-local.sh up
```

The script:

1. starts a container for Ada and creates her key;
2. creates the signing key of the hub and makes Ada its first admin;
3. starts the hub on `https://localhost:8740`;
4. starts a container for the bot and registers its key as an agent;
5. prints the stage link and the next steps.

Open the stage link in your browser. The hub uses a self-signed certificate, so the browser
shows a warning. Accept it for this page.

### Hold a meeting

Each participant runs the `office` command in its own container. `scripts/try-local.sh as <name>`
runs it for you:

```sh
scripts/try-local.sh as bot meeting join weekly     # the agent joins first and keeps a local log
scripts/try-local.sh as ada meeting join weekly
scripts/try-local.sh as ada say "Good morning, everyone"
scripts/try-local.sh as bot hand raise --topic "Budget"
scripts/try-local.sh tail bot                       # what the agent reads; stop with Ctrl+C
```

Watch the stage while you do this. Some more things to try:

```sh
scripts/try-local.sh as ada utterance off-the-record --last
scripts/try-local.sh as bot decision record "We meet again on Friday."
scripts/try-local.sh as bot task create "Send the numbers" --assignee ada
scripts/try-local.sh as ada meeting end
```

`scripts/try-local.sh as ada --help` lists every command. [`README.md`](README.md) describes
them in detail.

More participants:

```sh
scripts/try-local.sh add grace human
scripts/try-local.sh add claude agent
```

### Remove everything

```sh
scripts/try-local.sh down
```

This removes the containers, the data of the hub and the folder `office-trial`. The keys of the
participants live in their containers, so they are gone too.

### Settings

| Variable | Default | Meaning |
|---|---|---|
| `OFFICE_RUNTIME` | the first of `docker`, `podman`, `container` | Container runtime |
| `OFFICE_VERSION` | `latest` | Image tag, for example `0.0.2` |
| `OFFICE_PORT` | `8740` | Port of the hub on your computer |
| `OFFICE_TRIAL_DIR` | `./office-trial` | Where the script puts the certificate of the hub |

## 2. Speak with your headset

The containers have no microphone. To speak into the meeting, run the `office` client directly
on your computer and join the hub from part 1. The client transcribes your speech locally with
Whisper. Only text leaves your computer.

Use a headset. The client has no echo cancellation.

### Get the client

**Download it** (no build needed):

| Computer | File |
|---|---|
| macOS, Apple silicon | `office-darwin-arm64.tar.gz` |
| Linux, x86-64 | `office-linux-amd64.tar.gz` |
| Linux, ARM64 | `office-linux-arm64.tar.gz` |

```sh
mkdir -p bin
curl -fsSL https://github.com/mrsimpson/agentic-office/releases/latest/download/office-darwin-arm64.tar.gz | tar -xz -C bin
bin/office version
```

The client needs a program that records the microphone: on macOS `ffmpeg`
(`brew install ffmpeg`), on Linux `parec` (package `pulseaudio-utils`). If you download the file
with a browser on macOS, run `xattr -d com.apple.quarantine bin/office` once.

**Or build it** from source. macOS:

```sh
xcode-select --install                 # compiler, once
brew install go cmake ffmpeg           # ffmpeg records the microphone
make third_party/whisper.cpp whisper build
```

Linux (Debian or Ubuntu):

```sh
sudo apt install cmake g++ make git pulseaudio-utils
make third_party/whisper.cpp whisper build
```

You also need Go 1.27 or newer. If your distribution has an older one, install Go from
[go.dev/dl](https://go.dev/dl/). The client is then in `bin/office`.

Windows: the native client is not supported. Use the containers, or a Linux computer.

**Speech models.** The first join with `--mic` or `--wav` downloads the Whisper model and the
voice detection model once, about 490 MB, and checks them. To do this ahead of time, run
`bin/office models download`.

### Join the meeting

```sh
mkdir -p ~/.config/agentic-office/client
cp office-trial/hub.crt ~/.config/agentic-office/client/hub.crt
export OFFICE_HUB=https://localhost:8740

bin/office key create                              # prints your public key
scripts/try-local.sh register me <public-key> human
bin/office meeting join weekly --mic
```

The terminal shows each sentence and its status. Press `p` to pause and `q` to quit. On macOS,
the system asks once whether the terminal may use the microphone.

To try it without a microphone, play a recording instead:
`bin/office meeting join weekly --wav tests/e2e/testdata/german-3-sentences.wav`.

## 3. Connect your agent

Your agent uses the office through the `office mcp` server. It is the same `office` command
that you used in part 1. The example uses Claude Code; other MCP clients work the same way.

**With a participant container** (no build needed):

```sh
scripts/try-local.sh add claude agent
claude mcp add office -- docker exec -i office-trial-claude office mcp --session weekly
```

With Podman or Apple's `container`, write `podman exec -i` or `container exec -i` instead of
`docker exec -i`.

**With the native client** from part 2:

```sh
bin/office key create --key ~/.config/agentic-office/agent/key.pem
scripts/try-local.sh register claude <public-key> agent
claude mcp add office -e OFFICE_KEY=$HOME/.config/agentic-office/agent/key.pem \
  -e OFFICE_HUB=https://localhost:8740 -- $PWD/bin/office mcp --session weekly
```

Then ask your agent, for example: "Join the meeting weekly, follow it and keep the minutes."
The agent joins with `meeting_join`. It reads new events with `meeting_events`, and it acts with
tools such as `utterance_say`, `hand_raise` and `minutes_update`.

## 4. Troubleshooting

| Problem | What to do |
|---|---|
| `the trial is running already` | Run `scripts/try-local.sh down`, then `up` again. |
| `the hub did not start` | The script prints the log of the hub. Is port 8740 in use? Choose another port with `OFFICE_PORT=8750`. |
| `Apple's container needs a DNS domain` | Do the one-time setup in [Apple `container`: set up once](#apple-container-set-up-once). |
| A participant cannot reach the hub | The hub name inside the containers must match its certificate. Run `down` and `up` again after you change the runtime or the DNS domain. |
| The browser does not open the stage | Use the link from `scripts/try-local.sh stage` and accept the certificate warning. |
| `no audio recorder found` | Install `ffmpeg` (macOS) or `pulseaudio-utils` (Linux), or set `OFFICE_MIC_COMMAND` (see [`README.md`](README.md)). |

To run a hub for your team on a server, see "Container images" and "Hub setup" in
[`README.md`](README.md).
