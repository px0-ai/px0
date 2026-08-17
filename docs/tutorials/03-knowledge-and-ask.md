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
px0 list knowledge
```

## 3. Search it directly

```shell
px0 search reindex
px0 search "connection pooling" --k 8
```

`reindex` rebuilds the retrieval index from scratch; run it if `px0 ask`
reports the index is missing or stale. Each result line is a
`path#anchor` and a relevance score, followed by a text snippet.

## 4. Ask it a question

```shell
px0 ask "what did that Shopify post say about connection pooling?"
px0 ask "how does our payments architecture handle idempotency?" --k 8 --sources
```

`px0 ask` retrieves relevant passages from `knowledge/` and generates an
answer citing them -- `--sources` prints the `path#anchor` list
alongside the answer. It never touches connectors or guidelines; it's
retrieval plus generation over your library and nothing else.

Every `ask` produces a run record like any workflow run, so it shows up
in `px0 runs list` and `px0 why <run-id>` can explain exactly which
passages fed the answer.

## Next

- [04-guidelines-and-provenance.md](04-guidelines-and-provenance.md) --
  how guidelines evolve and how to trace any output back to its sources.
