#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

cd "$ROOT_DIR"

# Source environment variables if present (.env, .env.local, sdk.env)
[ -f "$ROOT_DIR/.env" ] && source "$ROOT_DIR/.env"
[ -f "$ROOT_DIR/.env.local" ] && source "$ROOT_DIR/.env.local"
[ -f "$ROOT_DIR/sdk.env" ] && source "$ROOT_DIR/sdk.env"

# Use virtualenv python if available, otherwise fall back to PATH python
if [ -f "$ROOT_DIR/venv/bin/python" ]; then
  PYTHON="$ROOT_DIR/venv/bin/python"
elif command -v python3 >/dev/null 2>&1; then
  PYTHON="python3"
else
  PYTHON="python"
fi

VERSION="$(cat "$ROOT_DIR/VERSION" | tr -d '[:space:]')"

echo "==> Preparing to publish px0 v$VERSION to PyPI"

# Ensure twine and build tools are available
"$PYTHON" -m pip install --quiet build twine

if [ ! -d "$ROOT_DIR/dist" ] || [ -z "$(ls -A "$ROOT_DIR/dist" 2>/dev/null)" ]; then
  echo "==> Building distribution packages..."
  "$PYTHON" -m build
fi

echo "==> Checking package distributions with twine..."
"$PYTHON" -m twine check dist/*

PYPI_REPOSITORY="${PYPI_REPOSITORY:-https://upload.pypi.org/legacy/}"

echo "==> Uploading px0 v$VERSION to $PYPI_REPOSITORY..."
args=(--repository-url "$PYPI_REPOSITORY")
if [ -n "${PYPI_TOKEN:-}" ]; then
  args+=(--username __token__ --password "$PYPI_TOKEN")
elif [ -n "${TWINE_PASSWORD:-}" ]; then
  [ -n "${TWINE_USERNAME:-}" ] && args+=(--username "$TWINE_USERNAME")
  args+=(--password "$TWINE_PASSWORD")
fi

"$PYTHON" -m twine upload --verbose --non-interactive "${args[@]}" dist/*

echo "==> Successfully published px0 v$VERSION to $PYPI_REPOSITORY!"

TAG="v${VERSION#v}"
if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null 2>&1; then
  echo "==> Git tag $TAG already exists."
else
  echo "==> Creating git tag $TAG..."
  git tag -a "$TAG" -m "Release $TAG"
  echo "==> Created git tag $TAG"
fi
