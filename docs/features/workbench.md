# Review Workbench

Running `px0` with no arguments opens the **workbench**: one page for opening any directory on the machine or any pull request, and for getting back to what you opened before.

---

## Opening things

- **Type a path or paste a PR URL** into the box at the top and press Enter. Paths may be absolute, relative to the directory px0 was started in, or start with `~`. A path to a file opens its repository.
- **Folder completion.** As you type a path, folders whose names start with what you typed (ignoring case) are listed below the box, with a `git` tag on repositories. Press **Tab** (or click) to complete into the highlighted folder and list what is inside it, **↑/↓** to choose, **Enter** to open, **Esc** to dismiss. Hidden folders are listed once you type the leading dot. The Browse panel follows along.
- **Browse** the filesystem on the right. Folders that are git repositories carry a `git` tag. Click a folder to go into it, use **Open** on its row, or **Open this folder** for the one you are in. Tick **hidden** to list dot-folders.
- **Open \<cwd\>** opens the directory px0 was started in, which is what a bare `px0` did before the workbench.
- **Recent** lists every directory and pull request opened before, most recent first, including ones opened directly with `px0 <dir>` or `px0 <pr-url>`. Click a row to reopen it; Cmd/Ctrl-click opens it in a new tab.

Each workspace opens at its own address, `/w/<id>/`, on the workbench's port. The address is stable, so a bookmarked or reloaded review simply reopens. The ▦ button in the explorer header leads back to the workbench.

## Managing the list

Hover a row for its actions:

| Action | Effect |
| :--- | :--- |
| ☆ / ★ | Pin to the top of the list, or unpin. Pinned entries are never dropped. |
| ■ | Stop the running workspace and free its memory. Opening it again restarts it. |
| ✕ | Stop it and remove it from the list. |

A status dot shows workspaces that are **running**, **starting**, or **failed** (hover for the error). Up to 50 unpinned entries are kept.

---

## Keyboard Shortcuts & Controls

| Shortcut / Control | Context | Action |
| :--- | :--- | :--- |
| `/` | Workbench Page | Focus the path / PR URL input box |
| `Tab` | Path Input | Complete into the highlighted folder suggestion |
| `↑` / `↓` | Path Suggestions | Navigate up / down through folder suggestions |
| `Enter` | Path Input / Suggestion | Open the entered folder, PR URL, or highlighted suggestion |
| `Esc` | Path Suggestions / Theme | Dismiss folder suggestions or restore previous theme |
| `Cmd/Ctrl + Click` | Recent Workspaces | Open workspace in a new browser tab |
| `☆` / `★` | Workspace Row | Pin workspace to the top of the list, or unpin |
| `■` | Workspace Row | Stop running workspace child process and free RAM |
| `✕` | Workspace Row | Stop workspace and remove from list |
| `▦` | Workspace Header | Return to the Review Workbench from any open workspace |
| `Palette button` / `Arrow keys` | Status Bar | Preview and select workbench color theme |

## Pull requests

A pull request always opens as a `git worktree` of a local clone of its repository: a separate checkout that shares the clone's history, so opening a PR costs a checkout and never a fresh clone. The PR is checked out again each time it is opened, so it is always current, and the worktree is removed when the workspace stops. Your clone's own branch and working tree are not touched. Unsubmitted review drafts are kept across reopenings.

The first time you open a PR of a repository, px0 finds the clone:

1. a clone you used for that repository before;
2. one of your recent folders, or the folder px0 was started in;
3. a quick scan (a few levels deep, at most 1.5 seconds) of the usual code folders in your home directory (`~/workspace`, `~/code`, `~/src`, `~/projects`, `~/dev`, `~/repos`, `~/go/src/github.com`, ...) and the folders around your recent ones.

A clone matches when any of its remotes (`origin`, `upstream` for a fork, or another) points at the PR's repository. The clone is remembered, so every later PR of that repository opens straight away.

If no clone is found, the page asks. You can **use your clone**: type its path (with folder completion) or browse to it and press **Use as clone**. Or you can **let px0 clone it**: px0 makes one partial clone under `~/.px0/repos/<owner>/<repo>`, without a checkout of its own, and reuses it for every later PR of that repository.

A plain `px0 <pr-url>` run outside the repository uses the remembered clone too.

If a pull request cannot be opened (a typo in the URL, no access), the page explains why and the entry is left out of the list.

## Status bar and themes

The bottom bar matches the app's: logo and version, the number of running workspaces, and the **CPU · RAM · LSP** chip. Here the chip shows the workbench and every running workspace added together; click it for a breakdown by process. The palette button opens the theme list. Arrow keys preview a theme, Enter or a click keeps it, and Esc restores the one you had. The choice is saved to settings, so the workspaces use it too.

## Resources

Every workspace runs as its own px0 process with its own index and language servers. A workspace that has had no open tab for 30 minutes is stopped to free its memory; it restarts the next time it is opened. Stopping the workbench (Ctrl-C) stops every workspace and removes their PR checkouts.

Running `px0` again while a workbench is already running opens the existing one instead of starting a second.

Add `-r` (or `-reuse`) to `px0 <file or directory>` to reuse it: while a workbench is running, the target opens in it as a workspace (on the file and line you named) and the command exits, so opening many files from a shell or a file manager runs one server, not one each. If no workbench is running, `-r` first starts one in the background, with no window or terminal of its own; it keeps running until you stop that process (Task Manager, or `taskkill /im px0.exe` on Windows, `pkill px0` elsewhere). Without `-r`, `px0 <target>` starts a server of its own.

## Security

The workbench can list and open **any directory the px0 process can read**. On the default `-host 127.0.0.1` that is only you. With `-host 0.0.0.0`, anyone who can reach the address can browse the machine's folders and open any of them for reading and editing, and px0 prints a warning saying so. Expose it only over networks you trust. The same DNS rebinding and same-origin checks as the rest of px0 apply.

## Where state lives

What you opened, and which clone each repository's PRs use, is saved in `$XDG_STATE_HOME/px0/workbench.json`, falling back to `~/.px0/workbench.json`. Clones px0 makes live in `repos/` beside it. Nothing is written inside your repositories, apart from the temporary worktree registrations and `refs/px0/*` refs that px0 already uses for PR review and removes on exit.

## Flags

The workbench passes `-no-lsp`, `-no-git`, `-no-agent`, `-no-telemetry` and `-agent` on to every workspace it opens. `-host`, `-port`, `-base-path` and `-no-open` apply to the workbench itself. To open the current directory directly, as before, run `px0 .`. Use `px0 -r .` to open it in the running workbench instead.
