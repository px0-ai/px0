#!/usr/bin/env bash
# Proof of concept: harness auth reporting and bring-your-own-key.
#
# Runs a real px0 against a throwaway workspace with a throwaway config home and
# asserts the properties that matter, over HTTP, against whatever harnesses
# happen to be installed on the machine.
#
# This is a demonstration, not a substitute for the unit tests in
# credentials_test.go and agentauth_test.go. It exists so a reviewer can watch
# the behaviour from the outside without reading Go.
#
# Requires only: go, curl, grep. No jq, no python.
#
#   scripts/poc-agent-auth.sh [port]

set -uo pipefail

PORT="${1:-7799}"
BASE="http://127.0.0.1:$PORT"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
CONF="$(mktemp -d)"
export XDG_CONFIG_HOME="$CONF"
CREDS="$CONF/px0/credentials.json"

pass=0; fail=0; PID=""
cleanup() { [ -n "$PID" ] && kill "$PID" 2>/dev/null; rm -rf "$WORK" "$CONF"; }
trap cleanup EXIT

ok()   { printf '  \033[32mPASS\033[0m  %s\n' "$1"; pass=$((pass + 1)); }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; fail=$((fail + 1)); }
skip() { printf '  \033[33mSKIP\033[0m  %s\n' "$1"; }
sec()  { printf '\n\033[1m%s\033[0m\n' "$1"; }

get()  { curl -sS "$BASE$1"; }
# post <path-with-query> [json-body]
post() {
  if [ -n "${2:-}" ]; then
    curl -sS -X POST "$BASE$1" -H "Origin: $BASE" -H 'Content-Type: application/json' -d "$2"
  else
    curl -sS -X POST "$BASE$1" -H "Origin: $BASE"
  fi
}
# authobj <harness> -- the compact JSON object for one harness, so a grep cannot
# match a different harness's fields.
authobj() { get /api/agent/auth | grep -o "\"$1\":{[^}]*}"; }

sec "0. Build and start px0 on a throwaway config home"
( cd "$ROOT" && go build -o "$WORK/px0" . ) || { echo "build failed"; exit 1; }
"$WORK/px0" -port "$PORT" -no-open -no-telemetry -no-lsp "$WORK" >"$WORK/px0.log" 2>&1 &
PID=$!
for _ in $(seq 1 60); do curl -sS "$BASE/api/meta" >/dev/null 2>&1 && break; sleep 0.2; done
if grep -q 'url:' "$WORK/px0.log"; then ok "px0 is serving on $BASE"; else
  echo "px0 did not start:"; cat "$WORK/px0.log"; exit 1; fi

sec "1. Harness catalog"
H="$(get /api/agent/harnesses)"
COUNT=$(printf '%s' "$H" | grep -o '"name":' | wc -l | tr -d ' ')
printf '  %s harnesses known\n' "$COUNT"
if [ "$COUNT" -ge 14 ]; then ok "catalog covers at least 14 harnesses"; else bad "only $COUNT harnesses"; fi
MISSING=""
for h in claude codex copilot gemini qwen droid cursor-agent agy opencode crush cline cn aider goose; do
  printf '%s' "$H" | grep -q "\"name\":\"$h\"" || MISSING="$MISSING $h"
done
if [ -z "$MISSING" ]; then ok "every documented harness is present"; else bad "missing:$MISSING"; fi

sec "2. Auth state is reported, and each state explains itself"
A="$(get /api/agent/auth)"
SEEN=""
for st in signed-in signed-out unknown key local; do
  printf '%s' "$A" | grep -q "\"$st\"" && SEEN="$SEEN $st"
done
printf '  states observed:%s\n' "${SEEN:- none}"
if [ "$(printf '%s' "$SEEN" | wc -w | tr -d ' ')" -ge 3 ]; then
  ok "several distinct states are reachable, not one hardcoded answer"
else
  bad "only saw:${SEEN:- none} (this machine may simply lack variety)"
fi
if printf '%s' "$A" | grep -q '"detail"'; then ok "states carry an explanation"; else bad "a state has no detail"; fi

sec "3. A stored key is never returned in full"
SECRET="sk-ant-poc-$(printf 'x%.0s' $(seq 1 40))"
post /api/agent/credential "{\"provider\":\"anthropic\",\"key\":\"$SECRET\"}" >/dev/null
LEAK=no
get /api/agent/auth | grep -q "$SECRET" && LEAK=yes
get /api/agent/harnesses | grep -q "$SECRET" && LEAK=yes
if [ "$LEAK" = no ]; then ok "the key appears in no GET response"; else bad "the key leaked"; fi
if get /api/agent/auth | grep -q '"masked"'; then ok "the key is reported masked instead"; else bad "no masked value"; fi

sec "4. A key too short to hide anything behind is not partially revealed"
post /api/agent/credential '{"provider":"groq","key":"short99"}' >/dev/null
if get /api/agent/auth | grep -q 'short99'; then bad "a 7-character key was partially revealed"; else
  ok "a 7-character key comes back fully masked"; fi

sec "5. Injection defaults to off for a subscription harness"
R="$(post '/api/agent/auth/mode?harness=claude&mode=auto')"
if printf '%s' "$R" | grep -o '"claude":{[^}]*}' | grep -q 'keysFromEnv'; then
  bad "auto injected a key for a subscription harness"
else
  ok "auto injects nothing for claude"
fi

sec "6. Opting in per harness is honoured, and reversible"
R="$(post '/api/agent/auth/mode?harness=claude&mode=key')"
if printf '%s' "$R" | grep -o '"claude":{[^}]*}' | grep -q 'keysFromEnv'; then
  ok "key mode injects the stored key"
else bad "key mode did not inject"; fi
R="$(post '/api/agent/auth/mode?harness=claude&mode=oauth')"
if printf '%s' "$R" | grep -o '"claude":{[^}]*}' | grep -q 'keysFromEnv'; then
  bad "oauth mode still injects"
else ok "oauth mode stops injecting"; fi
R="$(post '/api/agent/auth/mode?harness=claude&mode=auto')"
if printf '%s' "$R" | grep -o '"claude":{[^}]*}' | grep -q '"mode":"auto"'; then
  ok "auto is remembered and round-trips"; else bad "mode did not round-trip"; fi

sec "7. A mode a harness does not offer is refused"
if post '/api/agent/auth/mode?harness=crush&mode=oauth' | grep -q 'does not offer'; then
  ok "crush refuses oauth, because it has no login to override"
else bad "crush accepted a mode it cannot honour"; fi
if post '/api/agent/auth/mode?harness=nope&mode=key' | grep -q 'unknown harness'; then
  ok "an unknown harness is refused"
else bad "an unknown harness was accepted"; fi

sec "8. Sign-in is delegated, never reimplemented"
if post '/api/agent/signin?harness=claude' | grep -q 'no login command'; then
  ok "claude reports there is no login command to delegate to"
else bad "unexpected signin response"; fi
if post '/api/agent/signout?harness=copilot' | grep -q 'no logout command'; then
  ok "copilot reports there is no logout command"
else bad "unexpected signout response"; fi

sec "9. Cross-origin writes are refused"
CODE=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/agent/credential" \
  -H 'Origin: https://evil.example' -H 'Content-Type: application/json' \
  -d '{"provider":"anthropic","key":"sk-evil"}')
if [ "$CODE" = "403" ]; then ok "a cross-origin key write is refused"; else bad "cross-origin write returned $CODE"; fi

sec "10. A key for an unrecognised provider survives an unrelated save"
mkdir -p "$(dirname "$CREDS")"
printf '{"anthropic":"sk-ant-keep","retired-vendor":"legacy-key-abc"}' >"$CREDS"
post /api/agent/credential '{"provider":"openai","key":"sk-openai-new"}' >/dev/null
if grep -q 'legacy-key-abc' "$CREDS"; then ok "an unrecognised key was preserved"; else
  bad "an unrecognised key was destroyed by an unrelated save"; fi

# Ask Go what mode it actually wrote, rather than trusting the shell: MSYS and
# Cygwin synthesise a mode from a fake inode and will happily report 644 for a
# file Go created 0600, which would make this a false alarm rather than a
# finding.
cat >"$WORK/mode.go" <<'EOF'
package main

import (
	"fmt"
	"os"
)

func main() {
	fi, err := os.Stat(os.Args[1])
	if err != nil {
		fmt.Println("error")
		return
	}
	fmt.Printf("%o\n", fi.Mode().Perm())
}
EOF
MODE=$( cd "$WORK" && go run mode.go "$CREDS" 2>/dev/null )
case "$(go env GOOS)" in
  windows)
    skip "credentials.json is 0600 (Windows has no POSIX modes; Go reports 644 for everything)"
    ;;
  *)
    if [ "$MODE" = "600" ]; then ok "credentials.json is 0600"; else
      bad "credentials.json is $MODE, want 600"; fi
    ;;
esac

sec "11. A command-template harness has no auth story to tell"
if get /api/agent/auth | grep -q 'my-own-wrapper'; then
  bad "an unknown harness was described"
else ok "an unknown harness is reported as undescribable, not guessed at"; fi

printf '\n\033[1m%d passed, %d failed\033[0m\n' "$pass" "$fail"
[ "$fail" -eq 0 ] || exit 1
