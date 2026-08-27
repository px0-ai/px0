# 3. Versioning and undo

Modules: `px0/versioning.py`, `px0/claims.py`, `px0/authoring.py`

px0 writes to files you own, sometimes on its own initiative. A workflow gets rebuilt by `px0 workflows improve`. A memory gets written because you corrected an answer. A guideline gets drafted during a build. An assistant that quietly accumulates edits to your files is the failure mode to design against, so every one of those edits is recorded, diffable, and revertible.

## What is versioned

`versioning._walk_versioned_files` covers all Markdown under `workflows/`, `guidelines/`, and `memory/`, plus `config.toml`.

`memory/` is in that list for a reason the others are not: px0 writes to it on its own initiative. What an assistant has come to believe about you should be as reviewable as anything you wrote yourself.

`brain/` is not versioned. It holds ingested material, which is large and already has a source recorded in its frontmatter. `output/` is not versioned either -- it is derived, and a run can produce it again.

## Storage

Two pieces: a content-addressed blob store and a SQLite manifest, both under `.state/versions/`.

```
.state/versions/
  objects/
    a3/a3f2...           zstd-compressed file content, named by sha256
  manifest.sqlite
```

`store_blob` hashes the content with sha256, writes it under a two-character fanout directory, and skips the write if a blob with that digest already exists. Two files with identical content share one blob; so does one file reverted to an earlier state. Compression is zstd.

The manifest holds four tables:

| Table | What it holds |
| ----- | ------------- |
| `files` | One row per path: its latest version, size, mtime, and whether it is deleted |
| `versions` | One row per `(path, version)`: the blob hash, the actor, the change it belonged to, a timestamp, a deletion flag, and free-text evidence |
| `changes` | One row per change: an id, an actor, a timestamp |
| `aliases` | Claim renames, covered below |

A version is an immutable snapshot of one file's bytes. A change groups the versions produced by one session. History is never rewritten: a revert writes a new version rather than removing an old one.

## Recording a change

`record_change(home, actor, file_changes)` takes a list of `FileChange(rel_path, content, evidence)`, where `content=None` means a deletion tombstone.

Two things it will not do:

It does not allocate a change id until it knows at least one file actually changed. A change that turns out to be a no-op leaves nothing in the log.

It does not record a version whose content matches the file's current latest version. That check is why the checkpoint scan can run on every command without filling the history with noise.

Change ids are `chg_YYYY-MM-DD-NNN`, sequential per day, allocated by reading the highest existing id for today. Human-readable and sortable, which matters because you retype them at `px0 changes revert`.

The `actor` field is how the log stays legible when several things write to the same files. Actors in use: `builder`, `health`, `breaker`, `improve`, `user:cli`, `user:manual`, and `memory:<who>`.

## Catching hand edits

The store is plain Markdown, so people edit it in an editor. `checkpoint_scan` is what makes those edits part of the history.

It walks every versioned file, compares size and mtime against the manifest, hashes only what differs, and records the differences as one change. A file the manifest knows about that is no longer on disk becomes a tombstone.

The mtime shortcut is what makes this cheap enough to run unconditionally. `cli._ctx` calls it before nearly every command, so editing a file and then asking `px0 changes list` about it shows the edit. The daemon's nightly pass runs it with `force_hash=True`, which skips the shortcut and catches what mtime tricks miss -- an editor that restores timestamps, or a file swapped for one of identical size.

If the scan raises, `_ctx` swallows it. Bookkeeping must never block the command the user actually ran; `px0 doctor` reports a wedged version store separately.

## Reverting

`revert_change(home, change_id, actor)` looks up every file the change touched, finds the version immediately before it, and restores that content -- or deletes the file, if the change created it.

The subtle part is `_write_to_disk`. `record_change` is a history writer and touches nothing in the working tree, which is right for capturing an edit that has already happened and wrong for a revert. Reverting used to record the old content as a new version and leave the file alone, so `px0 changes revert` reported success and changed nothing. The next checkpoint scan then captured the untouched file again and quietly discarded the revert.

`_write_to_disk` also confines the target inside the store before writing:

```python
target = (home / rel_path).resolve()
root = home.resolve()
if root != target and root not in target.parents:
    raise ValueError(f"refusing to write outside the store: {rel_path}")
```

Paths come out of the manifest, which is data. Data does not get to name a path outside the store.

## Diffs

`diff_versions` renders two versions of a file as a unified diff, treating a deleted version as empty content. `show_change` returns a change's metadata plus a per-file diff against each file's previous version, using a diff from `/dev/null` for a first version.

Both use `difflib` rather than shelling out to `diff`, so output is identical wherever px0 runs.

## Claims: section-level history

A guideline is a set of rules, and the rules are `##` headings. `claims.py` makes each heading individually addressable and individually traceable.

A claim id is `<path>#<heading-slug>`, for example `commit-messages.md#summary-line`. `parse_claim_id` validates that shape and raises `ClaimIdError` on anything else. Every consumer used to split on `#` bare, which turned a typo into an unpacking error and let a malformed alias into the table, where it broke lineage lookups for unrelated claims.

`extract_sections` splits a file into `Section` objects, each running from its heading to the next one. `guidelines_log(home, claim_id)` then walks the file's version chain and reports every version in which that section was present.

### Rename detection

Rename a heading and the naive reading is one claim deleted plus one claim created, which loses the history.

`detect_renames` compares the sections of two versions of a file. For each heading that disappeared, it scores the body against each heading that appeared, using token-level Jaccard similarity with inline code spans unwrapped and punctuation stripped. A pair scoring at or above `RENAME_THRESHOLD` (0.7) is recorded as a rename, not a deletion.

```python
def jaccard_similarity(a: str, b: str) -> float:
    ta, tb = _normalize_tokens(a), _normalize_tokens(b)
    if not ta and not tb:
        return 1.0
    if not ta or not tb:
        return 0.0
    return len(ta & tb) / len(ta | tb)
```

Renames go into the `aliases` table. `lineage_slugs` walks that graph in both directions, so a section renamed twice still reports its full history under any of its names. A malformed alias row -- written before `add_alias` validated its inputs -- is skipped rather than raised, because one bad row must not break lineage for every other claim.

`scan_and_process` is checkpoint scan plus rename detection over what it captured, and it is what most callers use. `capture_guideline_change` is the same pairing for the write path, so a guideline written by the builder gets history and aliases from its first version.

## Authoring operations

`authoring.py` exists because creating, renaming, copying, and deleting store files used to be things you did in a shell. That works for the bytes and not for what surrounds them.

| Function | What it records |
| -------- | --------------- |
| `write_file` | One version of the new content |
| `remove_file` | A tombstone, with the content still in the blob store so a revert works |
| `move_file` | One change with two entries: a tombstone on the old path and a version on the new one, so the log reads as a rename |
| `copy_file` | A new file, with evidence naming what it was copied from |

`check_id` validates a workflow or guideline id against `^[A-Za-z0-9][A-Za-z0-9._-]*$`, strips a trailing `.md`, and rejects anything containing `/` or `..`. An id becomes a filename and a command-line argument, so it must be both safe and unquotable.

`workflow_path` and `guideline_path` search recursively before falling back to the top-level path a new file would take. Both kinds of file may sit in subdirectories, and a guideline the builder filed under a folder has to be addressable by the name it was reported under.

`set_frontmatter_key` deserves a note. It rewrites one scalar key in place -- replacing the line if the key exists, appending it to the end of the block if not -- rather than round-tripping the YAML. A full round-trip would reformat the entire document to change one flag, and the rest of the file comes back byte for byte this way.

## Next

[Part 4](04-workflow-file.md) covers the file format that all of this history is kept for.
