# `px0 brain`

The brain is what you have read and kept: a folder of Markdown that px0 ingests
into, indexes, and answers questions from. It can be px0's own `brain/` folder or
any folder of Markdown you already keep — including an Obsidian vault.

Implemented by `px0/brain.py` (ingestion), `px0/retrieval.py` (indexing and
search), and `px0/ask.py` (retrieval plus generation).

```
px0 brain add <source> [--to FOLDER] [--from-file PATH] [--no-propose]
px0 brain refresh [path] [--all] [--stale] [--days N] [--no-propose]
px0 brain list
px0 brain show <path> [--json]
px0 brain rm <path> [--yes]
px0 brain export <dir> [--include-private]
px0 brain search <query> [--k N] [--kind KIND] [--json]
px0 brain ask <question> [--k N] [--kind KIND] [--sources]
px0 brain reindex
```

---

## `px0 brain add`

Ingest one source: extract its text, write it as Markdown with frontmatter
recording where it came from, propose guideline edits from it, and reindex.

Extraction always runs locally and needs no API key.

### `source` (required)

What to ingest.

- **Input:** a URL, a YouTube link, a YouTube playlist link, or a path to a local
  file.
- Local paths accept `~` and are read relative to the current directory.

| Source | Extracted with | Files by default into |
| ------ | -------------- | --------------------- |
| `http(s)://...` | `requests` + BeautifulSoup | `blogs/` |
| `.html`, `.htm` | the same reader, over a saved page | `blogs/` |
| `.pdf` | `pdftotext -layout` when poppler is installed, else pypdf | `papers/` |
| `.docx`, `.odt` | `pandoc` when installed, else a stdlib zip+XML reader | `docs/` |
| `.doc` | `pandoc` only — the legacy binary format has no fallback | `docs/` |
| `.md`, `.markdown`, `.txt`, `.text`, `.rst`, `.org` | read as-is | `docs/` |
| YouTube video | `youtube-transcript-api` | `docs/` |
| YouTube playlist | queued for the daemon — see below | `docs/` |

Anything else is refused, and the error lists every accepted extension.

A YouTube video with no published transcript is written as a **stub**: its
metadata only, marked `kind: stub`, with the `refresh` command to retry printed
for you. A scanned PDF with no text layer is refused with a note that it needs
OCR first.

A playlist is not ingested inline. It is queued as a job under
`.state/ingest/`, and `px0 daemon start` drains it in the background. Without
`yt-dlp` installed, only the first ~100 videos of a playlist are reachable,
because that is all YouTube renders into the page px0 can fetch; the shortfall is
reported rather than passed off as a finished job.

```shell
px0 brain add https://example.com/some-post
px0 brain add ~/papers/raft.pdf
px0 brain add ./design-doc.docx
px0 brain add "https://youtu.be/dQw4w9WgXcQ"
```

### `--to FOLDER`

Which subfolder of the brain to file into, overriding the routing above.

- **Input:** any relative path inside the brain. `docs`, `blogs`, `papers`, and
  `work` are what px0 routes into by default, but a path of your own works too —
  useful when the brain points at a vault with its own structure.
- **Default:** chosen by source type, per the table above.
- Refused if it would land outside the brain: absolute paths, `~`, and any `..`
  that climbs out. Nothing is written when a folder is refused.

```shell
px0 brain add ./paper.pdf --to "Personal/Reading"
px0 brain add ./internal-pricing.md --to work
```

`work/` is the private folder: see [Private material](#private-material).

### `--no-propose`

Skip the guideline-proposal pass.

- **Input:** flag, no value.
- **Default:** off — after a successful ingest, px0 reads the new material and
  proposes guideline edits for `px0 guidelines review`.
- The pass costs one call to the coding-agent harness. Use this to ingest in bulk
  or when offline. Stubs never trigger it: there is nothing to learn from
  metadata.

```shell
px0 brain add https://example.com/post --no-propose
```

---

## `px0 brain refresh`

Re-fetch a source already in the brain and rewrite the file in place. Works for
every kind: a web page is fetched again, a local file re-read, a PDF or document
re-extracted, and a YouTube stub retries its transcript and is promoted to a
full `video` if one has since been published.

### `path` (required)

Which brain file to refresh.

- **Input:** any of these forms — an absolute path, store-relative
  (`brain/blogs/x.md`), brain-relative (`blogs/x.md`, the form `brain list`
  prints), or a bare filename matched anywhere in the brain.
- A bare name matching more than one file is refused, and the candidates listed.
  Directories are not accepted.

```shell
px0 brain refresh blogs/example-post.md
px0 brain refresh example-post.md
```

Fails with a clear message when the file records no `source`, when the original
local file is gone, or when a stub still has no transcript.

### `--no-propose`

As for `add`: skip the guideline-proposal pass. Default off.

---

### `--from-file PATH` (on `add`)

Ingest every source listed in a file, one per line.

- **Input:** a path to a text file. Blank lines and lines starting with `#` are
  ignored.
- **Default:** the single `source` argument is used.
- The index is rebuilt once at the end rather than per file, which is what keeps
  a long list from being quadratic in the size of the library.

```shell
px0 brain add "" --from-file ~/reading-backlog.txt
```

### `--all` (on `refresh`)

Re-fetch every file that records a source.

- **Input:** flag, no value. Default off.
- `path` becomes optional when this is given.

```shell
px0 brain refresh --all
```

### `--stale` (on `refresh`)

Re-fetch what has gone stale: anything retrieved longer ago than `--days`, plus
every stub, whose transcript may have been published since.

- **Input:** flag, no value. Default off.

```shell
px0 brain refresh --stale
```

### `--days N` (on `refresh`)

What counts as stale.

- **Input:** a whole number of days.
- **Default:** 30. A file with no `retrieved` date is always treated as stale.

```shell
px0 brain refresh --stale --days 90
```

---

## `px0 brain show`

One file: where it came from, what kind it is, and its text.

### `path` (required)

- **Input:** the same forms `refresh` accepts — a library-relative path
  (`blogs/x.md`), a store-relative one (`brain/blogs/x.md`), an absolute path, or
  a bare filename matched anywhere in the library.

### `--json`

Frontmatter, body, size, and whether the file is private, as one object.

- **Input:** flag, no value. Default off.

```shell
px0 brain show blogs/caching.md
px0 brain show caching.md --json | jq .header
```

A file in the private folder is marked as such.

---

## `px0 brain rm`

Remove a file and drop its passages from the index.

### `path` (required)

- **Input:** as for `show`.

### `--yes`

Skip the confirmation.

- **Input:** flag, no value. Default off.

```shell
px0 brain rm blogs/mistaken-ingest.md
```

Deleting the file by hand works right up until you search: the passages stay in
the index until something rebuilds it. This does both.

---

## `px0 brain export`

Copy the library elsewhere, keeping its folder structure.

### `dir` (required)

- **Input:** a directory path. Created if it does not exist.

### `--include-private`

Include the private folder, which is held back by default.

- **Input:** flag, no value. Default off.
- The private folder's whole promise is that it does not leave the machine by
  accident, so including it has to be asked for.

```shell
px0 brain export ~/Documents/brain-backup
px0 brain export ~/Documents/everything --include-private
```

---

## `px0 brain list`

Every file in the brain, one per line, relative to the brain root.

- **Arguments:** none.
- Files inside the private folder are marked `(private)` rather than hidden, so
  the exclusion explains itself.
- Tool state — dot-folders and anything matching `brain.ignore` — is not listed,
  but the count of what was skipped is.

```shell
px0 brain list
```

Also printed as one section of `px0 store list`.

---

## `px0 brain search`

Print the passages matching a query, best first, as `path#anchor` with a score
and a snippet.

### `query` (required)

- **Input:** free text. Words are OR-matched, so any one of them can hit.
  Punctuation is safe — it can never be read as query syntax.
- Non-ASCII works: Devanagari, CJK, and accented Latin all match, and accents
  fold both ways, so `cafe` finds `café` and the reverse.

### `--k N`

How many passages to return.

- **Input:** integer.
- **Default:** `retrieval.k_default`, which ships as `5`.

### `--kind {blog,paper,doc,video,stub}`

Only passages from material of this kind, read from each file's frontmatter.

- **Input:** one of the five kinds.
- **Default:** none — every kind is searched.
- Files px0 did not write carry no kind and so are never matched by `--kind`. In
  a vault that is most of your notes. When a `--kind` search finds nothing, the
  output says so explicitly rather than looking like an empty brain.

### `--json`

Print the results as a JSON array of objects, and nothing else.

- Each object has `path`, `anchor`, `text`, `score`, `ingested_at`, `kind`.

```shell
px0 brain search "consistent hashing"
px0 brain search "quorum" --kind paper --k 3
px0 brain search "backpressure" --json | jq -r '.[].path'
```

---

## `px0 brain ask`

Answer a question using only what the brain holds. Retrieves the top passages,
asks the coding-agent harness to answer from those alone, and records the
exchange as a run you can trace with `px0 runs why`.

Never touches connectors, and never retrieves guidelines by similarity — only
the brain.

### `question` (required)

- **Input:** free text.

### `--k N`

How many passages to ground the answer in. Default `retrieval.k_default` (`5`).

### `--kind {blog,paper,doc,video,stub}`

Answer only from material of this kind. Default: every kind.

### `--sources`

After the answer, list the passages it was drawn from as `path#anchor`.

- **Input:** flag, no value. Default off.

```shell
px0 brain ask "what did that post say about caching?"
px0 brain ask "what have I read about consensus?" --kind paper --sources
```

Refuses with an actionable message when the index is empty — run `reindex` — or
when nothing matched the question.

---

## `px0 brain reindex`

Rebuild the retrieval index from what is on disk. Prints how many passages were
indexed.

- **Arguments:** none.
- Run it after editing brain files by hand, after changing `brain.path`,
  `brain.ignore`, or `brain.private_folder`, or whenever `px0 doctor` says the
  index is empty. `add` and `refresh` reindex on their own, and the daemon
  reindexes nightly.

```shell
px0 brain reindex
```

---

## Private material

`brain/work/` is the never-leaves-this-machine folder. Its passages are withheld
from retrieval by default, so nothing in it reaches a model, a connector, or a
workflow output unless something explicitly asks for it.

The folder name is `brain.private_folder`, which is configurable, and settable to
`""` to disable the behaviour entirely.

```shell
px0 brain add ./internal-pricing.md --to work
px0 config set brain.private_folder ""            # nothing is held back
px0 config set brain.private_folder px0-private   # hold back this folder instead
```

## Pointing the brain at an existing vault

A px0 brain and an Obsidian vault are the same thing on disk — a folder of
Markdown — so `brain.path` can point straight at one:

```shell
px0 config set brain.path ~/Documents/MyVault
px0 brain reindex
```

Any folder of Markdown works: an Obsidian vault, a Logseq graph, a `notes/`
directory in a repo. px0 reads it in place — `reindex`, `search`, and `ask` never
write to it.

Skipped automatically: every dot-folder — `.obsidian/`, `.trash/` (so a note you
deleted does not stay searchable), `.git/`, `.stversions/` — and drawings stored
as Markdown (`*.excalidraw.md`). Add your own patterns with `brain.ignore`.

**One thing to know.** `work/` means "never leaves this machine" to px0 and "my
work notes" to every notes app. If your vault already has a top-level `work/`,
those notes are held back from every search. `px0 config set brain.path` warns
when it spots this, `px0 brain list` marks such files, and `px0 doctor` reports
the count.

## Related configuration

| Key | Effect |
| --- | ------ |
| `brain.path` | Which directory the brain is |
| `brain.private_folder` | Which subfolder is withheld from retrieval |
| `brain.ignore` | Glob patterns never indexed |
| `retrieval.backend` | `local` (SQLite FTS5/BM25) or `qmd` (hybrid + rerank) |
| `retrieval.k_default` | Default passage count per query |

See [Configuration keys](../reference/configuration.md).

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unrecognized source, missing file, refused `--to`, ambiguous path, empty index, nothing matched |
| `3` | `ask` could not reach the coding-agent harness |
