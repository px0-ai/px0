# 16. Sync and portability

Modules: `px0/sync.py`, `px0/store.py`

Two machines, one set of workflows. This part covers why the obvious answer breaks and what px0 does instead.

## Why Dropbox on `~/.px0` is wrong

`px0 store export` and `import` copy a whole store, which is the right answer once -- setting up a second machine -- and the wrong answer every time after, because it overwrites.

So people did the obvious thing and pointed a folder-syncing tool at `~/.px0`. That has a specific, quiet failure: the version history is a SQLite database. Two machines writing it produce a file that is neither machine's history, and the damage is invisible until something needs to be reverted.

`sync.hazard(home)` detects that arrangement and says so. It looks for `.dropbox`, `.dropbox.cache`, and `.icloud` markers beside or inside the store, and for the tell-tale path components:

```python
for needle, name in (("dropbox", "Dropbox"), ("google drive", "Google Drive"),
                     ("onedrive", "OneDrive"),
                     ("mobile documents", "iCloud Drive")):
```

`px0 doctor` reports it. Nothing else would, until a revert needed the history.

## What `px0 store sync` does instead

The deliberate version of what people were doing anyway. Content is Markdown, so it merges file by file. History does not merge and is not pretended to: each machine keeps its own, and what crosses between them is the files plus a record of what came from where.

```python
SYNCED = ("workflows", "guidelines", "memory", "brain", "tools")
```

`.state/` is deliberately absent. It holds the version history, credentials, the retrieval index, and every queue -- all of them either machine-specific or unmergeable.

The remote is a directory. Whatever puts that directory on both machines -- Dropbox, iCloud, a git repo, a USB stick -- is the user's business and none of px0's.

## Three rules

Each one is a rule because the alternative loses work.

Nothing is overwritten silently. A file changed on both sides is written beside its neighbour as a conflict copy and reported. px0 is not in a position to know which version is right, and picking one would mean deleting the other.

A push never deletes. A file missing here may be one this machine has not pulled yet, and "absent" is not the same fact as "deleted".

The remote is a directory, not a service. There is no protocol, no daemon, and no account.

## Three-way agreement

The manifest at the remote records not only what the remote holds, but what each machine last agreed to, per file:

```json
{
  "files":    {"workflows/digest.md": {"hash": "...", "from": "laptop", "at": "..."}},
  "machines": {"laptop-a1b2": "2026-08-27T09:00:00Z"},
  "agreed":   {"laptop-a1b2": {"workflows/digest.md": "..."}}
}
```

The `agreed` map is what makes the whole thing work, and it cannot be replaced by the remote's own hash. Once a second machine pushes, the remote agrees with them, and a first machine reading it would conclude its own edit was the only change and quietly overwrite theirs.

With the third hash, `status` can distinguish four cases rather than guessing from timestamps:

```python
if mine == theirs:      unchanged
elif theirs is None:    push
elif mine is None:      pull
elif agreed is None:    conflict   # both have it, neither has ever agreed
elif mine == agreed:    pull       # only they moved
elif theirs == agreed:  push       # only we moved
else:                   conflict   # both moved since the last agreement
```

Timestamps are never compared across machines, whose clocks and filesystems do not agree.

### Settling

There is a fifth case hiding in `unchanged`. If both sides hold the same bytes but `agreed` records something older, they agree in fact and the record has not caught up. That happens when a user resolves a conflict by hand.

```python
if mine == theirs:
    unchanged.append(rel)
    if agreed != mine:
        settled.append(rel)
```

Without recording it, a resolved conflict stayed unresolved: `agreed` kept the pre-conflict hash, and the next ordinary edit on either side was reported as a conflict again, forever.

`settled` is written even on a push-only or pull-only sync, because it is a statement about what is true rather than about what moved.

## Store identity

A conflict file is labelled with the machine name, because a person reading it needs to know which version they are looking at and a random token would not tell them.

But the machine name alone is not enough as an identity, and not only in theory. Two stores on one machine -- a real one and one under `PX0_HOME` for testing -- would share an agreement record and each conclude the other's edits were its own.

So `store_key` is the machine name plus a token minted once per store and kept in `.state/`, which never travels. The token is only an identifier; what a person reads on a conflict file is still `laptop`.

## Conflict naming

```python
CONFLICT_MARKER = ".conflict-"
beside = path.with_name(f"{path.name}{CONFLICT_MARKER}{origin}")
```

The marker goes after the extension, not before it. `x.conflict-laptop.md` is still a `.md` file in `workflows/`, so px0 loaded it as a second workflow carrying the same `id:` -- and `load_all` keys by id, so which of the two you got depended on directory order.

Putting the marker last means no loader's `*.md` glob can see it, and the file still sits beside the one it disagrees with.

Conflict copies are excluded from `_walk`, so they are never themselves synced. So are dotfiles and anything ending `.sample` -- scaffolding every store has an identical copy of, which would create an opportunity for a conflict and carry no information.

## Creating the remote

`ensure_remote` creates the directory when its parent exists, and refuses when the parent does not.

The first sync is the common case, and `px0 store sync ~/Dropbox/px0-shared` failing until you go and `mkdir` it is a step with no purpose. A missing parent is what a typo actually looks like, and the difference matters: the wrong answer there is a store quietly syncing into a directory nothing else can see.

## Export, import, and the redaction problem

Covered in [part 2](02-store-and-config.md), with one point worth restating here because it generalizes.

"Credentials excluded" is not satisfied by skipping the credentials file. The Composio key is also in `config.toml`, and `config.toml` is versioned, so the key is in the history blobs too. An export that redacts only the live file leaves the secret one change-log entry away.

So export blanks the live keys, deletes `config.toml`'s rows from the exported manifest, and removes the blobs no surviving version still references. Blobs are content-addressed and shared, so the last step is a set difference:

```python
still_used = {r[0] for r in conn.execute(
    "SELECT DISTINCT hash FROM versions WHERE hash IS NOT NULL")}
for h in doomed - still_used:
    blob.unlink(missing_ok=True)
```

Any system with deduplicated content-addressed storage and a history has this problem. The lesson transfers.

## Choosing between them

| Situation | Use |
| --------- | --- |
| Setting up a second machine | `px0 store export` then `px0 store import` |
| Keeping two machines in step | `px0 store sync <shared dir>` |
| Backing up | `px0 store export` |
| Sharing workflows with someone else | `px0 store export`, then hand them the folder |

`--dry-run` on sync says what would move and stops. `--pull` and `--push` restrict the direction.

## Next

[Part 17](17-cli.md) covers the surface all of this is driven from.
