#!/usr/bin/env bash
# `lminstall env` is for an orchestrator that starts the installed CLI itself, many
# times, and wants state (sessions, config) to persist between launches. It writes
# one persistent runtime directory and prints the environment to export.
#
# usage: examples/orchestrator-env.sh [claude|codex|gemini|pi]     (default claude)
set -euo pipefail

provider="${1:-claude}"
LMINSTALL="${LMINSTALL:-lminstall}"
LMMOCK="${LMMOCK:-lmmock}"
reply="ORCHESTRATOR_OK"
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
export LMINSTALL_ROOT="$work/root"
"$LMINSTALL" install "$provider" >/dev/null

if [ -z "${LMMOCK_URL:-}" ]; then
  export EXAMPLE_WORK="$work"
  echo "{\"text\":\"$reply\",\"toolCalls\":false,\"anthropicBearer\":true}" > "$work/mock.json"
  exec "$LMMOCK" exec --builtin llm-api --config "$work/mock.json" -- "$0" "$provider"
fi

work="$EXAMPLE_WORK"
model=mock-agent; [ "$provider" = gemini ] && model=gemini-2.5-flash
"$LMINSTALL" configure "$provider" --backend mock --base-url "$LMMOCK_URL" --model "$model" \
  --credential-env UNUSED --profile-dir "$work/profile" --format plan >/dev/null

# Render the persistent runtime and import the exports (mock backend only; no secret involved).
"$LMINSTALL" env "$provider" --profile-dir "$work/profile" --runtime-dir "$work/runtime" --format sh > "$work/env.sh"
# shellcheck disable=SC1091
. "$work/env.sh"
echo "runtime dir : $LMINSTALL_RUNTIME_DIR"
echo "executable  : $LMINSTALL_EXECUTABLE"

# The orchestrator now launches the CLI directly, as often as it likes, with that environment.
for n in 1 2; do
  case "$provider" in
    claude) out="$("$LMINSTALL_EXECUTABLE" --no-session-persistence --tools "" -p "say it" --output-format json </dev/null)" ;;
    codex)  out="$("$LMINSTALL_EXECUTABLE" exec --skip-git-repo-check --json "say it" </dev/null)" ;;
    gemini) out="$("$LMINSTALL_EXECUTABLE" -p "say it" --output-format json </dev/null)" ;;
    pi)     out="$("$LMINSTALL_EXECUTABLE" --no-session --no-tools -p "say it" </dev/null)" ;;
  esac
  grep -q "$reply" <<<"$out" || { echo "FAIL launch $n"; exit 1; }
  echo "launch $n    : ok"
done
