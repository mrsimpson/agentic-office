#!/usr/bin/env bash
# Try the agentic office on one computer with the published container images.
# See USER_GUIDE.md. Works with Docker, Podman and Apple's `container` (macOS).
#
#   scripts/try-local.sh up                         start a hub and two participants: ada (human, admin), bot (agent)
#   scripts/try-local.sh as <name> <office args>    run the office CLI as a participant, for example: as bot meeting join weekly
#   scripts/try-local.sh add <name> [human|agent]   add one more participant container and register its key
#   scripts/try-local.sh register <name> <public-key> [human|agent]
#                                                   register a key from outside, for example a native client with a microphone
#   scripts/try-local.sh tail <name>                follow the local log of a participant (what an agent reads)
#   scripts/try-local.sh stage [session]            print the stage link for the browser (default session: weekly)
#   scripts/try-local.sh down                       remove the containers, the hub data and the trial directory
#
# Environment:
#   OFFICE_RUNTIME   docker, podman or container (default: the first one found, in this order)
#   OFFICE_VERSION   image tag (default: latest)
#   OFFICE_PORT      port of the hub on this computer (default: 8740)
#   OFFICE_TRIAL_DIR where the hub certificate for native clients goes (default: ./office-trial)
set -euo pipefail

VERSION=${OFFICE_VERSION:-latest}
PORT=${OFFICE_PORT:-8740}
TRIAL_DIR=${OFFICE_TRIAL_DIR:-$PWD/office-trial}
HUB_IMAGE=ghcr.io/mrsimpson/agentic-office/office-hub:$VERSION
CLIENT_IMAGE=ghcr.io/mrsimpson/agentic-office/office:$VERSION
PREFIX=office-trial
HUB=$PREFIX-hub
HUB_VOLUME=$PREFIX-hub-data
NETWORK=$PREFIX
CERT_IN_CLIENT=/home/office/.config/agentic-office/client/hub.crt

die() { echo "try-local: $*" >&2; exit 1; }
say() { echo "== $*"; }

# --- container runtime ---------------------------------------------------------------------------

RT=${OFFICE_RUNTIME:-}
if [ -z "$RT" ]; then
  for c in docker podman container; do
    if command -v "$c" >/dev/null 2>&1; then RT=$c; break; fi
  done
fi
[ -n "$RT" ] || die "no container runtime found; install Docker, Podman or Apple's container (see USER_GUIDE.md)"
command -v "$RT" >/dev/null 2>&1 || die "$RT is not installed"

# Apple's container finds other containers by <name>.<dns domain> on its default network.
# Docker and Podman use a network of their own with the alias "hub".
if [ "$RT" = container ]; then
  CONFIG=${XDG_CONFIG_HOME:-$HOME/.config}/container/config.toml
  DOMAIN=$(awk '/^\[/{s=$0} s=="[dns]" && $1=="domain" {gsub(/"/,"",$3); print $3}' "$CONFIG" 2>/dev/null || true)
  HUB_HOST=${DOMAIN:+$HUB.$DOMAIN}
  NET_ARGS=()
  HUB_NET_ARGS=()
else
  HUB_HOST=hub
  NET_ARGS=(--network "$NETWORK")
  HUB_NET_ARGS=(--network "$NETWORK" --network-alias hub)
fi

rt_rm() { if [ "$RT" = container ]; then container delete --force "$@"; else "$RT" rm -f "$@"; fi; }
rt_volume_rm() { if [ "$RT" = container ]; then container volume delete "$@"; else "$RT" volume rm "$@"; fi; }
exists() { "$RT" inspect "$1" >/dev/null 2>&1; }
tty_flags() { if [ -t 0 ] && [ -t 1 ]; then echo -it; else echo -i; fi; }

need_hub() { exists "$HUB" || die "the hub is not running; start it with: $0 up"; }

# Runs the office CLI in a participant container.
office_as() { local who=$1; shift; "$RT" exec $(tty_flags) "$PREFIX-$who" office "$@"; }

# Starts a participant container. Its key lives in the container: it is gone after "down".
start_participant() {
  local who=$1
  exists "$PREFIX-$who" && die "participant $who exists already"
  "$RT" run -d --name "$PREFIX-$who" "${NET_ARGS[@]}" -e OFFICE_HUB="https://$HUB_HOST:8740" \
    --entrypoint sleep "$CLIENT_IMAGE" infinity >/dev/null
}

public_key_of() { office_as "$1" key create 2>/dev/null | awk '/^public key:/ {print $3}'; }

give_cert() { "$RT" cp "$TRIAL_DIR/hub.crt" "$PREFIX-$1:$CERT_IN_CLIENT"; }

# --- commands ------------------------------------------------------------------------------------

cmd_up() {
  exists "$HUB" && die "the trial is running already; remove it first with: $0 down"
  if [ "$RT" = container ] && [ -z "$HUB_HOST" ]; then
    die "Apple's container needs a DNS domain, so that containers find the hub by name.
Add these lines to ~/.config/container/config.toml and restart the service:
  [dns]
  domain = \"test\"
  container system stop && container system start
See USER_GUIDE.md."
  fi
  say "runtime $RT, images $VERSION, hub https://localhost:$PORT (inside: https://$HUB_HOST:8740)"
  [ "$RT" = container ] || "$RT" network create "$NETWORK" >/dev/null 2>&1 || true
  mkdir -p "$TRIAL_DIR"

  say "participant ada (human, first admin)"
  start_participant ada
  local ada; ada=$(public_key_of ada)
  [ -n "$ada" ] || die "could not create the key of ada"

  say "hub: signing key"
  # As root, because a new volume may belong to root; the hub itself runs as the user office.
  "$RT" run --rm -u root -v "$HUB_VOLUME:/data" --entrypoint sh "$HUB_IMAGE" -c "
    test -f /data/signing.key || office-hub keygen /data/signing.key >/dev/null
    chown -R office:office /data"

  say "hub: start"
  "$RT" run -d --name "$HUB" "${HUB_NET_ARGS[@]}" -p "$PORT:8740" -v "$HUB_VOLUME:/data" "$HUB_IMAGE" \
    -data-dir /data -addr 0.0.0.0:8740 -signing-key /data/signing.key \
    -first-system-admin "ada=$ada" \
    -tls-hosts "$HUB_HOST,localhost,127.0.0.1" -public-url "https://localhost:$PORT" >/dev/null
  local i
  for i in $(seq 1 60); do
    "$RT" logs "$HUB" 2>&1 | grep -q listening && break
    sleep 0.5
  done
  "$RT" logs "$HUB" 2>&1 | grep -q listening || { "$RT" logs "$HUB" >&2 || true; die "the hub did not start"; }

  "$RT" cp "$HUB:/data/tls.crt" "$TRIAL_DIR/hub.crt"
  give_cert ada
  office_as ada whoami >/dev/null || die "ada cannot reach the hub"

  say "participant bot (agent)"
  cmd_add bot agent >/dev/null

  echo
  echo "Ready. The hub certificate is in $TRIAL_DIR/hub.crt (for native clients)."
  echo "Stage for the browser (accept the warning about the self-signed certificate):"
  echo "  $(cmd_stage weekly)"
  echo "Next:"
  echo "  $0 as bot meeting join weekly"
  echo "  $0 as ada meeting join weekly"
  echo "  $0 as ada say \"Good morning\""
  echo "  $0 tail bot"
}

cmd_add() {
  local who=${1:?usage: add <name> [human|agent]} kind=${2:-agent}
  need_hub
  start_participant "$who"
  local key; key=$(public_key_of "$who")
  [ -n "$key" ] || die "could not create the key of $who"
  give_cert "$who"
  office_as ada admin user create "$who" "$key" --name "$who" --kind "$kind"
  echo "participant $who ($kind) is ready: $0 as $who <office command>"
}

cmd_register() {
  local who=${1:?usage: register <name> <public-key> [human|agent]} key=${2:?usage: register <name> <public-key> [human|agent]}
  need_hub
  office_as ada admin user create "$who" "$key" --name "$who" --kind "${3:-human}"
}

cmd_stage() { need_hub; office_as ada stage link --session "${1:-weekly}"; }

cmd_as() {
  local who=${1:?usage: as <name> <office command>}; shift
  exists "$PREFIX-$who" || die "no participant $who; add one with: $0 add $who"
  office_as "$who" "$@"
}

cmd_tail() {
  local who=${1:?usage: tail <name>}
  exists "$PREFIX-$who" || die "no participant $who"
  "$RT" exec $(tty_flags) "$PREFIX-$who" sh -c 'f=$(office meeting log) && exec tail -n 20 -f "$f"'
}

cmd_down() {
  local names
  if [ "$RT" = container ]; then
    names=$(container list --all --quiet 2>/dev/null | grep "^$PREFIX-" || true)
  else
    names=$("$RT" ps -a --format '{{.Names}}' | grep "^$PREFIX-" || true)
  fi
  [ -z "$names" ] || rt_rm $names >/dev/null
  rt_volume_rm "$HUB_VOLUME" >/dev/null 2>&1 || true
  [ "$RT" = container ] || "$RT" network rm "$NETWORK" >/dev/null 2>&1 || true
  rm -rf "$TRIAL_DIR"
  say "removed"
}

case "${1:-}" in
  up) cmd_up ;;
  as) shift; cmd_as "$@" ;;
  add) shift; cmd_add "$@" ;;
  register) shift; cmd_register "$@" ;;
  stage) shift; cmd_stage "$@" ;;
  tail) shift; cmd_tail "$@" ;;
  down) cmd_down ;;
  *) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
