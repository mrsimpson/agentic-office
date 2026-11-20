# User guide

This guide shows how a team uses the agentic office: how to get the software, run a hub, join
meetings as a human with a headset, and bring in agents.

The commands explain themselves. `office help` lists them, `office <group> --help` lists the
commands of one group (for example `office admin --help`), `office <command> --help` shows the
purpose, the flags with their defaults and an example, and `office help <topic>` explains a topic
(`config`, `json`, `import-format`, `deck-format`, `ears`). `office-hub -h` does the same for the
hub. This guide does not repeat flag tables: it shows what to do, and in which order. Developers
find how to build and test the office in [`README.md`](README.md); the architecture is in
[`docs/architecture/`](docs/architecture/).

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
your computer, and can stop with `SIGILL` (illegal instruction) on another CPU. To copy the client
to other computers, build it with `make whisper build WHISPER_NATIVE=OFF`: it needs x86-64 with
AVX2, or any ARMv8 CPU. The release binaries and the container images are built this way.

### Container images

Every release tag (`v1.2.3`) publishes two images for `linux/amd64` and `linux/arm64`:

| Image | Content |
|---|---|
| `ghcr.io/mrsimpson/agentic-office/office-hub` | The hub. Data, keys and the archive live in the volume `/data` |
| `ghcr.io/mrsimpson/agentic-office/office` | The client CLI and MCP server, for agents that run in containers |

The tags are the version (`1.2.3`), the minor version (`1.2`), `latest` and `sha-<commit>`. You can
build them yourself with `docker build --target hub .` or `docker build --target office .`.

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
  `-tls-hosts`, in `tls.crt`. A start that fails writes nothing, so you can fix the flags and
  start again. If a later start names a host in `-tls-hosts` that the certificate misses, the hub
  does not start: delete `tls.crt` and `tls.key` to get a new certificate, and send the new
  fingerprint to the participants. To use your own certificate, see
  [Use your own certificate](#use-your-own-certificate).
- `office-hub -h` lists every flag with its default and says what the hub keeps in its data
  directory: the certificate, the hub repository with the users, tenants and memberships, and one
  Git repository for each tenant. The hub needs the `git` command line tool.
- Participants can push changes to the artifacts with Git (see
  [Work on files in a local directory](#work-on-files-in-a-local-directory)). Three options set the
  size limits of a push. A push over a limit is refused:

  | Option | Default | Limit |
  |---|---|---|
  | `-git-max-push-mib` | 32 | Size of one push, in MiB |
  | `-git-max-file-kib` | 1024 | Size of one file, in KiB |
  | `-git-max-files` | 500 | Number of files that one push changes |

- The hub needs the `git` command line tool in its `PATH`. Git runs a hook program to check every
  push. This program is the hub binary itself: at start, the hub writes a small script that calls
  the binary at its real path. Do not move or delete the binary while the hub runs, and restart
  the hub after you replace it.
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

The last command copies the certificate; the participants usually do not need the file (see below). `--restart unless-stopped` starts
the hub again after a restart of the server. When you pass flags to the image, pass all of them,
as in the example (the default command is `-data-dir /data -addr 0.0.0.0:8740 -signing-key
/data/signing.key`).

**Health check.** For a container platform (Kubernetes, a load balancer), the hub answers
`GET /healthz` with `200` and `ok` on its TLS port. It needs no login and reads no data. The
platform has to accept the self-signed certificate: the Kubernetes probe `httpGet` with
`scheme: HTTPS` does.

### The signing key

The hub signs the tree heads of the meeting logs. A tree head is a short proof that a log has not
changed. You give the hub the signing key at every start with `-signing-key FILE`. The hub never
creates a signing key by itself, and it does not start without the flag, or if the file is
missing, broken or readable by others.

- `office-hub keygen FILE` creates the file once. It writes the private key with mode 0600, prints
  the public key and refuses to overwrite an existing file.
- The hub logs the public key at start: `signing key of tree heads …`.
- Keep the file with your other secrets and back it up. Start the hub with the same file every
  time. If you replace it, tree heads that were signed with the old key no longer verify.
- How clients pin the public key of the hub is still open (issue #41).

### Use your own certificate

With a certificate from your own authority or from Let's Encrypt, clients need no stored
certificate. Start the hub with the files and its public address:

```sh
office-hub -tls-cert /etc/office/fullchain.pem -tls-key /etc/office/privkey.pem \
  -public-url https://office.example.org:8740 -signing-key … -addr 0.0.0.0:8740
```

- The certificate file is PEM and may hold the whole chain. The key file is PEM (PKCS #8, PKCS #1
  or SEC 1). Or put both into the data directory as `tls.crt` and `tls.key`: the hub never
  overwrites existing files.
- Participants then trust the system roots: they use `office hub add` without a stored
  certificate (see [Set up once](#set-up-once)), or give no certificate file.
- `-public-url` is the address in stage links. It defaults to `https://<listen address>`.
- The hub reads the certificate at start. Restart it after you renew the certificate.

The self-signed certificate of the hub is valid for 820 days, because macOS and the browsers on
it reject server certificates that are valid for more than 825 days. The hub warns at start when
its certificate is valid for longer. Delete `tls.crt` and `tls.key` and restart to get a new one.
A client with a certificate file trusts only the certificates in that file, not the system roots.

### When the hub restarts

Stop the hub with SIGINT or SIGTERM (Ctrl+C, `systemctl stop`, `docker stop`). It then writes every
changed session to the archive once more. The hub writes the sessions that changed every 30
seconds while they run (`-archive-interval`), and at once when a session ends.

- **After a clean stop** the hub starts again without a wait. Sessions that have not ended are
  back with all their events.
- **After a crash** the events after the last write are lost on the hub, but the participants have
  them, and their clients fill them in by themselves. Until a participant has done so, for at
  most `-restore-wait` (10 seconds), commands of a session that has not ended fail with
  `unavailable`. Repeat the command; nothing is lost or stored twice.
- Users, keys, tenants and memberships are files in the hub repository and survive. Access
  tokens do not: clients sign in again.
- Releases survive too (see [Release what a session created](#release-what-a-session-created)):
  a session whose grace period ended while the hub was down is released at start, and a release
  that the stop cut off is completed, once.
- A known gap (issue #47): a session that the hub never wrote to the archive is unknown after a
  crash until a participant offers a whole copy of its log.

### Call the hub directly

Every interface of the hub is a Protobuf contract served with Connect RPC, which is plain HTTP
with JSON. You can call any command with `curl`, for example to test the hub from a script:

```sh
curl --cacert "$CFG/hub/tls.crt" \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" -H 'Office-Tenant: acme' \
  -d '{"sessionId":"demo","commandId":"…"}' \
  https://hub.example.org:8740/office.sessioncommands.v1.SessionCommandsService/Join
```

- Every call carries an access token (`Authorization: Bearer`), except `GetChallenge` and
  `ExchangeChallenge`, which give you one: sign the challenge with your key. A token lives 15
  minutes (`-token-ttl`). The Go package `hub/identity/api/auth` does this for you.
- Every call about meetings, records, tasks, slides and the stage names its tenant in the header
  `Office-Tenant`.
- Every command carries a `command_id` that the client creates, a UUIDv7. A repeated command
  returns the stored result with `duplicate` set.
- Every event has an `id` that grows by one inside a session, and a `type` in dotted form.
- The contracts and their rules live with the part of the hub that provides them: the `.proto`
  files and the package comments below `hub/`. [`README.md`](README.md) says where to find them.

### Give the participants the fingerprint

With the self-signed certificate, each participant checks it once with `office hub add` (see
[Set up once](#set-up-once)). Send them the address of the hub, the tenant and the SHA-256
fingerprint from the log of the hub (`docker logs office-hub` or the output of the hub). They
compare it with the one that `office hub add` shows. They need no file. With a certificate from a
public CA, they need nothing but the address. If a participant has no way to reach the hub when
setting up, send the file `tls.crt` instead; it holds no secret. They store it as the `ca-cert` of
a profile.

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

Who may do what: a **system admin** creates, lists and removes users and tenants, names system
admins and manages the members of any tenant. A **tenant admin** manages the members of the own
tenant (`admin member set` and `remove`); a member lists them (`admin member list`). Everything
else fails with `permission_denied`. A tenant that you are not a member of looks as if it did not
exist (`not_found`), even to a system admin. Every call about meetings, records, tasks and slides
names its tenant: the client sends the tenant of the hub profile, of `--tenant` or of
`OFFICE_TENANT`.

`office admin member set <tenant> <user> --role tenant-admin` makes a member a tenant admin.
`office admin member remove <tenant> <user>` ends a membership, and `office admin member list <tenant>`
lists the members. `office admin tenant list` and `office admin tenant remove <tenant>` list and end
tenants, `office admin user list` and `office admin user remove <user>` do the same for users.
Removing ends the validity and keeps the record, so an ID is never used again. A tenant always
keeps at least one tenant admin, and the hub at least one system admin.

`office admin key add <user> <public key>` adds a further key to a user. `office admin key revoke <key-id>` revokes a key.
The last key of a user cannot be revoked.

Every user has a web portal in the browser. It shows what your key may see: your tenants, the
sessions of a tenant in which you took part (a tenant admin sees all of them), the members of a
tenant to its tenant admins, and the administration to system admins. There a system
admin lists all users and keys, creates a user or adds a key with a form, and revokes a key after a
confirmation, and sees the tenants and the system admins, read only. Tenants and memberships stay
with the commands above:

```sh
office portal link                # opens the tenant you work in
office portal link --tenant ""    # opens the start page, for example as a system admin
```

The command prints a link. Open it in a browser within a few minutes: the link works once. The
portal then stays open for one hour. After that, or if you see `Portal link not valid` or `Session
ended`, run `office portal link` again. A system admin is no member of a tenant, so it opens the
start page.

## 3. Join a meeting

### Set up once

1. Get the client (see [Get the software](#1-get-the-software)). To speak, you also need a
   program that records the microphone: `ffmpeg` on macOS (`brew install ffmpeg`), `parec` on
   Linux (package `pulseaudio-utils`).
2. Add the hub as a **hub profile**. A profile has a name, the address of the hub, your tenant and
   the certificate of the hub. Ask your admin for the address, the tenant and the fingerprint of the
   certificate (the hub logs it at its start):

   ```sh
   office hub add main https://hub.example.org:8740 --tenant acme --default
   ```

   The command connects to the hub and shows the **SHA-256 fingerprint** of its certificate.
   Compare it with the fingerprint from your admin or from the log of the hub. Only if they are
   the same, answer `y`. The client then stores the certificate and writes the profile to
   `config.yaml` in your configuration folder (`$CFG/client` below). `--default` makes it the
   profile that the client uses when you name none. In a script, pass the fingerprint instead of
   answering: `--fingerprint <sha256>`. A hub with a certificate from a public certificate
   authority needs no stored certificate and no question.

3. Create your key and send the public key to your admin:

   ```sh
   office key create
   ```

4. After the admin has registered you and added you to the tenant, check it: `office whoami` prints
   your participant ID.

Your private key stays in `$CFG/client/key.pem`. It never leaves your computer. `$CFG` is the
folder of the client on your computer:

```sh
CFG="$([ "$(uname)" = Darwin ] && echo "$HOME/Library/Application Support" || echo "${XDG_CONFIG_HOME:-$HOME/.config}")/agentic-office"
```

### Work with several hubs

Add one profile per hub, with its own tenant:

```sh
office hub add lab https://lab.example.org:8740 --tenant research
office hub list                      # the profiles; the active one has a star
```

Choose the hub for one command with `--hub-profile`, or for a shell with a variable:

```sh
office whoami --hub-profile lab
export OFFICE_HUB_PROFILE=lab        # every command in this shell uses the hub "lab"
```

A project can choose its hub too. Put the name into `.office/config.yaml` in the folder where you
start the commands, and check the file in:

```yaml
hub-profile: lab
```

The order is: the default profile in your `config.yaml`, then the project file, then
`OFFICE_HUB_PROFILE`, then `--hub-profile`; the last one wins. `--hub`, `--tenant`, `--ca-cert`
and `--key` still override single values of the profile, for example `--tenant other` if you are a
member of several tenants. `office config show` shows the active profile and where each value
comes from. `office hub remove lab` removes a profile and its stored certificate. Profiles never
hold a secret; the private key stays a file. A profile can name another key file with `key:` in
`config.yaml`.

A profile is a whole hub. It sets the hub, the tenant and the certificate: a value that the
profile leaves out is empty (no tenant, the system roots). The key is the exception: a profile
without a `key` uses the key that is set without a profile. If a profile of the user file and one
of the project file have the same name, they merge key by key.

### Settings in files

You can keep settings in two YAML files instead of flags and variables: your **user file**,
`config.yaml` in `$CFG/client` next to your key, for what holds for all your projects; and the
**project file**, `.office/config.yaml` in the folder where you start the commands, for what
belongs to the project, which you can check in. Each source overrides the one before it, key by
key: the defaults, the user file, the project file, the environment variables, the flags.

- The files never hold a secret. A key is the path of a key file, never the key itself.
- The files are strict. A typo stops the command with exit code 2 and names the file, the line and
  the key, for example `configuration file /work/.office/config.yaml, line 4: unknown key
  agent.wake.api-key`.
- A relative path in the project file is relative to the folder where you start the command; in
  the user file, relative to `$CFG/client`.
- `office config show` prints what the client will use and where each value comes from.

`office help config` lists all flags, variables, keys and rules. The options of the agent runner
are in the section `agent` of the same files (see
[Start an agent with a configuration file](#start-an-agent-with-a-configuration-file)).

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
You can also set `OFFICE_WHISPER_MODEL`, a name or a file; `--model` and `--fast` win over it. Use a
multilingual model, not a `.en` model: those only know English. `--language auto` lets Whisper
detect the language.

The models are stored in `~/.local/share/agentic-office/models/` (or
`$XDG_DATA_HOME/agentic-office/models/`). The client checks the SHA-256 of each download. If your
network blocks the download, fetch the files elsewhere and set `OFFICE_MODEL_URL` to a mirror that
holds them; `office help models download` names the files. `make models` fetches `small` and the
VAD model for developers.

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
- A sentence ends after a short silence (500 ms). When you stay silent for longer after your last
  sentence (1.8 seconds), the ears end your turn: agents that wait for the end of a turn wake up
  then. While you are paused, nothing is transcribed and no turn ends.
- When the network drops, the ears keep transcribing and send the sentences in order when the
  network is back. Sentences that are still waiting when you quit are lost; the client says how
  many.
- `--wav FILE` plays a WAV file in real time instead of the microphone. This is how you try the
  ears without a headset.
- Before you join, the client checks the models, the WAV file and the recorder. If something is
  missing, the join fails and you do not join. The client needs a recorder program that runs as
  your user; it tries `parec`, `arecord`, `rec` and, on macOS, `ffmpeg`. To use another one, set
  `OFFICE_MIC_COMMAND`.

`office help ears` lists every flag and variable of the ears.

Without a microphone, join with `office meeting join weekly` and write instead of speaking:
`office say "I agree"`. `office meeting view` shows your own sentences with their status.

### Read the log and act from a shell

`office meeting join` starts a small program in the background, the **replicator**. It copies the
meeting into a **local log**, a file on your computer, and `join` prints its path. Each line of the
file is one event as JSON. People and agents follow this file instead of asking the hub again and
again:

```sh
LOG=$(office meeting join weekly)     # returns at once and prints the path of the local log
tail -f "$LOG" | grep --line-buffered '"type":"turn.end"\|"type":"addressed"'
office say "I can summarize that."
office meeting leave                  # or: office meeting end (for everyone)
```

- The file is `<runtime directory>/<session>/<participant>.jsonl`. The runtime directory is
  `$XDG_RUNTIME_DIR/agentic-office`, else `/tmp/agentic-office-<uid>`, and private to you. Nobody
  can change a line: marking a sentence off the record adds a new line.
- `join` starts the replicator **detached** and returns as soon as the file is open. This suits
  agents whose shell ends after each command. The replicator stops after the meeting ended or
  after you left. If the hub is not reachable, it keeps trying and reconnects by itself.
- `office meeting join weekly --foreground` stays and prints the path. The replicator then stops
  when `join` ends, also when it is killed. Use it when you run the command as a background task.
- Run `office meeting join` again to restart a replicator that was killed; it resumes the file.
  After a crash it cuts an incomplete last line and goes on after the last complete one.
- The local logs stay until the runtime directory is cleaned, at logout for `$XDG_RUNTIME_DIR`.
  How long they should be kept is still open.
- To read events from the hub instead of a file, for example to fill a gap or when you cannot
  follow a file: `office meeting events --after 41 --limit 20`.

**Off the record.** What is said stays said. You can mark **your own** sentence off the record
(`office utterance off-the-record 42`, or `--last`; with the microphone, press `o`). Everyone who
reads the session later gets a placeholder: the same event ID, `"offTheRecord": true`, no text.
The stage hides it. Agents must not use such a sentence anywhere, and nobody should put it into
minutes, decisions, notes or documents; the hub does not check this.

Other commands for the meeting: `meeting pause` and `meeting resume`, `signal turn-end`,
`signal silence-long` and `signal addressed` for agents and scripts, `hand raise`.

### Show the stage

The stage shows the meeting in two columns:

- **Left:** the artifact in focus, large. The **minutes** are the first artifact: they are in
  focus until someone focuses another artifact. Until there are minutes, the stage asks you to
  bring in an agent that takes them (see [Bring in an agent](#4-bring-in-an-agent)). When a session
  has more than one artifact, tabs appear above, with the minutes first. Below is a log of who
  created, changed or focused which artifact.
- **Right:** the participants with their capture status (captured, paused, not captured), agents
  marked as agents, and a hand with a number for everyone who raised their hand, in the order they
  raised it. Under the name of an agent that runs with `office agent run`, the stage shows what
  it does: "working", "working: fetch" with the kind or office tool it calls (never what it
  fetches or searches), "waiting", or "answered only in its own chat" when its reply reached
  nobody. Below it is the transcript of the latest sentences.

The stage is dark or light, as your computer is set. One person creates a link, opens it in a
browser and shares the window in your meeting tool (Zoom, Teams, Meet, Jitsi):

```sh
office stage link --session weekly
```

The link is read only and works for 24 hours (`-stage-link-ttl` of the hub), for this one session.
Sentences that are off the record are not shown. A hand stays raised until its participant speaks,
raises it again or leaves.

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

**Front matter.** Documents are the pages of the wiki of your team. A document may start with
front matter that names the schema of documents. The hub then checks its fields and refuses a
document with a wrong field, both through `office document` and with a push. Every field is
optional, and no other field is allowed:

```markdown
---
schema: office.document.v1
title: ACME GmbH                       # one line
summary: Customer since 2025.          # one line
tags: [customer]                       # a list; no tag twice
sources: [minutes/000012.yaml]         # paths of records, from the top of the repository
status: draft                          # draft, reviewed or outdated
owner: ada                             # a participant ID
reviewed:                              # reviews, each with exactly id and date
  - id: bob
    date: 2026-11-19
---
# ACME GmbH
```

A document without front matter is fine too; its first heading is its title.

**Links that lead nowhere.** Link other files with plain relative Markdown links. For example,
`documents/acme.md` links the minutes `minutes/000012.yaml` like this:

```markdown
[minutes](../minutes/000012.yaml)
```

When a link of a document leads to no file, the hub writes the document anyway and gives a hint. `office document create` and
`office document change` print each hint on stderr; `--json` puts them into the field `hints`:

```text
hint: the link "pricing.md" leads nowhere: there is no documents/pricing.md
```

A link to a document that waits for a release works: its stub is there.

A document that names a session (`--session`) waits for the release of the session, like the
minutes (see [Release what a session created](#release-what-a-session-created)).

To work on the files with your own tools, or to let an agent with a shell do it, see
[Work on files in a local directory](#work-on-files-in-a-local-directory).

### Work on files in a local directory

The documents, notes, decisions, minutes, tasks and slides of your tenant are files in a Git
repository on the hub. You can keep a copy of it in a directory on your computer and work on the
files with your own tools: an editor, `grep`, or an agent with a shell. You need the `git` command
line tool in your `PATH`.

How it fits together:

- The **main directory** is a mirror of the branch `main` on the hub. The office moves it forward
  and never merges or commits there. A commit in it is refused.
- You change files in a **worktree**: a second directory of the same clone, with its own branch.
- You send your changes with plain `git push`. The hub checks every push and refuses a bad one.
- A change of someone else (in a meeting, or by a push) reaches the main directory when you run
  `office sync`, or at once while `office sync follow` runs.
- The copy holds artifacts only. The session logs are never in it.

**Set up once.** Use an empty or new directory. The command needs your hub profile and tenant
(see [Set up once](#set-up-once)) and your key:

```sh
office sync init ~/artifacts
```

```text
cloning https://hub.example.org:8740/git/acme into /home/ada/artifacts
set up /home/ada/artifacts (https://hub.example.org:8740/git/acme)
commits in this directory are refused; work in a worktree: git worktree add ../my-change -b my-change
```

The command writes into the Git configuration of this clone only. Your global Git configuration
stays as it is. The clone gets:

- you as the author: your display name and `<participant ID>@office.invalid`,
- a helper that asks the hub for a fresh access token each time Git needs one, so no token is
  stored and none is in the address,
- the certificate that your hub profile trusts,
- a guard that refuses commits in the main directory.

Run `office sync init` again on a set-up directory to repair this configuration. It does not touch
your files. It refuses any other directory that is not empty and changes nothing.

**Update.** Run this in the main directory, or name the directory:

```sh
office sync                  # or: office sync ~/artifacts
```

```text
updated /home/ada/artifacts: main moved from f7d208a to 4b6c66e
```

If nothing is new, it says `is up to date`. The command moves `main` forward. If the hub rewrote
the history of `main`, nothing of yours is on `main`, so the command moves `main` to the new
state of the hub. Files that you changed and did not commit stay, unless the move would overwrite
them. It stops and
changes nothing when:

- `main` has commits of its own: move them to a branch in a worktree and push that branch,
- an incoming change would overwrite a file that you changed and did not commit: commit or move
  the file, then run the command again.

If a directory has the name of a command (`init`, `follow` or `credential`), write `./init`
instead of `init`. The command stops and asks for this form when both exist.

**Follow.** To keep the directory up to date while you work, leave this running in a terminal:

```sh
office sync follow
```

```text
following the changes of the hub
updated /home/ada/artifacts: main moved from 4b6c66e to 74f44db
```

It updates once, then again after each change of an artifact, whether it came through a meeting
or through a push. If the connection breaks, it connects again and updates. Stop it with Ctrl-C.
`--path` limits which changes start an update, for example `--path documents --path notes/plan.md`.
An update still brings everything.

**Follow while you are in a meeting.** You do not need `office sync follow` in a meeting. If you
run `office meeting join` (or an agent calls the MCP tool `meeting_join`) in a set-up directory, or
in a worktree of one, the clone is followed for as long as the meeting process runs: the
replicator of `join`, or the MCP server. It updates once and then after each change. Nothing is
printed on your terminal; the messages go to the log of the replicator
(`<participant>.replicator.log` in the runtime directory) or to the log of the MCP server. In any
other directory, `join` does nothing extra.

A problem with the clone never stops the meeting. It is written once to that log, in plain words,
for example `artifact sync: update of /home/ada/artifacts failed: ... the main directory has commits
of its own`. Fix it as described above. If a `join` finds a replicator that runs already, it reuses it
and does not start a second follow.

**Work in a worktree.** Create a worktree and a branch next to the main directory, change files
there, commit, and push to `main` of the hub:

```sh
cd ~/artifacts
git worktree add ../my-change -b my-change
cd ../my-change
printf '# Budget\n\nDraft.\n' > documents/budget.md
git add documents/budget.md
git commit -m "Add the budget"
git push origin HEAD:main
```

```text
To https://hub.example.org:8740/git/acme
   f7d208a..41024fd  HEAD -> main
```

The push moves `main` forward, and everyone who follows the tenant sees the change. The file
`documents/budget.md` is now a document of the tenant, as if it had been made with
`office document create`.

If you commit in the main directory, Git stops you and says where to work:

```text
office: no commit in the main directory of the artifacts.
The main directory mirrors the hub. Work in a worktree instead:
  git worktree add ../my-change -b my-change
Commit there, then push the branch.
```

**Plain Git and the certificate of the hub.** `office sync init` writes the certificate that your
hub profile trusts into the configuration of the clone (`http.sslCAInfo`). The variable
`GIT_SSL_CAINFO` beats that setting. If it is set in your environment, plain `git push` and
`git fetch` use the file of the variable and may say that the certificate is not trusted. The
`office` commands ignore the variable for their own Git calls and print a warning when it is
set and names another file. To fix plain Git, either:

- unset the variable for this shell: `unset GIT_SSL_CAINFO`, or
- point it at a bundle that holds the certificate of the hub as well as the authorities that you
  need, for example `cat ca-bundle.crt hub-ca.pem > ~/my-bundle.pem`, then
  `export GIT_SSL_CAINFO=~/my-bundle.pem`.

Do not turn off the check of certificates (`GIT_SSL_NO_VERIFY`, `http.sslVerify=false`).

**When the hub refuses a push.** The hub checks every push before it accepts it. A refused push
changes nothing. The reasons are shown as `remote:` lines. Fix the cause and push again.

| Reason | Message (shortened) | What to do |
|---|---|---|
| Wrong author | `commit c0028ab has the author Mallory <m@example.org>; every commit must have the author Ada Lovelace <ada@office.invalid>` | Set `user.name` and `user.email` as the message says, and amend the commits. A clone from `office sync init` has them right already. |
| Wrong file type | `notes.txt: only Markdown (.md) and YAML (.yaml, .yml) files are allowed` | Push only `.md`, `.yaml` and `.yml` files. No hidden files or folders (names that start with a dot), no links. |
| Invalid file | `documents/x.yaml: the schema "office.nothing.v1" is not known; the schemas are office.decision.v1, ...` | Fix the file. A file that names a schema must follow it. Any other `.md` or `.yaml` file only has to be valid text or YAML. |
| Too large | `documents/big.md is 1500000 bytes; a file may have at most 1048576 bytes` | Make the file smaller, or push fewer files. See the limits below. |
| Old base | `! [rejected] HEAD -> main (fetch first)` | Someone changed `main` meanwhile. Run `office sync`, then `git rebase main` in your worktree, then push again. |

Only the end result of a push counts for the file checks. A file that is broken in one commit and
fixed in the next one is fine.

**Hints.** A push of a document with a link that leads nowhere is accepted, and the hub shows a
hint for each such link. Links between the files of the same push work.

```text
remote: hints, which do not refuse the push:
remote:   - documents/budget.md: the link "plan.md" leads nowhere: there is no documents/plan.md
```

**What is not allowed.**

- You can push to the branch `main` only. No other branch, no tag, no deletion, no forced push, no
  rewrite of the history.
- Only Markdown and YAML files change. You may remove them.
- The session logs are not in the clone and cannot be pushed. They can hold sentences that are
  off the record.
- You need a key that is registered in the tenant. Another tenant looks as if it did not exist.

**Limits.** The operator of the hub sets them (see
[Start the hub with the binary](#start-the-hub-with-the-binary)). The defaults are 32 MiB for one
push, 1 MiB for one file and 500 files changed in one push. Ask your operator if you need more.

### Record decisions, notes and tasks

```sh
office hand raise --topic "Budget"
office minutes update --file minutes.md                # the running minutes, shown on the stage
office decision record "We rent the new office from January."
office decision record --session weekly --file decision.md
office note add --tag bank "Ask the bank about the loan."
office task create "Send the numbers to the bank" --assignee grace --session weekly
office task list --status open,in_progress --assignee grace
office task update local:1 --status done
office decision list --after 10 --limit 20
office meeting end
```

- The running minutes belong to the session. `office meeting end` stores the text of the last
  update as the minutes of the session. Read them later with `office minutes show weekly` or
  `office minutes list`.
- After `office meeting end`, what the session created waits for its release; see the next section.
- `office session list` lists the sessions in which you took part or that you imported, the newest
  first, with start, end and participants; a tenant admin sees every session of the tenant. The
  portal shows the same list on the page of the tenant (`office portal link`). Open a session there
  to read it: its minutes, decisions, tasks, notes and transcript, and its release with the staged
  artifacts. Sentences off the record do not appear.
- A decision or a note is linked to a session only with `--session`, and only a participant of
  the session may name it. It then waits for the release of the session, like the minutes; see the
  next section. A decision or a note without a session is shared with the tenant at once.
- A note is Markdown. Give it tags with `--tag`, once per tag. The hub stores it as a Markdown file
  `notes/NNNNNN.md`: the front matter names the author, whether the author is a human or an agent,
  the time, the session and the tags, and the body is the text.
- For now a task is a local draft in the hub. Its reference, for example `local:42`, is opaque:
  keep it and pass it back. The statuses are `open`, `in_progress`, `done` and `cancelled`. Any
  participant may read and change every task. With `--session ""` or `--assignee ""` you remove
  the session or the assignee.
- The list commands show the oldest first, at most 100 at a time (`--limit`, at most 1000). `task
  list` prints the token of the next page on stderr; pass it as `--page-token`.

### Release what a session created

Every member of the tenant reads the artifact repository, and nothing can be taken out of it
again. So what a session creates waits in a staging area of the session until it is released.
Only the people and agents who took part read it there.

```sh
office meeting end                     # prints when the hub releases the session
office session release weekly          # release it now
office session hold weekly             # or object: the hub then does not release it
office meeting end --release           # end and release in one go
office session release weekly --accept-quotes <token>   # release despite quotes (see below)
```

- **Who releases.** Every human who took part may release or object, also after leaving. An agent
  never does; the hub refuses it. If no human took part, for example a meeting of agents or an
  import that only agents worked on, the hub releases the session at its end.
- **The grace period.** Otherwise the hub releases the session 24 hours after its end. The operator
  sets another length for the whole hub with `office-hub -release-grace-period 2h`.
- **During the grace period** you can still mark your own sentence off the record. That mark reaches
  no local log and no agent any more: correct the staged files yourself, ask their authors, or
  object with `office session hold`. An objection holds the session until a human releases it.
- **The release** moves everything the session staged into the artifact repository in one commit,
  with you (or the hub) as the author, and the authors of the artifacts as co-authors.
- **A path that is taken.** The release replaces only the stub of a staged artifact, or writes
  where no file is. If someone pushed their own file at that path, for example while the hub was
  down, the release keeps the pushed file and releases everything else. The staged artifact is
  then not released: it stays in the staging area, and the people who took part still read it.
  The page of the session in the portal shows it as "not released: the path is taken". To keep
  it, read it and create it again at a new path with the command of its type, for example
  `office document get documents/plan.md` and then `office document create documents/plan-2.md
  --file plan.md`.
- **Quotes off the record.** Before every release, the hub searches the staged files for
  sentences that are off the record, word for word (sentences of at least three words; case and
  spaces do not count). If it finds one, it refuses the release with an error, and nothing is
  released. `office session release` then prints each place (file, line and the event of the
  sentence, never the sentence) and a token, and exits with 1. With `--json` it prints an error
  object with the code `failed_precondition`, the places (`quotes`) and the token (`quoteToken`);
  the MCP tool `session_release` returns the same error. Correct the files and release again, or release anyway with
  `--accept-quotes <token>`. The hub searches again and releases only if it finds exactly the same
  quotes; if a staged file changed in between, you get new places and a new token. The hub changes
  no content.
- **The grace period ends with quotes.** Then the hub does not release the session but holds it,
  as if someone had objected. A human who took part releases it with the token. A session without
  humans is released at its end all the same.
- **In the portal**, the page of a session shows the release status, when the hub releases it,
  and the staged artifacts to everyone who took part and to the tenant admins. A human who took
  part gets a button to release and, while the session waits, one to object. If the hub refuses a
  release because of quotes, the page lists the places and a button "Release anyway". Other members
  see a session only after its release: the released artifacts, who released them and their
  authors.
- After the end, a session takes only the release, the objection, notes about artifacts and,
  until the release, marks off the record.

**Staged minutes, decisions, notes, documents and slides.** Until the release, every participant
of the session may correct or remove them. Name the session of the artifact with `--session`:

```sh
office minutes show weekly --json              # the ID of the minutes, and "staged": true
office minutes change 3 --session weekly --file minutes.md
office decision change 4 --session weekly "We ship on Monday."
office note remove 7 --session weekly
office document get documents/plan.md --json   # the version, and "staged": true
office document change documents/plan.md --base <version> --session weekly --file plan.md
office document remove documents/plan.md --base <version> --session weekly
office slides change slides/42 deck.md --session weekly
office slides remove slides/42 --session weekly
```

- Only the participants of the session and the tenant admins read a staged record. Every other
  member sees only its number and that it waits for a release; the artifact repository and every
  clone hold the same stub instead of the record.
- A removed record is gone for good: its number is not used again, and only a new record brings
  its content back.
- After the release a record never changes. Record a new one that refers to it. A released
  document changes with `document change` like any other document, and the change is shared at
  once, whatever session it names. A released deck does not change; render a new one.
- A record, a document or a deck that names a session that is already released is shared with the
  tenant at once.
- The stage of a session shows the staged artifacts of that session. A staged artifact of another
  session shows as its stub.

### Show slides

Write a deck in Markdown, have the hub render it and show it on the stage:

```sh
cat > deck.md <<'EOF'
# Budget 2027
## Plan
- Hire two people
- Open an office in Lyon
Say the numbers slowly.
## Questions
EOF
ID=$(office slides render deck.md --session weekly)   # prints the deck ID, for example slides/42
office stage focus "$ID"
office slides get "$ID" --out deck.html
```

With `--session`, the deck waits for the release of the session; without it, every member sees
it at once.

`# ` is the deck title, `## ` starts a slide, `- ` is a bullet, and other text is the notes of the
slide, which are hidden. The result is one HTML page with large black text on white, for a screen
share. The same deck always gives the same page. `office help deck-format` describes the JSON form
and the details.

### Bring in an old conversation

You can import a conversation from another tool, for example a chat export or a meeting
transcript, as a session of its own. Convert it to the import format first (`office help
import-format` shows it), then:

```sh
office session import design.json --session design-review
office meeting join design-review       # then write minutes, record decisions and tasks, end it
```

The session is marked as imported: its log starts with the source and the importer, the stage says
so and shows "Imported" instead of "Live", and the imported speakers are marked. The office did not
see the conversation happen, and the signatures of the log only prove who imported it, not that
the speakers said it. If the import stops halfway, the session stays incomplete: import again into
a new session.

### Find out more

`office help` lists all commands. `office <group> --help` lists the commands of one group, for
example `office meeting --help`. `office <command> --help` explains one command.

## 4. Bring in an agent

An agent is a participant like you. It follows the meeting and acts through tools: it can keep
the minutes, record decisions, raise its hand, write documents, show slides and speak. Several agents can take
part in the same meeting, for example Claude Code, opencode and Kiro next to each other. The
**agent runner** (`office agent run`) keeps each one in the meeting, so that people can talk to
it in plain words. Each agent needs:

- its **own participant ID and key**, so that everyone sees who said or recorded what;
- its **own folder**, where its tool keeps its work and settings;
- the **hub and its certificate**, as for a human (see [Set up once](#set-up-once)). On the
  computer of a human participant, the agent uses the same hub profile.

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
writes in its own chat reaches nobody. If an agent ends its turn with text in its chat and does
not speak, the runner logs a warning with the text (`WARNING: the agent wrote a reply in its chat
but did not speak it`) and asks the agent once whether the text was meant for the meeting. If
the agent again does not speak, the runner logs that the reply reached nobody.

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

The runner prints what it does on its terminal, each line starting with `office agent:`: the
session, the wake strategy, each wake-up, each tool call, each permission decision, each decision
of a model with its reason or its probabilities, and what the agent wrote in its chat.

**When the runner stops.** After the meeting ended, it sends a last prompt, lets the agent finish
its work and stops. It also stops when the agent itself leaves the meeting, on Ctrl+C or SIGTERM,
and when the harness stops by itself, which is an error (exit code 1). It then stops the harness:
it closes its input, then sends SIGTERM and at last SIGKILL to its process group.

**What the agent may do.** The harness asks the runner before it uses a tool. By default the
runner allows the office tools and rejects everything else, for example shell commands or edits
of files. To allow more, give patterns of tool titles with `--allow` (`*` is any text). The
patterns replace the default, so include the office tools, for example
`--allow '*office*' --allow 'read*'`. `--allow '*'` allows everything. A harness that does not
ask, because its own settings allow a tool, follows its own settings.

More exactly, the runner offers the harness no file system and no terminal of its own. By default it
allows the call of an office tool, such as `mcp__office__utterance_say`, unless its kind is
`execute`, `edit`, `delete` or `move`, and rejects every other call. With `--allow` it allows
exactly the calls whose title matches one of the patterns; case does not matter. Configure the
harness itself for what it may do without asking.

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

### Teach the agent the memory of the team

The office ships a skill, `office-memory`, that tells an agent how to read and write the memory of
the team: which artifacts never change (minutes, decisions and notes), how to write a page of the
wiki with its front matter, and how to bring a meeting into the wiki.

With the runner, you do not install it. Before the runner starts the harness, it writes the skill
into the folder of the agent (`--cwd`), and the harness offers it to the agent:

- `.agent/skills/office-memory/`: Kiro and opencode read skills from there.
- `.claude/skills/office-memory`: a link to the same folder, for Claude Code.

The runner writes the skill of its own release at every start and replaces its earlier copy. It
never replaces a folder `office-memory` that it did not write, for example one that you installed
yourself: it keeps it and warns in its log. Remove that folder if the agent should get the skill
of the office.

If the folder of the agent is in a Git repository, the runner adds both paths to the file
`.git/info/exclude` of the repository, so you do not commit them by accident. It does not change
your `.gitignore`. Without Git, nothing needs to be done.

Do not change the skill. Put the rules of your team into the role of the agent instead.

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

### What the agent receives

When the runner wakes the agent, it sends everything that others said since the last time, oldest
first, one sentence per line: the event ID, the name, whether the speaker is a human or an agent,
and the text. Then it says whose turn has ended, or that the speaker may still be speaking, so
the agent should raise its hand or write instead of interrupting. For example:

```text
New in the meeting "weekly" since my last message (event ID, speaker, human or agent, text):

[41] Ada (human): We start with the budget.
[42] Ada (human): Bot, what did we decide last week?

The speaker may still be speaking. Do not interrupt: raise your hand or write instead, unless the speaker asked you to answer now.

The model of the runner thinks that the speaker addressed: bot.

Decide yourself whether someone addressed you or whether your role asks you to contribute now.
If not, end your turn without text. To answer, call utterance_say: text in this chat reaches
nobody in the meeting.
```

- The agent never gets its own sentences, its own end of turn, or sentences that are off the
  record. A sentence that went off the record before it was sent is never sent. If it was
  sent already, the runner wakes the agent as soon as it is free, names the event and says that
  it is off the record.
- One prompt runs at a time. While the agent works, what comes waits and goes out together in
  one prompt when the agent ends its turn. An end of turn without new sentences wakes nobody.
- With a model, the prompt also says whom the speaker addressed and, when a block of topics has
  ended, gives the task for that (`--topic-task`; the default is to update the minutes and record
  the decisions and tasks of the block, if the role asks for it).
- After the meeting ended, the last prompt carries what is left and asks the agent to finish the
  work of its role, for example the minutes, decisions or tasks, **without speaking**: the
  ended session takes no new sentences.

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
`reason` may be empty. With a server, the runner sends the same request as the user message of a
chat completion, with temperature 0 and a strict JSON schema for the answer.

**How often the runner asks.** With `llm` or `decision-model`, the runner asks after each sentence of
another participant, and once more when that participant ends the turn. One check runs at a time:
sentences that come meanwhile wait, and the next check gets all of them. A check also runs while
the agent works; a wake-up then waits until the agent is free. The runner wakes the agent when the
answer says that the agent should act, or that a topic has closed.

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
set, but never the key. `office help config` lists all keys.

Relative paths in the project file, such as `role-file`, are relative to the folder where you
start the command. A flag that changes the wake strategy, for example `--wake turn-end`, leaves out
the `wake` settings of the files that do not fit it. Keys that exclude each other are `role` and
`role-file`, and `wake.command` and `wake.url`: when a higher source sets one, the other one from a
lower source no longer counts.

### Without the runner: the tools only

A harness can also use the office directly as an MCP server, without the runner. Then the agent
must keep reading new events itself, and it stops listening when its turn ends. This suits short
jobs, for example writing the minutes after a meeting. `office mcp` is a local MCP server on
standard input and output. It takes the same flags and `OFFICE_*` variables as the CLI, and
`--session` sets the default session of the tools. The key must exist and be registered first.
Add `office mcp` as an MCP server in the agent's folder. In a configuration file, write absolute
paths: no `~` and no `$HOME`. The `cat <<EOF` in the Kiro example fills them in for you.

Without the runner, nobody hands the agent the memory skill. Install it in the folder of the agent
with the [skills CLI](https://www.npmjs.com/package/skills):

```bash
npx skills add mrsimpson/agentic-office --skill office-memory
```

```sh
# Claude Code
claude mcp add office -e OFFICE_KEY="$CFG/agents/claude/key.pem" -e OFFICE_HUB_PROFILE=main \
  -- "$(command -v office)" mcp --session weekly
# opencode (writes opencode.json in the folder)
opencode mcp add office --env OFFICE_KEY="$CFG/agents/opencode/key.pem" --env OFFICE_HUB_PROFILE=main \
  -- "$(command -v office)" mcp --session weekly
# Kiro
mkdir -p .kiro/settings
cat > .kiro/settings/mcp.json <<EOF
{
  "mcpServers": {
    "office": {
      "command": "$(command -v office)",
      "args": ["mcp", "--session", "weekly"],
      "env": { "OFFICE_KEY": "$CFG/agents/kiro/key.pem", "OFFICE_HUB_PROFILE": "main" },
      "autoApprove": ["meeting_events", "meeting_log", "whoami"]
    }
  }
}
EOF
```

A client that reads a JSON configuration, for example `.mcp.json`, takes the same:

```json
{
  "mcpServers": {
    "office": {
      "command": "/path/to/office",
      "args": ["mcp"],
      "env": { "OFFICE_KEY": "/home/me/.config/agentic-office/agent/key.pem",
               "OFFICE_HUB_PROFILE": "main" }
    }
  }
}
```

Then start the harness (`claude`, `opencode` or `kiro-cli chat`) and tell the agent what to do,
for example:

> Join the meeting "weekly" (meeting_join). Keep reading new events with meeting_events, from the
> last one you read. Keep the minutes with minutes_update. Speak (utterance_say) only when someone
> addresses you as "Claude". Stop when the session ends.

The tools do not wait for new events by themselves: the agent calls `meeting_events` again and
again. If it stops in between, tell it to keep listening.

**The tools.** Their names use `_` where the CLI uses a space: `meeting_join` is `meeting join`.

| Tool | CLI command |
|---|---|
| `meeting_join`, `meeting_log`, `meeting_events` | `meeting join`, `meeting log`, `meeting events` |
| `meeting_leave`, `meeting_pause`, `meeting_resume`, `meeting_end` | `meeting leave`, `pause`, `resume`, `end` |
| `utterance_say`, `utterance_off_the_record` | `say`, `utterance off-the-record` |
| `signal_turn_end`, `signal_silence_long`, `signal_addressed` | `signal …` |
| `hand_raise`, `minutes_update` | `hand raise`, `minutes update` |
| `stage_focus`, `stage_link` | `stage focus`, `stage link` |
| `slides_render`, `slides_get`, `slides_change`, `slides_remove` | `slides …` |
| `document_create`, `document_change`, `document_remove`, `document_get`, `document_list` | `document …` |
| `decision_record`, `decision_list`, `decision_change`, `decision_remove` | `decision …` |
| `note_add`, `note_list`, `note_change`, `note_remove` | `note …` |
| `minutes_get`, `minutes_list`, `minutes_change`, `minutes_remove` | `minutes show`, `minutes list`, `minutes change`, `minutes remove` |
| `task_create`, `task_get`, `task_list`, `task_update` | `task …` |
| `session_import`, `whoami` | `session import`, `whoami` |
| `session_release`, `session_hold` | `session release`, `session hold` (an agent is refused; its human releases) |

Session tools take an optional `session`; the default is `--session`, `OFFICE_SESSION`, or the
only session this participant joined from this machine. Each tool returns one JSON object, as
text and as structured content, in the same shape as the `--json` output of the CLI (see below).
Errors set `isError` and return `{"error":{"code","message"}}`. Not offered are the key and admin
commands (set up the key with the CLI), the gateway view and the ears (an agent has no
microphone), and a tool that waits for new events: the agent reads the log file.

**The log.** `meeting_join` returns `logPath`. The replicator runs inside the MCP process, so the
replication lives as long as that process: it stops when the client closes the connection, when
the process ends, when you leave, and after the meeting ended. Call `meeting_join` again to
restart it. If `office meeting join` already runs a replicator for the same participant,
`meeting_join` reuses it. The description of `meeting_join` explains the file and the event types
to the agent. The agent can follow the file, for example:

```sh
tail -n +1 -f "$LOG" | grep --line-buffered '"type":"turn.end"\|"type":"addressed"'
jq -c 'select(.id > 41 and (.type == "utterance.final" or .type == "addressed"))' "$LOG"
```

An agent that works in a shell can also read the local copy of the meeting log and act with the
same commands as a human:

```sh
export OFFICE_KEY="$CFG/agents/claude/key.pem"
LOG=$(office meeting join weekly)
tail -f "$LOG" | grep '"utterance.final"\|"turn.end"'
```

`meeting join` prints the path of the log. Each line is one event as JSON.

### Read results in scripts: `--json`

With `--json` (or `OFFICE_JSON=1`), a command prints exactly one JSON object on one line on
standard output, also when it fails, so a script can pipe it to `jq`. The field names are
camelCase. An event has the same form as its line in the local log. On failure the object is
`{"error": {"code": "permission_denied", "message": "…"}}`: the code is the code of the hub or one
of `usage`, `no_key`, `no_session` and `error`. The exit code is 0 for success, 1 for an error and
2 for a wrong call. `office help json` has the details, and the help of each command names its
result.

### Run an agent in a container

An agent that runs in a container uses the image `ghcr.io/mrsimpson/agentic-office/office`. Keep
the container running, because `meeting join` starts a replicator in the background that writes
the local log:

```sh
docker run -d --name bot -v bot-home:/home/office \
  --entrypoint sleep ghcr.io/mrsimpson/agentic-office/office infinity
docker exec bot office hub add main https://hub.example.org:8740 --tenant acme --default \
  --fingerprint <sha256 from the log of the hub>   # stores the certificate and the profile
docker exec bot office key create          # register the printed public key on the hub
docker exec bot office meeting join weekly # prints the path of the local log
docker exec bot office say "Hello"
```

The client image has no microphone recorder and no Whisper models. To transcribe, mount the models
and set `OFFICE_WHISPER_MODEL` and `OFFICE_VAD_MODEL`, or use `--wav`.

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
  client then joins this hub: run `office hub add trial https://localhost:8740 --default` and
  compare the fingerprint with the log of the hub container (the script also leaves the
  certificate in `office-trial/hub.crt`). This is how you try your headset (see [Join a meeting](#3-join-a-meeting)).
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
| `certificate signed by unknown authority` or `certificate is not standards compliant` | The client lacks the certificate of the hub. Run `office hub add <name> <url>` and compare the fingerprint with the log of the hub. Check with `office config show` that the hub profile you expect is active and has a `ca-cert`. |
| `certificate is valid for …, not …` or `cannot validate certificate for …` | The name or address of the hub (the `hub` of the profile) is not in the certificate. Delete `tls.crt` and `tls.key` in the data directory of the hub and start it with this name in `-tls-hosts`. Then give the participants the new fingerprint and let them run `office hub add` again with the same profile name; it replaces the old certificate. |
| `hub profile "…" … is not defined` | The profile named by `--hub-profile`, `OFFICE_HUB_PROFILE` or `hub-profile` in a file does not exist. The message lists the known profiles; `office hub list` shows them too. Add it with `office hub add`. |
| The command reaches the wrong hub or tenant | `office config show` prints the active profile and the source of each value. A `hub`, `tenant` or `OFFICE_*` variable from a higher source overrides the profile, and a `.office/config.yaml` in the current folder can choose another profile. |
| `office hub add` says the fingerprint does not match | The hub presents another certificate than you expect. Do not continue until you know why: you may reach another hub, or someone sits between you and the hub. Compare with the fingerprint in the log of the hub. |
| `public key is not registered: ask an admin to register SHA256:…` | Send your public key to your admin (`office key show` prints it). Check with `office whoami`. |
| `no such session for you` | Only who took part in a session, who imported it, and the tenant admins read it. Join it first, or ask a tenant admin. Check the name with `office session list`. |
| `Portal link not valid` or `Session ended` in the portal | The link works once and for a few minutes, and the portal for one hour. Run `office portal link` again. `not a member of the tenant`: name your tenant with `--tenant`, or open the start page with `--tenant ""`. |
| `the document has changed since you read it` (`aborted`) | Someone changed the document first. Read it again with `office document get` and redo your change with the new version in `--base`. |
| `no audio recorder found` | Install `ffmpeg` (macOS) or `pulseaudio-utils` (Linux), or set `OFFICE_MIC_COMMAND` (see `office help ears`). |
| The model download fails | Check your network, or download the models elsewhere and set `OFFICE_MODEL_URL` to a mirror. |
| `SIGILL: illegal instruction` | A client built on another computer uses CPU features yours lacks. Use the pre-built client, or build with `WHISPER_NATIVE=OFF`. |
| `office: the harness stopped …` or `ACP session/new: …` | The harness ended or refused the session. Run its command alone (for example `opencode acp`) and read its error. Often it needs a login first: log in with the harness itself. |
| The agent never speaks | Read the runner's terminal. Does it wake the agent (`waking the agent with …`)? With `--wake turn-end` (the default), or after the model failed, it wakes the agent only when your turn ends: pause for a moment, or run `office signal turn-end` after typed text. Does the runner reject tools that the agent needs (`permission rejected`)? Allow them with `--allow`. |
| `the forwarding judge failed (…)` or `the decision model failed (…)` | The sentences wait for the end of the turn; after three failures the runner switches to `turn-end`. For `--wake llm`, check `--decide-url` (it ends in `/v1`), `--decide-model` and `OFFICE_DECIDE_KEY`, and that the server supports structured output with a JSON schema. For `--wake decision-model`, check the full URL of the endpoint, the model and the key. A slow model needs a larger `--decide-timeout`. |
| A command fails with `unavailable` after the hub restarted | The hub waits for the participants to fill in the events after its last write, for at most `-restore-wait` (10 seconds by default). Repeat the command: it is safe, nothing is stored twice. |
| `configuration file …, line N: unknown key …` | A typo in `config.yaml` or `.office/config.yaml`. The message names the file, the line and the key. `office help config` lists the keys. |
| `the trial is running already` | Run `scripts/try-local.sh down`, then `up` again. |
| `Apple's container needs a DNS domain` | Do the one-time setup in [Try everything on one computer](#5-try-everything-on-one-computer). |
| Apple's `container` hangs: `container exec`, `logs` or `system stop` do not return, or the port takes connections but answers nothing | Stop the `container` services with launchd, then start them again: `launchctl list \| awk '/com\.apple\.container\./ {print $3}' \| while read s; do launchctl bootout "gui/$(id -u)/$s"; done`, then `container system start`. If that does not help, restart the Mac. |
| `bind: address already in use` for port 8740 | Another hub runs, for example `office-trial-hub` from `scripts/try-local.sh`. Find it with `lsof -i :8740`; remove the trial with `scripts/try-local.sh down`. |
