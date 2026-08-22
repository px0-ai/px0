#!/bin/sh

# px0 installer / uninstaller
#
# Supported:
#   sh install.sh
#   ./install.sh
#   curl -fsSL <URL> | sh
#
# Optional environment variables:
#   PX0_VERSION       Install a specific px0 version
#   PX0_CHANNEL       beta
#   PX0_PREFIX        Directory for px0 binary
#   PX0_NO_DAEMON     true
#   NO_COLOR          Disable colour
#   FORCE_COLOR       Force colour

set -u

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

QMD_PINNED_VERSION="2.8.3"

ACCENT=208
OK=71
ERR=167
WARN=179
DIM=245

# ---------------------------------------------------------------------------
# Presentation
# ---------------------------------------------------------------------------

_use_color=1

if [ -n "${NO_COLOR:-}" ] || [ "${TERM:-}" = "dumb" ] || [ ! -t 1 ]; then
    _use_color=0
fi

if [ -n "${FORCE_COLOR:-}" ]; then
    _use_color=1
fi

_c() {
    code="$1"
    text="$2"

    if [ "$_use_color" = "1" ]; then
        printf '\033[38;5;%sm%s\033[0m' "$code" "$text"
    else
        printf '%s' "$text"
    fi
}

_bold() {
    text="$1"

    if [ "$_use_color" = "1" ]; then
        printf '\033[1m%s\033[0m' "$text"
    else
        printf '%s' "$text"
    fi
}

if [ -t 1 ]; then
    G_OK="✓"
    G_ERR="✗"
    G_WARN="!"
    G_STEP="◇"
else
    G_OK="[OK]"
    G_ERR="[FAIL]"
    G_WARN="[WARN]"
    G_STEP="-"
fi

ok_msg() {
    message="$1"
    detail="${2:-}"

    printf '%s %s' "$(_c "$OK" "$G_OK")" "$message"

    if [ -n "$detail" ]; then
        printf '  %s' "$(_c "$DIM" "$detail")"
    fi

    printf '\n'
}

err_line() {
    printf '%s %s\n' "$(_c "$ERR" "$G_ERR")" "$1" >&2
}

warn_line() {
    printf '%s %s\n' "$(_c "$WARN" "$G_WARN")" "$1"
}

step_line() {
    printf '\n%s %s\n' "$(_c "$ACCENT" "$G_STEP")" "$(_bold "$1")"
}

hint_line() {
    printf '\n%s\n' "$(_c "$DIM" "$1")"
}

cmd_line() {
    printf '  %s\n' "$(_c "$ACCENT" "$1")"
}

banner() {
    printf '\n'

    if [ "$_use_color" = "1" ]; then
        printf '\033[48;5;%sm\033[1;30m %s \033[0m' "$ACCENT" "px0"
    else
        printf 'px0'
    fi

    printf ' %s\n\n' "$(_c "$DIM" "- an agent that works the way you work.")"
}

# ---------------------------------------------------------------------------
# Error handling
# ---------------------------------------------------------------------------

die() {
    err_line "$1"
    exit "${2:-1}"
}

# ---------------------------------------------------------------------------
# Path helpers
# ---------------------------------------------------------------------------

add_path_if_missing() {
    directory="$1"

    case ":${PATH:-}:" in
        *":$directory:"*)
            ;;
        *)
            PATH="${PATH:-}:$directory"
            export PATH
            ;;
    esac
}

# ---------------------------------------------------------------------------
# Python version helpers
# ---------------------------------------------------------------------------

python_version_ok() {
    python_bin="$1"

    version="$(
        "$python_bin" -c '
import sys
print("%d.%d" % (sys.version_info[0], sys.version_info[1]))
' 2>/dev/null
    )" || return 1

    major="${version%%.*}"
    minor="${version#*.}"

    if [ "$major" -gt 3 ]; then
        return 0
    fi

    if [ "$major" -eq 3 ] && [ "$minor" -ge 11 ]; then
        return 0
    fi

    return 1
}

find_supported_python() {
    # Prefer newer versions when available.
    for candidate in \
        python3.13 \
        python3.12 \
        python3.11 \
        python3
    do
        if command -v "$candidate" >/dev/null 2>&1 &&
           python_version_ok "$(command -v "$candidate")"; then
            command -v "$candidate"
            return 0
        fi
    done

    return 1
}

# ---------------------------------------------------------------------------
# Sudo helpers
# ---------------------------------------------------------------------------

# sudo reads its password prompt from /dev/tty, not from stdin, so a piped
# install (`curl ... | sh`) -- where stdin is the script itself -- can still
# prompt interactively as long as a real terminal is attached. Only fall
# back to non-interactive sudo when there is truly no tty to prompt on,
# otherwise a required password silently fails the whole install.
resolve_sudo() {
    SUDO=""

    if [ "$(id -u)" -eq 0 ] || ! command -v sudo >/dev/null 2>&1; then
        return 0
    fi

    if [ -r /dev/tty ] && [ -w /dev/tty ]; then
        SUDO="sudo"
    else
        SUDO="sudo -n"
    fi
}

# ---------------------------------------------------------------------------
# Package manager helpers
# ---------------------------------------------------------------------------

pkg_install_other() {
    dnf_package="$1"
    yum_package="$2"
    pacman_package="$3"
    apk_package="$4"
    brew_package="$5"

    if command -v dnf >/dev/null 2>&1; then
        $SUDO dnf install -y "$dnf_package"
        return $?
    fi

    if command -v yum >/dev/null 2>&1; then
        $SUDO yum install -y "$yum_package"
        return $?
    fi

    if command -v pacman >/dev/null 2>&1; then
        $SUDO pacman -Sy --noconfirm "$pacman_package"
        return $?
    fi

    if command -v apk >/dev/null 2>&1; then
        $SUDO apk add "$apk_package"
        return $?
    fi

    if command -v brew >/dev/null 2>&1; then
        brew install "$brew_package"
        return $?
    fi

    return 1
}

# ---------------------------------------------------------------------------
# Find / install Python 3.11+
# ---------------------------------------------------------------------------

PYTHON_BIN=""

if PYTHON_FOUND="$(find_supported_python 2>/dev/null)"; then
    PYTHON_BIN="$PYTHON_FOUND"
else
    resolve_sudo

    step_line "Installing Python 3.11+"

    # macOS / Homebrew
    if command -v brew >/dev/null 2>&1; then
        if brew install python@3.12 >/dev/null 2>&1; then
            BREW_PYTHON="$(brew --prefix python@3.12 2>/dev/null)/bin/python3.12"

            if [ -x "$BREW_PYTHON" ] &&
               python_version_ok "$BREW_PYTHON"; then
                PYTHON_BIN="$BREW_PYTHON"
            fi
        fi
    fi

    # Debian / Ubuntu
    if [ -z "$PYTHON_BIN" ] &&
       command -v apt-get >/dev/null 2>&1; then

        if $SUDO apt-get update -qq >/dev/null 2>&1 &&
           $SUDO apt-get install -y python3.11 python3.11-venv >/dev/null 2>&1; then

            if command -v python3.11 >/dev/null 2>&1 &&
               python_version_ok "$(command -v python3.11)"; then
                PYTHON_BIN="$(command -v python3.11)"
            fi
        fi
    fi

    # Fedora / RHEL
    if [ -z "$PYTHON_BIN" ] &&
       command -v dnf >/dev/null 2>&1; then

        if $SUDO dnf install -y python3.11 >/dev/null 2>&1; then
            if command -v python3.11 >/dev/null 2>&1 &&
               python_version_ok "$(command -v python3.11)"; then
                PYTHON_BIN="$(command -v python3.11)"
            fi
        fi
    fi

    # Arch / Alpine / other RHEL derivatives without their own python3.11
    # package -- fall back to whatever "python3"-ish package each package
    # manager ships (typically already 3.11+ on rolling-release distros).
    if [ -z "$PYTHON_BIN" ] &&
       pkg_install_other python3 python3 python python3 python@3.12 >/dev/null 2>&1; then

        if command -v python3 >/dev/null 2>&1 &&
           python_version_ok "$(command -v python3)"; then
            PYTHON_BIN="$(command -v python3)"
        fi
    fi

    if [ -z "$PYTHON_BIN" ]; then
        err_line "px0 requires Python 3.11 or newer"

        if command -v python3 >/dev/null 2>&1; then
            printf 'Detected: %s\n' "$(python3 --version 2>&1)" >&2
        fi

        hint_line "Install Python 3.11+ and run this installer again."

        if command -v brew >/dev/null 2>&1; then
            cmd_line "brew install python@3.12"
        elif command -v apt-get >/dev/null 2>&1; then
            cmd_line "sudo apt-get install python3.11 python3.11-venv"
        elif command -v dnf >/dev/null 2>&1; then
            cmd_line "sudo dnf install python3.11"
        elif command -v pacman >/dev/null 2>&1; then
            cmd_line "sudo pacman -S python"
        elif command -v apk >/dev/null 2>&1; then
            cmd_line "sudo apk add python3"
        fi

        exit 1
    fi
fi

ok_msg "Python ready" "$("$PYTHON_BIN" --version 2>&1)"

# ---------------------------------------------------------------------------
# Ensure pip exists
# ---------------------------------------------------------------------------

if ! "$PYTHON_BIN" -m pip --version >/dev/null 2>&1; then
    step_line "Bootstrapping pip"

    if ! "$PYTHON_BIN" -m ensurepip --upgrade >/dev/null 2>&1; then
        # Debian/Ubuntu disable ensurepip for the system Python and require
        # installing pip via the OS package manager instead.
        resolve_sudo

        if command -v apt-get >/dev/null 2>&1; then
            apt_out="$($SUDO apt-get install -y python3-pip python3-venv 2>&1)" || {
                warn_line "could not install python3-pip via apt-get"
                printf '%s\n' "$apt_out" >&2
            }
        else
            pkg_install_other python3-pip python3-pip python-pip py3-pip python@3.12 \
                >/dev/null 2>&1 || true
        fi
    fi
fi

if ! "$PYTHON_BIN" -m pip --version >/dev/null 2>&1; then
    err_line "pip is not available for $PYTHON_BIN"

    hint_line "install it yourself, then re-run this script:"

    if command -v apt-get >/dev/null 2>&1; then
        cmd_line "sudo apt-get install python3-pip python3-venv"
    elif command -v dnf >/dev/null 2>&1; then
        cmd_line "sudo dnf install python3-pip"
    elif command -v pacman >/dev/null 2>&1; then
        cmd_line "sudo pacman -S python-pip"
    elif command -v apk >/dev/null 2>&1; then
        cmd_line "sudo apk add py3-pip"
    elif command -v brew >/dev/null 2>&1; then
        cmd_line "brew reinstall python@3.12"
    fi

    exit 1
fi

# ---------------------------------------------------------------------------
# Install / locate pipx
# ---------------------------------------------------------------------------

PIPX_BIN=""

add_path_if_missing "$HOME/.local/bin"

if [ -x "$HOME/.local/bin/pipx" ]; then
    PIPX_BIN="$HOME/.local/bin/pipx"
elif command -v pipx >/dev/null 2>&1; then
    PIPX_BIN="$(command -v pipx)"
fi

# We deliberately use the selected Python to install pipx if we cannot find it.
if [ -z "$PIPX_BIN" ]; then
    step_line "Installing pipx"

    pipx_installed=0

    if "$PYTHON_BIN" -m pip install --user pipx >/dev/null 2>&1; then
        pipx_installed=1
    fi

    # PEP 668 fallback.
    if [ "$pipx_installed" -eq 0 ]; then
        if "$PYTHON_BIN" -m pip install \
            --user \
            --break-system-packages \
            pipx >/dev/null 2>&1; then
            pipx_installed=1
        fi
    fi

    add_path_if_missing "$HOME/.local/bin"

    if [ -x "$HOME/.local/bin/pipx" ]; then
        PIPX_BIN="$HOME/.local/bin/pipx"
    elif command -v pipx >/dev/null 2>&1; then
        PIPX_BIN="$(command -v pipx)"
    elif "$PYTHON_BIN" -m pipx --version >/dev/null 2>&1; then
        # Keep this as a fallback. Most installations expose a pipx binary.
        PIPX_BIN="$PYTHON_BIN -m pipx"
    fi

    if [ -z "$PIPX_BIN" ]; then
        die "pipx could not be installed"
    fi

    ok_msg "pipx ready"
fi

# ---------------------------------------------------------------------------
# Configure pipx paths
# ---------------------------------------------------------------------------

if [ -n "${PX0_PREFIX:-}" ]; then
    export PIPX_BIN_DIR="$PX0_PREFIX"
else
    export PIPX_BIN_DIR="$HOME/.local/bin"
fi

add_path_if_missing "$PIPX_BIN_DIR"

# ---------------------------------------------------------------------------
# Install px0
# ---------------------------------------------------------------------------

PX0_PACKAGE="px0"

if [ -n "${PX0_VERSION:-}" ]; then
    PX0_PACKAGE="px0==$PX0_VERSION"
fi

step_line "Installing px0"

# Use the explicitly selected modern Python.
#
# This is the important fix:
#   pipx install --python "$PYTHON_BIN" px0
#
# It prevents pipx from creating the px0 environment with an older system
# Python such as 3.9 or 3.10.

if [ "${PX0_CHANNEL:-}" = "beta" ]; then
    if [ -x "$PIPX_BIN" ]; then
        if ! "$PIPX_BIN" install \
            --python "$PYTHON_BIN" \
            --pip-args "--pre" \
            "$PX0_PACKAGE"; then

            if ! "$PIPX_BIN" upgrade \
                --python "$PYTHON_BIN" \
                --pip-args "--pre" \
                px0 >/dev/null 2>&1; then
                die "failed to install px0"
            fi
        fi
    else
        if ! sh -c "$PIPX_BIN install --python \"\$1\" --pip-args \"--pre\" \"\$2\"" \
            sh "$PYTHON_BIN" "$PX0_PACKAGE"; then

            if ! sh -c "$PIPX_BIN upgrade --python \"\$1\" --pip-args \"--pre\" px0" \
                sh "$PYTHON_BIN" >/dev/null 2>&1; then
                die "failed to install px0"
            fi
        fi
    fi
else
    if [ -x "$PIPX_BIN" ]; then
        if ! "$PIPX_BIN" install \
            --python "$PYTHON_BIN" \
            "$PX0_PACKAGE"; then

            if ! "$PIPX_BIN" upgrade \
                --python "$PYTHON_BIN" \
                px0 >/dev/null 2>&1; then
                die "failed to install px0"
            fi
        fi
    else
        if ! sh -c "$PIPX_BIN install --python \"\$1\" \"\$2\"" \
            sh "$PYTHON_BIN" "$PX0_PACKAGE"; then

            if ! sh -c "$PIPX_BIN upgrade --python \"\$1\" px0" \
                sh "$PYTHON_BIN" >/dev/null 2>&1; then
                die "failed to install px0"
            fi
        fi
    fi
fi

ok_msg "px0 package installed"

# ---------------------------------------------------------------------------
# Locate px0 binary
# ---------------------------------------------------------------------------

PX0_BIN="$PIPX_BIN_DIR/px0"

if [ ! -x "$PX0_BIN" ]; then
    if command -v px0 >/dev/null 2>&1; then
        PX0_BIN="$(command -v px0)"
    elif [ -x "$HOME/.local/bin/px0" ]; then
        PX0_BIN="$HOME/.local/bin/px0"
    else
        die "px0 installed successfully but the executable could not be found"
    fi
fi

# ---------------------------------------------------------------------------
# Bootstrap qmd
# ---------------------------------------------------------------------------

if ! command -v qmd >/dev/null 2>&1; then

    if ! command -v bun >/dev/null 2>&1; then
        step_line "Installing bun"

        if ! command -v curl >/dev/null 2>&1; then
            warn_line "curl is not installed; cannot install bun"
        elif ! command -v bash >/dev/null 2>&1; then
            warn_line "bash is not installed; cannot install bun"
        else
            if curl -fsSL https://bun.sh/install | bash >/dev/null 2>&1; then
                ok_msg "bun installed"
            else
                warn_line "bun installation did not complete"
            fi
        fi

        add_path_if_missing "$HOME/.bun/bin"
    fi

    if command -v bun >/dev/null 2>&1; then
        step_line "Installing qmd"

        if bun install -g "@tobilu/qmd@$QMD_PINNED_VERSION" >/dev/null 2>&1; then
            ok_msg "qmd installed" "$QMD_PINNED_VERSION"
        else
            warn_line "qmd installation did not complete"
            hint_line "install it manually with:"
            cmd_line "bun install -g @tobilu/qmd@$QMD_PINNED_VERSION"
        fi
    else
        warn_line "bun is unavailable; skipping qmd"
        hint_line "install it manually with:"
        cmd_line "bun install -g @tobilu/qmd@$QMD_PINNED_VERSION"
    fi
fi

# ---------------------------------------------------------------------------
# Scheduler
# ---------------------------------------------------------------------------

if [ "${PX0_NO_DAEMON:-}" != "true" ] && [ -t 0 ]; then
    step_line "Scheduler"

    printf 'Install the px0 scheduler daemon now? %s ' \
        "$(_c "$DIM" "[y/N]:")"

    ans=""

    if read -r ans; then
        case "$ans" in
            y|Y)
                if "$PX0_BIN" daemon install; then
                    ok_msg "scheduler installed"
                else
                    warn_line "scheduler installation failed"
                    hint_line "you can retry with:"
                    cmd_line "px0 daemon install"
                fi
                ;;
            *)
                ;;
        esac
    fi

elif [ "${PX0_NO_DAEMON:-}" != "true" ]; then
    warn_line "not a terminal; skipping the daemon prompt"
    hint_line "enable it any time with:"
    cmd_line "px0 daemon install"
fi

# ---------------------------------------------------------------------------
# Success
# ---------------------------------------------------------------------------

ok_msg "$(_bold "px0 is installed")"

hint_line "try these next:"
cmd_line "px0 init"
cmd_line "px0 workflows new"

printf '\n'
