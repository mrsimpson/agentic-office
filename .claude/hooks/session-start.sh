#!/bin/bash
# SessionStart hook for Claude Code cloud sessions. It builds whisper.cpp and the office programs,
# and sets up the access to the shared office hub when the environment provides a key.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

cd "${CLAUDE_PROJECT_DIR:-.}"

# whisper.cpp (cgo, for the ears): build it only when the Makefile (it pins WHISPER_REF) or this
# script changed since the last successful build. The hash file lives in the build directory,
# so it goes away with the build.
hashes=third_party/whisper.cpp/build_go/.session-start.sha256
if ! sha256sum --check --status "$hashes" 2>/dev/null; then
  rm -f "$hashes"
  make whisper >&2
  sha256sum .claude/hooks/session-start.sh Makefile > "$hashes"
fi

# The programs in bin/. The Go build cache makes this quick. A build that fails, for example on
# work in progress after a resume, must not block the session.
if ! make build >&2; then
  echo "session-start: make build failed; run it yourself" >&2
fi

# Access to the shared office hub. The key comes from the environment of the cloud session.
# The OFFICE_* variables are not exported for the whole session, because the tests take their
# environment from the session. The wrapper office-web sets them for one call only.
if [ -n "${CLAUDE_WEB_OFFICE_ADMIN_KEY:-}" ]; then
  dir="$HOME/.config/agentic-office-web"
  mkdir -p "$dir" "$HOME/.local/bin"
  (umask 077 && printf '%s\n' "$CLAUDE_WEB_OFFICE_ADMIN_KEY" > "$dir/key.pem")
  cat > "$HOME/.local/bin/office-web" << WRAPPER
#!/bin/sh
# office with the shared hub. Override the tenant with --tenant or OFFICE_WEB_TENANT.
exec env OFFICE_HUB="\${OFFICE_WEB_HUB:-https://office.no-panic.org}" \\
  OFFICE_TENANT="\${OFFICE_WEB_TENANT:-test}" \\
  OFFICE_KEY="$dir/key.pem" \\
  OFFICE_RUNTIME_DIR="$dir/run" \\
  "$PWD/bin/office" "\$@"
WRAPPER
  chmod +x "$HOME/.local/bin/office-web"
  if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
    echo "export PATH=\"\$HOME/.local/bin:\$PATH\"" >> "$CLAUDE_ENV_FILE"
  fi
fi
