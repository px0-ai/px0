#!/usr/bin/env sh
# Vendor the pinned Mermaid ESM build into web/lib/mermaid/<version>/.
#
# Downloads the npm tarball, verifies its sha256, and extracts only
#
#   package/dist/mermaid.esm.min.mjs
#   package/dist/chunks/mermaid.esm.min/*.mjs
#
# preserving the relative layout the entry imports. Source maps and the other
# dist flavours (mermaid.esm, mermaid.core, the .js bundles) are not extracted.
# web/src/mermaid.js imports the entry at
# /static/lib/mermaid/<version>/mermaid.esm.min.mjs with a computed dynamic
# import, so none of this reaches web/app.js. The version directory keeps the
# immutable /static/lib/ cache header safe when the pin moves.
#
# Requires curl and tar. Safe to re-run: every run rebuilds the checkout from
# the verified tarball, so an interrupted run is fixed by running it again.
set -eu

VERSION=11.17.2
SHA256=6ad2f42c3fc26bbf9e45cbb6d11898972573ea52b33a5f4ff51952899f950ffd
TARBALL_URL="https://registry.npmjs.org/mermaid/-/mermaid-${VERSION}.tgz"

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DEST="$ROOT/web/lib/mermaid/$VERSION"
REL=${DEST#"$ROOT"/}

TMP=$(mktemp -d "${TMPDIR:-/tmp}/px0-mermaid.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

log() { printf '[vendor-mermaid] %s\n' "$*"; }
die() { printf '[vendor-mermaid] error: %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  fi
}

log "downloading mermaid $VERSION"
curl -fsSL --retry 2 --connect-timeout 10 -o "$TMP/mermaid.tgz" "$TARBALL_URL" ||
  die "download failed: $TARBALL_URL"

got=$(sha256_of "$TMP/mermaid.tgz")
[ "$got" = "$SHA256" ] ||
  die "sha256 mismatch: expected $SHA256, got $got"
log "sha256 ok"

log "extracting dist files"
STAGE="$TMP/stage"
mkdir -p "$STAGE"
tar -xzf "$TMP/mermaid.tgz" -C "$STAGE" \
  package/dist/mermaid.esm.min.mjs \
  package/dist/chunks/mermaid.esm.min
find "$STAGE/package/dist/chunks/mermaid.esm.min" -name '*.map' -type f -exec rm -f {} +
[ -f "$STAGE/package/dist/mermaid.esm.min.mjs" ] || die "entry module missing from tarball"
mv "$STAGE/package/dist/mermaid.esm.min.mjs" "$STAGE/mermaid.esm.min.mjs"
mkdir -p "$STAGE/chunks"
mv "$STAGE/package/dist/chunks/mermaid.esm.min" "$STAGE/chunks/mermaid.esm.min"
rm -rf "$STAGE/package"

# Browser module graphs fail whole-file on an unresolved specifier, so every
# relative import in the shipped files must land on another shipped file.
log "checking the import graph"
unresolved="$TMP/unresolved"
: > "$unresolved"
count=0
for f in "$STAGE/mermaid.esm.min.mjs" "$STAGE"/chunks/mermaid.esm.min/*.mjs; do
  count=$((count + 1))
  dir=${f%/*}
  # Quoted strings that start with ./ and end in .mjs are import specifiers in
  # this build. A false positive could only over-report, never hide a miss.
  for spec in $(grep -o "['\"]\./[^'\"]*\.mjs['\"]" "$f" | tr -d "'\""); do
    [ -f "$dir/$spec" ] || printf '  %s -> %s\n' "${f#"$STAGE"/}" "$spec" >> "$unresolved"
  done
done
[ "$count" -gt 1 ] || die "no chunk files found in tarball"
if [ -s "$unresolved" ]; then
  cat "$unresolved" >&2
  die "$(wc -l < "$unresolved" | tr -d ' ') unresolved import(s)"
fi

bytes=$(find "$STAGE" -type f -name '*.mjs' -exec cat {} + | wc -c | tr -d ' ')
mib=$(awk -v b="$bytes" 'BEGIN { printf "%.1f", b / 1048576 }')
log "graph ok: $count files, $bytes bytes ($mib MiB)"

log "installing into $REL"
rm -rf "$ROOT/web/lib/mermaid"
mkdir -p "$(dirname "$DEST")"
mv "$STAGE" "$DEST"
log "done: mermaid $VERSION -> $REL"
