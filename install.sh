#!/usr/bin/env sh
# Universal installer script for px0 (https://px0.ai)
#
# Usage:
#   curl -fsSL https://px0.ai/install.sh | bash
#
# Environment variables:
#   VERSION      - target version to install (e.g. "0.1.0" or "latest", default: "latest")
#   INSTALL_DIR  - target directory for binary (default: ~/.local/bin, no sudo needed;
#                  an existing px0 in a writable directory on PATH is updated in place)
#   PX0_REPO     - GitHub repository (default: px0-ai/px0)

set -eu

REPO="${PX0_REPO:-px0-ai/px0}"
VERSION="${VERSION:-latest}"

# Color codes
BOLD="\033[1m"
GREEN="\033[38;5;71m"
AMBER="\033[38;5;208m"
RED="\033[38;5;167m"
RESET="\033[0m"

# %b so color codes embedded in messages are interpreted
log_info() {
  printf " ${GREEN}✓${RESET} %b\n" "$1"
}

log_step() {
  printf " ${AMBER}›${RESET} %b\n" "$1"
}

log_warn() {
  printf " ${AMBER}!${RESET} %b\n" "$1"
}

log_error() {
  printf " ${RED}✗${RESET} %b\n" "$1" >&2
}

# 1. Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux*)   OS="linux" ;;
  darwin*)  OS="darwin" ;;
  freebsd*) OS="freebsd" ;;
  openbsd*) OS="openbsd" ;;
  netbsd*)  OS="netbsd" ;;
  msys*|cygwin*|mingw*) OS="windows" ;;
  *)
    log_error "Unsupported operating system: $OS"
    exit 1
    ;;
esac

# 2. Detect Architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)    ARCH="amd64" ;;
  aarch64|arm64)   ARCH="arm64" ;;
  armv7*|armv6*|arm) ARCH="arm" ;;
  i386|i686)       ARCH="386" ;;
  riscv64)         ARCH="riscv64" ;;
  *)
    log_error "Unsupported machine architecture: $ARCH"
    exit 1
    ;;
esac

# 3. HTTP Client detection
fetch() {
  url="$1"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 10 --max-time 30 --retry 2 "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- -T 15 -t 3 "$url"
  else
    log_error "Neither curl nor wget found in PATH. Please install one of them."
    exit 1
  fi
}

fetch_file() {
  url="$1"
  out="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 10 --max-time 300 --retry 2 -o "$out" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -T 30 -t 3 -O "$out" "$url"
  fi
}

log_step "Detecting latest release for ${BOLD}${REPO}${RESET} (${OS}/${ARCH})..."

# 4. Resolve latest version if not explicitly provided
if [ "$VERSION" = "latest" ]; then
  RELEASE_JSON=$(fetch "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null || true)
  if [ -n "$RELEASE_JSON" ]; then
    VERSION=$(printf '%s' "$RELEASE_JSON" | grep '"tag_name":' | head -n 1 | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' | sed 's/^v//')
  fi
  # Fallback to VERSION file in master if release API returned empty/rate-limited
  if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
    VERSION=$(fetch "https://raw.githubusercontent.com/${REPO}/master/VERSION" 2>/dev/null | tr -d ' \r\n' || true)
  fi
fi

if [ -z "$VERSION" ]; then
  log_error "Could not determine target version. Check your network or specify VERSION=x.y.z."
  exit 1
fi

BINARY_EXT=""
[ "$OS" = "windows" ] && BINARY_EXT=".exe"

# 5. Determine installation target directory
# Install at user level by default so no sudo is needed.
EXISTING_BIN="$(command -v px0 2>/dev/null || true)"
case "$EXISTING_BIN" in
  /*) EXISTING_DIR="$(dirname "$EXISTING_BIN")" ;;
  *)  EXISTING_BIN=""; EXISTING_DIR="" ;;
esac

if [ -n "${INSTALL_DIR:-}" ]; then
  TARGET_DIR="$INSTALL_DIR"
  # sudo is only used when the user explicitly chose a directory they cannot write to
  if [ -d "$TARGET_DIR" ] && [ ! -w "$TARGET_DIR" ] && command -v sudo >/dev/null 2>&1; then
    USE_SUDO=1
  fi
elif [ -n "$EXISTING_DIR" ] && [ -w "$EXISTING_DIR" ]; then
  # Update an existing install in place instead of leaving a second copy on PATH
  TARGET_DIR="$EXISTING_DIR"
else
  TARGET_DIR="${HOME}/.local/bin"
fi

TARGET_BIN="${TARGET_DIR}/px0${BINARY_EXT}"

# 6. Check existing installation and version
ACTION="Installed"
if [ -f "$TARGET_BIN" ] || [ -L "$TARGET_BIN" ]; then
  CURRENT_VER=""
  if [ -x "$TARGET_BIN" ]; then
    CURRENT_VER=$("$TARGET_BIN" -version 2>&1 | grep -oE "[0-9]+\.[0-9]+(\.[0-9]+)?" | head -n 1 || true)
    if [ -z "$CURRENT_VER" ]; then
      CURRENT_VER=$("$TARGET_BIN" --version 2>&1 | grep -oE "[0-9]+\.[0-9]+(\.[0-9]+)?" | head -n 1 || true)
    fi
    if [ -z "$CURRENT_VER" ]; then
      CURRENT_VER=$("$TARGET_BIN" version 2>&1 | grep -oE "[0-9]+\.[0-9]+(\.[0-9]+)?" | head -n 1 || true)
    fi
  fi

  if [ -n "$CURRENT_VER" ]; then
    log_step "Found existing px0 v${CURRENT_VER} at ${BOLD}${TARGET_BIN}${RESET}"
    ACTION="Updated"
  else
    log_step "Replacing existing binary at ${BOLD}${TARGET_BIN}${RESET}..."
    ACTION="Replaced"
  fi
fi

BINARY_NAME="px0-${VERSION}-${OS}-${ARCH}${BINARY_EXT}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/v${VERSION}/${BINARY_NAME}"

# Fallback download url without 'v' prefix in tag if needed
FALLBACK_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY_NAME}"

TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t 'px0install')"
trap 'rm -rf "$TMP_DIR"' EXIT

log_step "Downloading ${BINARY_NAME}..."
TMP_FILE="${TMP_DIR}/px0"

if ! fetch_file "$DOWNLOAD_URL" "$TMP_FILE" 2>/dev/null; then
  if ! fetch_file "$FALLBACK_URL" "$TMP_FILE" 2>/dev/null; then
    log_error "Failed to download binary from $DOWNLOAD_URL"
    log_warn "If this version is newly tagged, the GitHub Action release build may still be compiling."
    exit 1
  fi
fi

chmod +x "$TMP_FILE"

# 7. Install / Replace binary
log_step "Installing to ${BOLD}${TARGET_BIN}${RESET}..."

if [ "${USE_SUDO:-0}" = "1" ]; then
  sudo rm -f "$TARGET_BIN"
  sudo mv "$TMP_FILE" "$TARGET_BIN"
  sudo chmod 755 "$TARGET_BIN"
else
  mkdir -p "$TARGET_DIR"
  rm -f "$TARGET_BIN"
  mv "$TMP_FILE" "$TARGET_BIN"
  chmod 755 "$TARGET_BIN"
fi

if [ "$ACTION" = "Updated" ]; then
  log_info "px0 successfully updated (v${CURRENT_VER} -> v${VERSION})!"
else
  log_info "px0 v${VERSION} installed successfully!"
fi

# An older px0 we could not update (e.g. a root-owned /usr/local/bin/px0) may shadow the new one
if [ -n "$EXISTING_BIN" ] && [ "$EXISTING_BIN" != "$TARGET_BIN" ]; then
  log_warn "Another px0 is installed at ${EXISTING_BIN} and may take precedence on your PATH."
  RM_CMD="rm"
  [ -w "$EXISTING_DIR" ] || RM_CMD="sudo rm"
  printf "   Remove it with:\n"
  printf "     %s %s\n\n" "$RM_CMD" "$EXISTING_BIN"
fi

# Check if TARGET_DIR is in PATH
PATH_ALREADY_CONFIGURED=0
case ":$PATH:" in
  *":$TARGET_DIR:"*) PATH_ALREADY_CONFIGURED=1 ;;
esac

# 8. Shell profile configuration (PATH)
detect_shell_rc() {
  case "$(basename "${SHELL:-}")" in
    zsh)
      echo "${HOME}/.zshrc"
      ;;
    bash)
      if [ -f "${HOME}/.bashrc" ]; then
        echo "${HOME}/.bashrc"
      elif [ -f "${HOME}/.bash_profile" ]; then
        echo "${HOME}/.bash_profile"
      else
        echo "${HOME}/.profile"
      fi
      ;;
    fish)
      echo "${HOME}/.config/fish/config.fish"
      ;;
    *)
      if [ -f "${HOME}/.zshrc" ]; then
        echo "${HOME}/.zshrc"
      elif [ -f "${HOME}/.bashrc" ]; then
        echo "${HOME}/.bashrc"
      elif [ -f "${HOME}/.bash_profile" ]; then
        echo "${HOME}/.bash_profile"
      elif [ -f "${HOME}/.profile" ]; then
        echo "${HOME}/.profile"
      else
        echo ""
      fi
      ;;
  esac
}

SHELL_RC="$(detect_shell_rc)"

# Check if PATH is already exported in profile
has_path_configured() {
  rc="$1"
  dir="$2"
  [ -f "$rc" ] && grep -Fq "$dir" "$rc"
}

# Auto-add TARGET_DIR to PATH in shell profile if not in current PATH and not already written
if [ "$PATH_ALREADY_CONFIGURED" = "0" ]; then
  log_warn "${TARGET_DIR} is not in your current PATH."
  if [ -n "$SHELL_RC" ]; then
    if has_path_configured "$SHELL_RC" "$TARGET_DIR"; then
      log_info "PATH addition already present in ${SHELL_RC}."
    else
      printf "\n# Added by px0 installer\nexport PATH=\"%s:\$PATH\"\n" "$TARGET_DIR" >> "$SHELL_RC"
      log_info "Added ${TARGET_DIR} to PATH in ${SHELL_RC}"
    fi
  else
    printf "   Add it by running:\n"
    printf "     export PATH=\"%s:\$PATH\"\n\n" "$TARGET_DIR"
  fi
fi

printf "\nRun %b to inspect any repository:\n" "${BOLD}px0${RESET}"
printf "  ${AMBER}px0 .${RESET}\n\n"
