# Publishing & Release Guide

This document outlines the step-by-step instructions for preparing, testing, and publishing a new release of `px0`.

## 1. Prerequisites

Before cutting a new release, ensure you have:

- Git with push and tag permissions for `px0-ai/px0`.
- Go (version 1.24+).
- Node.js 22.7+ or Bun for bundling frontend assets. The bundler is an ES module without a `package.json`, so older Node needs `node --experimental-detect-module ./scripts/build-web.js`. When `web/src/` is untouched, the committed `web/app.js` is current and `go build -o px0 .` alone is enough.
- A clean working tree with all tests passing.

## 2. Pre-Release Verification

Run the test suite and verify asset bundling:

```bash
# 1. Run unit and integration tests
make test

# 2. Build local binary and verify sanity
make build
./px0 --version
```

## 3. Release Methods
### Method 1: Automated Release via Makefile (Recommended)

The project includes an automated `make publish` target in the [Makefile](Makefile) that:

1. Updates the [VERSION](VERSION) file.
1. Bundles web assets and cross-compiles binaries into `dist/`.
1. Commits `VERSION`.
1. Creates an annotated Git tag `v<version>`.

#### Step 1: Run publish command

Specify the version without a leading `v`:

```bash
make publish 0.2.0
```

#### Step 2: Push commit and tags to GitHub

```bash
git push origin master --tags
```

### Method 2: Manual Release via Git

```bash
# 1. Update VERSION file
echo "0.2.0" > VERSION

# 2. Bundle frontend assets and compile release binaries
make dist

# 3. Commit version bump
git add VERSION
git commit -m "Release v0.2.0"

# 4. Tag the release
git tag -fa v0.2.0 -m "Release v0.2.0"

# 5. Push to GitHub
git push origin master
git push origin v0.2.0
```

### Method 3: GitHub Actions Workflow Dispatch

1. Navigate to Actions -> Release on GitHub: `https://github.com/px0-ai/px0/actions/workflows/release.yml`
1. Click Run workflow.
1. Enter the version tag (e.g. `v0.2.0`).
1. Trigger the workflow.

## 4. Post-Release Verification

1. Verify GitHub Actions workflow completion on the Actions tab.
1. Confirm artifacts on the [Releases](https://github.com/px0-ai/px0/releases) page (cross-platform binaries and `checksums.txt`). The self-updater requires this file and verifies the selected binary against it before execution.
1. Verify the installer script:
  ```bash
  curl -fsSL https://px0.ai/install.sh | sh
  ```
1. Verify self-update functionality:
  ```bash
  px0 --update
  ```
