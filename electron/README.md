# px0 app mode

A small Electron shell that runs the px0 server and shows it in its own window,
titled `px0 - <reponame>` (also what the window switcher displays).

```bash
make build                 # builds ./px0 at the repo root
cd electron && npm install
npx electron . [px0 args]  # e.g. npx electron . ~/code/myrepo, or a PR URL
```

- The `px0` binary is found via `$PX0_BIN`, bundled resources, `../px0`, then `$PATH`.
- Arguments are passed straight to px0. `--no-open --no-color --port 0` are added.
- Closing the window stops the server.
- Repo name comes from the PR URL if one is given, otherwise the workspace directory name.

## Install

```bash
./electron/install.sh      # installs a `px0` launcher into ~/.local/bin
cd ~/code/myrepo && px0    # opens a window on the current directory
```

The launcher runs the app detached from the terminal (log: `$TMPDIR/px0-app.log`) and passes
`--version`/`--update`/`--help` to the plain px0 binary. App files live in `~/.local/share/px0-app`.
An existing `~/.local/bin/px0` that isn't ours is moved to `px0.bak`.
