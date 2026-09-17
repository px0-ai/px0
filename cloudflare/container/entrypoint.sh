#!/usr/bin/env sh
# Boots px0 immediately against an (initially content-less) /repo, fetches
# the requested GitHub repo in the background, and exposes a tiny status
# endpoint on :8081 that the Durable Object polls before forwarding any
# real traffic to px0 on :7777.
#
# /repo is `git init`'d BEFORE px0 starts, then populated via `git fetch`
# + `checkout` rather than `git clone` into a fresh directory. This is
# deliberate: px0 memoizes git-availability per root the first time it's
# asked (see gitProbe() in git.go, cached in a process-lifetime map), so
# if px0 starts against a directory with no .git yet, it caches "no git"
# forever, even after content arrives. Having .git present from the very
# first probe keeps px0's own (unmodified) git-awareness features working.
set -u

STATUS_DIR=/status
STATUS_FILE="$STATUS_DIR/status.json"
mkdir -p "$STATUS_DIR"

write_status() {
  # $1=status  $2=message
  printf '{"status":"%s","message":"%s"}' "$1" "$2" >"$STATUS_FILE"
}

write_status "booting" ""

: "${PX0_OWNER:?PX0_OWNER env var required}"
: "${PX0_REPO:?PX0_REPO env var required}"
PX0_REF="${PX0_REF:-HEAD}"
MAX_REPO_MB="${PX0_MAX_REPO_MB:-200}"

REPO_DIR=/repo
CLONE_URL="https://github.com/${PX0_OWNER}/${PX0_REPO}.git"

mkdir -p "$REPO_DIR"
git -C "$REPO_DIR" init -q
git -C "$REPO_DIR" remote add origin "$CLONE_URL"

# 1. Start px0 immediately, pointed at /repo (already a git working tree,
#    just with no commits checked out yet).
px0 -host 0.0.0.0 -port 7777 -no-open -no-telemetry -no-agent "$REPO_DIR" &
PX0_PID=$!

# 2. Status server the DO polls independently of px0 itself.
darkhttpd "$STATUS_DIR" --port 8081 &

# 3. Pre-flight size check via GitHub's API, before spending time fetching.
write_status "checking-size" ""
SIZE_KB=$(curl -fsSL "https://api.github.com/repos/${PX0_OWNER}/${PX0_REPO}" 2>/dev/null \
  | grep -o '"size":[[:space:]]*[0-9]*' | head -1 | grep -o '[0-9]*')
if [ -n "${SIZE_KB:-}" ] && [ "$SIZE_KB" -gt $((MAX_REPO_MB * 1024)) ]; then
  write_status "error" "repo exceeds ${MAX_REPO_MB}MB cap"
  wait "$PX0_PID"
  exit 1
fi

# 4. Shallow fetch + checkout into the already-initialized /repo.
write_status "cloning" ""
if git -C "$REPO_DIR" fetch --depth=1 origin "$PX0_REF" 2>/tmp/clone.err \
  && git -C "$REPO_DIR" checkout -q FETCH_HEAD 2>>/tmp/clone.err; then
  CLONE_STATUS=0
else
  CLONE_STATUS=1
fi

if [ "$CLONE_STATUS" -ne 0 ]; then
  ERR=$(tail -c 200 /tmp/clone.err 2>/dev/null | tr -d '\n"')
  write_status "error" "clone failed: ${ERR}"
  wait "$PX0_PID"
  exit 1
fi

# 5. Tell px0 to pick up the newly-populated directory, then mark ready.
curl -fsS -X POST "http://localhost:7777/api/reindex" >/dev/null 2>&1 || true
write_status "ready" ""

wait "$PX0_PID"
