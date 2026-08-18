#!/bin/bash
set -e

# px0 installer / uninstaller script

# Detect uninstall
if [ "$1" = "--uninstall" ]; then
    echo "Uninstalling px0..."
    if command -v pipx >/dev/null 2>&1; then
        pipx uninstall px0
    else
        python3 -m pipx uninstall px0 >/dev/null 2>&1 || true
    fi
    echo "To remove all local configurations and history, run:"
    echo "  rm -rf ~/.px0"
    exit 0
fi

# Bootstrap pipx if missing
if ! command -v pipx >/dev/null 2>&1; then
    echo "pipx not found. Bootstrapping pipx..."
    python3 -m pip install --user pipx
    python3 -m pipx ensurepath
    export PATH="$PATH:$HOME/.local/bin"
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

echo "Running install: $INSTALL_CMD"
eval "$INSTALL_CMD"

# Initialize store
echo "Initializing px0 store..."
px0 init

# Daemon offer
if [ "$PX0_NO_DAEMON" != "true" ]; then
    echo -n "Install the px0 scheduler daemon now? [y/N]: "
    read -r ans
    if [ "$ans" = "y" ] || [ "$ans" = "Y" ]; then
        px0 daemon install
    fi
fi

echo ""
echo "px0 has been installed successfully!"
echo "Try running these next:"
echo "  px0 list workflows"
echo "  px0 doctor"
echo "  px0 run pr-precheck --stdin < some.diff"
