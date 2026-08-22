#!/bin/bash
set -e

# px0 installer / uninstaller script

# Must match px0/retrieval.py's QMD_PINNED_VERSION -- px0 doctor hard-fails if
# the installed qmd version does not match what px0 itself is pinned to.
QMD_PINNED_VERSION="2.8.3"

# --- presentation --------------------------------------------------------
#
# Mirrors px0/ui.py's palette and glyphs (accent amber, ok green, err red,
# warn yellow, dim grey) so the first thing anyone sees from px0 already
# looks like every screen the CLI prints after it. Colour and animation are
# both off the moment output is not a terminal, so `curl ... | sh` in a log
# file stays plain and greppable.

_use_color=1
if [ -n "${NO_COLOR:-}" ] || [ "${TERM:-}" = "dumb" ] || [ ! -t 1 ]; then
    _use_color=0
fi
if [ -n "${FORCE_COLOR:-}" ]; then
    _use_color=1
fi

ACCENT=208 OK=71 ERR=167 WARN=179 DIM=245

_c() { # _c <256-colour-code> <text>
    if [ "$_use_color" = "1" ]; then
        printf '\033[38;5;%sm%s\033[0m' "$1" "$2"
    else
        printf '%s' "$2"
    fi
}
_bold() {
    if [ "$_use_color" = "1" ]; then
        printf '\033[1m%s\033[0m' "$1"
    else
        printf '%s' "$1"
    fi
}
_visible_len() { # strips escape sequences before counting, so padding stays correct
    local stripped
    stripped=$(printf '%s' "$1" | sed -E 's/\x1b\[[0-9;]*m//g')
    printf '%s' "${#stripped}"
}

if [ -t 1 ]; then
    G_OK="✓" G_ERR="✗" G_WARN="!" G_STEP="◇"
else
    G_OK="[OK]" G_ERR="[FAIL]" G_WARN="[WARN]" G_STEP="-"
fi

ok_line()   { printf '%s %s' "$(_c "$OK" "$G_OK")" "$1"; [ -n "${2:-}" ] && printf '  %s' "$(_c "$DIM" "$2")"; printf '\n'; }
err_line()  { printf '%s %s\n' "$(_c "$ERR" "$G_ERR")" "$1" >&2; }
warn_line() { printf '%s %s\n' "$(_c "$WARN" "$G_WARN")" "$1"; }
step_line() { printf '\n%s %s\n' "$(_c "$ACCENT" "$G_STEP")" "$(_bold "$1")"; }
hint_line() { printf '\n%s\n' "$(_c "$DIM" "$1")"; }
cmd_line()  { printf '  %s\n' "$(_c "$ACCENT" "$1")"; }

box() { # box <colour> <line>...  -- bordered block; lines may carry colour of their own
    local color="$1"; shift
    local max=0 len
    for l in "$@"; do
        len=$(_visible_len "$l")
        [ "$len" -gt "$max" ] && max=$len
    done
    local inner=$((max + 2))
    local rule; rule=$(printf -- '─%.0s' $(seq 1 "$inner"))
    printf '%s\n' "$(_c "$color" "╭${rule}╮")"
    for l in "$@"; do
        len=$(_visible_len "$l")
        local pad=$((max - len))
        printf '%s %s%*s %s\n' "$(_c "$color" "│")" "$l" "$pad" "" "$(_c "$color" "│")"
    done
    printf '%s\n' "$(_c "$color" "╰${rule}╯")"
}

banner() {
    box "$ACCENT" "$(_bold "px0")" "$(_c "$DIM" "Describe a job. px0 writes, runs, and schedules it.")"
}

# The Nth (0-8) braille spinner frame. A `case` over ten literal glyphs,
# rather than an array, because this script has to run under whatever `sh`
# resolves to when piped (`curl ... | sh`) -- often dash, which has no arrays.
_frame_at() {
    case $(($1 % 10)) in
        0) printf '⠋' ;; 1) printf '⠙' ;; 2) printf '⠹' ;; 3) printf '⠸' ;;
        4) printf '⠼' ;; 5) printf '⠴' ;; 6) printf '⠦' ;; 7) printf '⠧' ;;
        8) printf '⠇' ;; *) printf '⠏' ;;
    esac
}

# Runs "$@" with a braille spinner on a terminal, or a single "message..."
# line otherwise. Output is captured and only shown if the command fails, so
# a slow `pipx install` doesn't scroll its own progress bars over ours.
spin() {
    local msg="$1"; shift
    local log status pid i
    log=$(mktemp)
    status=0
    if [ "$_use_color" = "1" ] && [ -t 1 ]; then
        ("$@") >"$log" 2>&1 &
        pid=$!
        i=0
        while kill -0 "$pid" 2>/dev/null; do
            printf '\r%s %s' "$(_c "$ACCENT" "$(_frame_at "$i")")" "$msg"
            i=$((i + 1))
            sleep 0.08
        done
        if wait "$pid"; then status=0; else status=$?; fi
        printf '\r\033[K'
    else
        printf '%s...\n' "$msg"
        if "$@" >"$log" 2>&1; then status=0; else status=$?; fi
    fi
    if [ "$status" -eq 0 ]; then
        ok_line "$msg"
    else
        err_line "$msg failed"
        cat "$log" >&2
    fi
    rm -f "$log"
    return "$status"
}

# --- uninstall -------------------------------------------------------------

if [ "$1" = "--uninstall" ]; then
    banner
    if command -v px0 >/dev/null 2>&1; then
        hint_line "px0 uninstall removes px0 and your entire store (~/.px0) in one step."
        hint_line "This script only removes the package and the scheduler unit."
    fi

    step_line "Removing px0"
    # Either arm may fail (px0 not installed, pipx itself broken); uninstall is
    # idempotent by intent, so report and carry on rather than aborting on set -e.
    if command -v pipx >/dev/null 2>&1; then
        pipx uninstall px0 >/dev/null 2>&1 && ok_line "uninstalled" "via pipx" \
            || warn_line "pipx could not uninstall px0 (already gone?)"
    else
        python3 -m pipx uninstall px0 >/dev/null 2>&1 && ok_line "uninstalled" "via pipx" || true
    fi

    step_line "Removing the scheduler"
    # Removed before the binary goes: a launchd job with KeepAlive, or a
    # systemd service, otherwise keeps trying to run a px0 that is gone.
    removed_any=0
    PLIST="$HOME/Library/LaunchAgents/sh.px0.daemon.plist"
    if [ -f "$PLIST" ]; then
        launchctl unload "$PLIST" >/dev/null 2>&1 || true
        rm -f "$PLIST"
        ok_line "removed" "launchd unit"
        removed_any=1
    fi
    UNIT="$HOME/.config/systemd/user/px0d.service"
    if [ -f "$UNIT" ]; then
        systemctl --user disable --now px0d.service >/dev/null 2>&1 || true
        rm -f "$UNIT"
        systemctl --user daemon-reload >/dev/null 2>&1 || true
        ok_line "removed" "systemd unit"
        removed_any=1
    fi
    if [ "$removed_any" = "0" ]; then
        ok_line "nothing to remove" "no scheduler unit was installed"
    fi
    if crontab -l 2>/dev/null | grep -q "px0 workflows run"; then
        warn_line "px0 cron entries remain in your crontab; remove them with \`crontab -e\`"
    fi

    hint_line "to remove all local configuration and history, run:"
    cmd_line "rm -rf ~/.px0"
    exit 0
fi

# --- install -----------------------------------------------------------

banner

# Bootstrap pipx if missing
if ! command -v pipx >/dev/null 2>&1; then
    step_line "Setting up pipx"
    if ! pip_out=$(python3 -m pip install --user pipx 2>&1); then
        if echo "$pip_out" | grep -q "externally-managed-environment"; then
            # Debian/Ubuntu (PEP 668) refuses any --user install into the
            # system Python. pipx immediately isolates itself into its own
            # venv, which is exactly the case PEP 668 means --break-system-packages
            # for, so this override is safe even though px0 itself never uses it.
            python3 -m pip install --user --break-system-packages pipx
        else
            err_line "could not install pipx"
            printf '%s\n' "$pip_out" >&2
            exit 1
        fi
    fi
    python3 -m pipx ensurepath
    export PATH="$PATH:$HOME/.local/bin"
    ok_line "pipx ready"
fi

# Determine pipx env from PX0_PREFIX
if [ -n "$PX0_PREFIX" ]; then
    export PIPX_BIN_DIR="$PX0_PREFIX"
fi

# Build installation command
INSTALL_CMD="pipx install"
if [ "$PX0_CHANNEL" = "beta" ]; then
    INSTALL_CMD="$INSTALL_CMD --pip-args=\"--pre\""
fi

# Append package name with optional version pinning
if [ -n "$PX0_VERSION" ]; then
    INSTALL_CMD="$INSTALL_CMD px0==$PX0_VERSION"
else
    INSTALL_CMD="$INSTALL_CMD px0"
fi

step_line "Installing px0"
_run_install() { eval "$INSTALL_CMD"; }
spin "pipx install px0${PX0_VERSION:+==$PX0_VERSION}" _run_install

# Bootstrap qmd, px0's retrieval backend, if it is not already on PATH. Never
# touches an existing qmd -- version drift is px0 doctor's job to report, not
# this script's to silently "fix" by reinstalling over what's there.
if ! command -v qmd >/dev/null 2>&1; then
    step_line "Installing qmd"
    if ! command -v bun >/dev/null 2>&1; then
        _install_bun() { curl -fsSL https://bun.sh/install | bash; }
        spin "Installing bun" _install_bun
        export PATH="$PATH:$HOME/.bun/bin"
    fi
    if command -v bun >/dev/null 2>&1; then
        _install_qmd() { bun install -g "@tobilu/qmd@$QMD_PINNED_VERSION"; }
        spin "bun install -g @tobilu/qmd@$QMD_PINNED_VERSION" _install_qmd
    else
        warn_line "bun install did not complete; skipping qmd for now"
        hint_line "install it yourself once bun is on PATH:"
        cmd_line "bun install -g @tobilu/qmd@$QMD_PINNED_VERSION"
    fi
fi

# Initialize store
step_line "Setting up your store"
px0 init

# Daemon offer
# Only offer the daemon when there is a terminal to answer on. Under
# `curl ... | sh` stdin is the script itself, so `read` hits EOF and would
# abort the installer on set -e just before it prints success.
if [ "$PX0_NO_DAEMON" != "true" ] && [ -t 0 ]; then
    step_line "Scheduler"
    printf 'Install the px0 scheduler daemon now? %s ' "$(_c "$DIM" "[y/N]:")"
    if read -r ans && { [ "$ans" = "y" ] || [ "$ans" = "Y" ]; }; then
        px0 daemon install
    fi
elif [ "$PX0_NO_DAEMON" != "true" ]; then
    warn_line "not a terminal; skipping the daemon prompt"
    hint_line "enable it any time with:"
    cmd_line "px0 daemon install"
fi

box "$OK" "$(_bold "px0 is installed")" "$(_c "$DIM" "try these next:")" \
    "$(_c "$ACCENT" "px0 doctor")" \
    "$(_c "$ACCENT" "px0 workflows new")     $(_c "$DIM" "# describe a job, get a workflow")" \
    "$(_c "$ACCENT" "px0 workflows list")"
