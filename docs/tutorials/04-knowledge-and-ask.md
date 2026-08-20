# Knowledge and ask

`knowledge/` is the library of what you've read and kept -- papers, blog
posts, internal docs, video transcripts. Unlike `workflows/` and
`guidelines/`, it isn't versioned: it's bulk reference material, and it's
rebuildable from its source URL if you ever lose it.

## 1. Add something to the library

```shell
px0 knowledge add https://blog.example.com/how-shopify-scaled-database   # web page -> knowledge/blogs/
px0 knowledge add ~/papers/raft.pdf                                      # pdf -> knowledge/papers/
px0 knowledge add ~/notes/payments-architecture.docx                     # document -> knowledge/docs/
px0 knowledge add https://youtube.com/watch?v=...                        # transcript -> knowledge/docs/
```

Extraction runs locally and the destination folder is picked from the
source type -- override it with `--to docs|blogs|papers` if you want it
somewhere specific.

A YouTube link without a published transcript isn't rejected: px0 writes
a stub holding the URL and whatever metadata it can find (title,
channel, description), marked as metadata-only. It's indexed and
searchable even without a body. Once a transcript appears, upgrade it:

```shell
px0 knowledge refresh knowledge/docs/some-talk.md
```

Ingesting a source also runs a proposal pass over the text by default --
px0 looks for claims that might belong in your guidelines. Skip that
with `--no-propose` if you just want the material filed.

## 2. See what's in the library

```shell
px0 knowledge list
```

## 3. Search it directly

```shell
px0 knowledge reindex
px0 knowledge search "connection pooling" --k 8
px0 knowledge search "connection pooling" --json
```

`reindex` rebuilds the retrieval index from scratch; run it if `px0 knowledge ask`
reports the index is missing or stale. The nightly daemon pass reindexes
too, and every `knowledge add` reindexes what it just filed. Each result
line is a `path#anchor` and a relevance score, followed by a text
snippet.

### Choosing a retrieval backend

`retrieval.backend` picks how search works:

| Backend | What you get | Cost |
| --- | --- | --- |
| `local` (default) | SQLite FTS5 keyword matching with BM25 ranking, embedded, no server | Nothing to install |
| `qmd` | Hybrid keyword + vector search with LLM reranking | Needs the qmd CLI and ~2GB of local models |

`local` only matches words that are actually in the text. A search for
"connection pooling" won't surface a passage that says "reusing database
connections" -- same idea, no shared keywords. That's what `qmd` is for:

```shell
npm install -g @tobilu/qmd     # or: bun install -g @tobilu/qmd
px0 config set retrieval.backend qmd
px0 knowledge reindex
```

qmd needs Node.js 20 or newer. If it isn't on your `PATH`, px0 says so
and points you back at `local` rather than failing obscurely.

The first reindex after switching prints the model download sizes and
waits for an explicit `y`:

```
Local models needed for semantic search & reranking:
--------------------------------------------------
embeddinggemma-300M       ~300MB  (Embeddings)
qwen3-reranker-0.6b       ~640MB  (Reranking)
qmd-query-expansion-1.7B  ~1.1GB  (Expansion)
--------------------------------------------------
Total Download Size:      ~2.04GB
--------------------------------------------------
Download ~2.04GB of local models for semantic search? [y/N]
```

Decline and px0 keeps indexing keywords only -- it degrades rather than
breaks, and it won't ask again until you consent. The answer is recorded
in the store, and `px0 doctor` reports both the consent state and
whether your installed qmd matches the version px0 is pinned to:

```
✓ index  qmd backend configured (version: 2.8.3, consented)
```

Point `retrieval.qmd_cmd` at a specific binary if `qmd` isn't the right
command (a wrapper script, a pinned path). Switching back to `local` is
one `px0 config set` away; nothing is lost, since the index is rebuilt
from `knowledge/` either way.

### `knowledge/work/` never leaves the machine

Anything filed under `knowledge/work/` is excluded from every retrieval
px0 performs -- `search`, `ask`, and workflow `retrieve:` inputs alike,
on both backends. That's a hard exclusion, not a ranking penalty.

## 4. Ask it a question

```shell
px0 knowledge ask "what did that Shopify post say about connection pooling?"
px0 knowledge ask "how does our payments architecture handle idempotency?" --k 8 --sources
```

`px0 knowledge ask` retrieves relevant passages from `knowledge/` and generates an
answer citing them -- `--sources` prints the `path#anchor` list
alongside the answer. It never touches connectors or guidelines; it's
retrieval plus generation over your library and nothing else.

Every `ask` produces a run record like any workflow run, so it shows up
in `px0 runs list` and `px0 runs why <run-id>` can explain exactly which
passages fed the answer.

## 5. Queued playlist ingests

A YouTube *playlist* URL isn't ingested inline -- it's queued under
`.state/ingest/` and drained by the daemon's nightly pass, one knowledge
file per video that has a transcript. Videos already ingested are
skipped on later passes, and a job that keeps failing is retired to
`.state/ingest/failed/` rather than retried forever. So a playlist needs
the daemon running to actually land:

```shell
px0 daemon install
```

See [06-scheduling-and-the-daemon.md](06-scheduling-and-the-daemon.md).

## Next

- [05-guidelines-and-provenance.md](05-guidelines-and-provenance.md) --
  how guidelines evolve and how to trace any output back to its sources.
- [06-scheduling-and-the-daemon.md](06-scheduling-and-the-daemon.md) --
  nightly reindexing and playlist draining.
