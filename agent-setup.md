# Set up the agentic office for a human and their agents

Purpose: you set up the office client for one human and for the agents they name, once, and you
connect them to a hub. Follow the steps in order. Run each check before the next step. Details are
in the [user guide](https://mrsimpson.github.io/agentic-office/user-guide/) and the [command reference](https://mrsimpson.github.io/agentic-office/reference/).
Every command explains itself: `office help`, `office help <command>`, `office-hub -h`.

## Safety rules

- Never put a secret in the chat or in a file that is checked in. Never print or send a private key
  (`key.pem`). Public keys, key IDs and fingerprints are not secret.
- Ask the human before you install software, write outside the folders named here, or change a hub.
- Never confirm a certificate fingerprint on your own. The human compares it.
- When a check fails, stop, read the message, fix the cause and run the check again. Do not go on.

## 0. Ask the human first

Do not guess. Ask all of these in one message, then wait:

1. The hub address (for example `https://hub.example.org:8740`) and the tenant.
2. The name for the human in the office (for example `ada`, "Ada Lovelace"), and the meeting name.
3. The language they speak: German or English. The default of the client is German.
4. Microphone, or text only? With a microphone, the human runs the meeting command in their own terminal.
5. Which agents join, with a name and a harness each: Claude Code, OpenCode or Kiro.
6. Does the human also run the hub, or does an admin register the keys?
7. Where may the programs go? Suggest `~/.local/bin`.

## 1. Get the client

```sh
os=$(uname -s | tr A-Z a-z); arch=$(uname -m)
case "$os-$arch" in
  darwin-arm64) p=darwin-arm64;; linux-x86_64) p=linux-amd64;; linux-aarch64|linux-arm64) p=linux-arm64;;
  *) echo "no release for $os-$arch"; false;;
esac
mkdir -p ~/.local/bin && cd ~/.local/bin
base=https://github.com/mrsimpson/agentic-office/releases/latest/download
curl -fsSLO $base/office-$p.tar.gz && curl -fsSLO $base/office-$p.tar.gz.sha256
shasum -a 256 -c office-$p.tar.gz.sha256 || sha256sum -c office-$p.tar.gz.sha256
tar -xzf office-$p.tar.gz && rm office-$p.tar.gz office-$p.tar.gz.sha256
export PATH="$HOME/.local/bin:$PATH"
office version
```

- Intel Macs and Windows have no release. On Windows, use WSL 2 and the Linux archive. Otherwise
  build from source: see [Get the software](https://mrsimpson.github.io/agentic-office/user-guide/get-the-software/).
- Check: `office version` prints a version. If the checksum fails, delete the files and stop.
  On macOS, `xattr -d com.apple.quarantine ~/.local/bin/office` helps only if a browser downloaded it.
- Ask before you add `~/.local/bin` to the `PATH` in a shell profile.
- With a microphone, check the recorder: `command -v ffmpeg` on macOS, `command -v parec` on
  Linux. If it is missing, ask before you install it (`brew install ffmpeg`, or the package
  `pulseaudio-utils`). Without a microphone, skip this.

## 2. Connect to the hub

```sh
office hub add main <hub address> --tenant <tenant> --default < /dev/null
```

- The command prints the SHA-256 fingerprint of the hub certificate and writes nothing (the input
  is empty, so it does not confirm).
- Show the fingerprint to the human. Ask them to compare it with the log of the hub or with the
  message of the operator. Wait for their answer. Then confirm it with their value:

  ```sh
  office hub add main <hub address> --tenant <tenant> --default --fingerprint <fingerprint>
  ```

- Check: `office hub list` shows `main` with a star. If the fingerprint does not match, stop and
  tell the human: they may reach another hub. A hub with a certificate from a public authority
  needs no fingerprint.

## 3. Create the keys

```sh
CFG="$([ "$(uname)" = Darwin ] && echo "$HOME/Library/Application Support" || echo "${XDG_CONFIG_HOME:-$HOME/.config}")/agentic-office"
office key create
office key create --key "$CFG/agents/<agent>/key.pem"      # once per agent
```

- Each command prints a public key and a key ID. The private key stays in the file; do not print
  it. `office key show` and `office key show --key <file>` print the public key again.
- Check: the files `$CFG/client/key.pem` and `$CFG/agents/<agent>/key.pem` exist. An existing key
  is never changed.

## 4. Register the keys

Registering needs an admin. Registering a user and adding it to a tenant are two steps.

- **An admin does it:** send the admin the list: user ID, name, kind (`human` or `agent`) and public
  key of each participant. Wait until they confirm. Send no private key.
- **The human is the system admin:** use the admin key of the human, not the key of the
  participants. A system admin is never a member of a tenant, so the human works as another user.

  ```sh
  export ADMIN_KEY=<the admin key file of the human>
  OFFICE_KEY=$ADMIN_KEY office admin user create <human id> <public key> --name "<name>" --kind human
  OFFICE_KEY=$ADMIN_KEY office admin tenant create <tenant> <human id> --name "<tenant name>"
  OFFICE_KEY=$ADMIN_KEY office admin user create <agent id> <public key> --name "<name>" --kind agent
  OFFICE_KEY=$ADMIN_KEY office admin member set <tenant> <agent id>
  ```

  Leave out `tenant create` if the tenant exists, and add the human with `admin member set`.
- **The human runs a new hub:** the hub starts with `-first-system-admin`; see `office-hub -h`
  and [Run a hub](https://mrsimpson.github.io/agentic-office/user-guide/run-a-hub/).
- Check: `office whoami` prints the ID of the human, and for an agent
  `OFFICE_KEY="$CFG/agents/<agent>/key.pem" office whoami` prints its ID. A message
  `public key is not registered` means the admin has not finished: wait, do not retry in a loop.

## 5. Models and vocabulary

Skip this for text only. Tell the human the size first: the models are 0.5 to 1.6 GB.

```sh
office models list --language english
office models download --language english
office vocabulary extract <folder of the project documents> --no-model
```

- Use `--language german` for German. Add `--fast` if there is no GPU: `models list` says which
  model is used.
- `vocabulary extract` writes `.office/vocabulary.txt` in the current folder. Without `--no-model`
  it also finds names, but downloads a model of 540 MB. Ask the human to read the file.
- Check: `office models list` shows the chosen model as downloaded.

## 6. The first meeting

```sh
LOG=$(office meeting join <meeting>)
office say "Hello, this is a test."
office meeting leave
```

- Check: `grep utterance.final "$LOG"` shows your sentence. If `join` fails,
  run `office config show`: it names the hub, the tenant and the key that the client uses.
- With a microphone, give the human this command for their own terminal, and ask them to say
  one sentence: `office meeting join <meeting> --mic --language english`. Keys: `p` pause,
  `o` off the record, `q` leave. Use a headset.

## 7. The agents of the human

Each agent has its own key (step 3), its own folder and its own role.

- **With the agent runner** (the agent stays in the meeting). Create the folder
  `~/office-agents/<agent>/` with a role file and `.office/config.yaml`:

  ```yaml
  key: <absolute path of the agent's key.pem>
  agent:
    session: <meeting>
    harness: [opencode, acp]       # Claude Code: [npx, -y, "@agentclientprotocol/claude-agent-acp"]; Kiro: [kiro-cli, acp]
    role-file: role.md
  ```

  Write `role.md` in plain words, for example "You keep the minutes. Speak only when asked."
  Give each agent a different role. Then start it from that folder, in its own terminal:

  ```sh
  cd ~/office-agents/<agent> && office agent run
  ```

  Log in to the harness first; the runner does not. Check: the runner prints `joined <meeting>`.
  Only the human may allow more than the office tools (`--allow`).
- **With MCP only** (short jobs, no waiting for the meeting). Run in the agent's folder:

  ```sh
  claude mcp add office -e OFFICE_KEY="$CFG/agents/<agent>/key.pem" -- "$(command -v office)" mcp --session <meeting>
  ```

  OpenCode and Kiro: see [Without the runner](https://mrsimpson.github.io/agentic-office/user-guide/bring-in-an-agent/#without-the-runner-the-tools-only).
- Keys never go into the role file or the config file, only paths. API keys of a decision model go
  in an environment variable named by `key-env`; see `office help config`.

## 8. Final check

1. `office whoami` prints the ID of the human, and each agent key prints the ID of the agent.
2. Start the agents (step 7), join as the human (step 6) and say "Hello, <agent>". The agent
   answers with `utterance_say`, so its sentence appears in `$LOG`.
3. `office stage link --session <meeting>` prints a read-only link for the stage. Give it to the
   human. It shows the meeting, the minutes and the documents, and works for 24 hours.
4. End the test: `office meeting end`. The runners stop by themselves.

## What to tell the human

Tell them in a few lines: what you installed and where; the hub profile `main` and the tenant;
the user IDs; where the keys are (paths, never the keys); how to start an agent
(`cd ~/office-agents/<agent> && office agent run`); how to join (`office meeting join <meeting>`,
with `--mic` for speech); the stage link. Name anything you did not finish, for example a key
that waits for the admin.

More: [user guide](https://mrsimpson.github.io/agentic-office/user-guide/), [troubleshooting](https://mrsimpson.github.io/agentic-office/user-guide/troubleshooting/),
[commands](https://mrsimpson.github.io/agentic-office/reference/).
