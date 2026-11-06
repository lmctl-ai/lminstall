#!/usr/bin/env bash
# Install a coding CLI with lminstall, point it at lmmock's built-in LLM API mock
# and check three things with the real binary:
#   1. with the right API key it prints the mock's exact reply,
#   2. the mock saw the request (lmmock's audit log),
#   3. with a WRONG key the CLI fails with an authentication error instead.
#
# usage: examples/mock-run.sh [claude|codex|gemini|pi]       (default claude)
# needs: lminstall and lmmock (>= the release that has --builtin llm-api) on PATH,
#        or LMINSTALL=/path/to/lminstall LMMOCK=/path/to/lmmock. No account needed.
set -euo pipefail

provider="${1:-claude}"
LMINSTALL="${LMINSTALL:-lminstall}"
LMMOCK="${LMMOCK:-lmmock}"
reply="EXAMPLE_REPLY_OK"
prompt="Reply with exactly $reply. Do not call any tools."
good_key="example-valid-key"
bad_key="example-WRONG-key"

# Per-provider model and one-shot arguments (the same ones lminstall's CI uses).
case "$provider" in
  claude) model=mock-agent;       args=(--no-session-persistence --tools "" -p "$prompt" --output-format json) ;;
  codex)  model=mock-agent;       args=(exec --skip-git-repo-check --json "$prompt") ;;
  gemini) model=gemini-2.5-flash; args=(-p "$prompt" --output-format json) ;;
  pi)     model=mock-agent;       args=(--no-session --no-tools -p "$prompt") ;;
  *) echo "unsupported example provider: $provider" >&2; exit 2 ;;
esac

if [ -z "${LMMOCK_URL:-}" ]; then
  # Outer phase: install once, then run this same script inside `lmmock exec`.
  work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
  export LMINSTALL_ROOT="$work/root"           # an owned prefix; nothing outside it is touched
  "$LMINSTALL" install "$provider" >/dev/null
  # toolCalls=false: coding CLIs always declare tools, we only want a plain reply.
  # anthropicBearer: Claude Code authenticates with Authorization: Bearer.
  cat > "$work/mock.json" <<JSON
{"text":"$reply","toolCalls":false,"anthropicBearer":true,"apiKey":"$good_key"}
JSON
  export EXAMPLE_WORK="$work"
  exec "$LMMOCK" exec --builtin llm-api --config "$work/mock.json" --audit-log "$work/audit.jsonl" -- "$0" "$provider"
fi

# Inner phase: LMMOCK_URL is the mock's loopback URL, set by `lmmock exec`.
work="$EXAMPLE_WORK"
"$LMINSTALL" configure "$provider" --backend mock --base-url "$LMMOCK_URL" --model "$model" \
  --credential-env EXAMPLE_API_KEY --profile-dir "$work/profile" --format plan >/dev/null

echo "== $provider with the right key"
out="$(EXAMPLE_API_KEY="$good_key" "$LMINSTALL" run "$provider" --profile-dir "$work/profile" -- "${args[@]}" </dev/null)"
grep -q "$reply" <<<"$out" || { echo "FAIL: reply missing"; echo "$out" | head -c 400; exit 1; }
echo "ok: got $reply"

echo "== $provider with a wrong key"
set +e
bad="$(EXAMPLE_API_KEY="$bad_key" "$LMINSTALL" run "$provider" --profile-dir "$work/profile" -- "${args[@]}" 2>&1 </dev/null)"
status=$?
set -e
if grep -q "$reply" <<<"$bad"; then echo "FAIL: a wrong key still got the reply"; exit 1; fi
grep -Eq '"status":(401|403)' "$work/audit.jsonl" || { echo "FAIL: the mock never rejected a key"; exit 1; }
detail="$(grep -oE '"(terminal_reason|result)":"[^"]*"|[^ ]*Invalid API key[^ ]*|AUTH[^ ]*' <<<"$bad" | head -3 | tr '\n' ' ')"
echo "ok: rejected (exit $status) ${detail:-$(tr '\n' ' ' <<<"$bad" | cut -c1-100)}"
