# 9. The brain and retrieval

Modules: `px0/brain.py`, `px0/retrieval.py`, `px0/ask.py`

The brain is a folder of Markdown holding what you have read and kept. Ingestion turns a URL or a file into one of those Markdown files; retrieval finds the passages that answer a question.

## Ingestion is local and text only

Every extraction path runs on this machine. No API keys, and nothing leaves the laptop to be parsed.

Every format has a route that works on a stock install, with an external tool used only when it is present and does the job better:

| Kind | Preferred | Fallback |
| ---- | --------- | -------- |
| Web page | `requests` plus BeautifulSoup | none needed |
| Saved HTML | the same reader, over a local file | none needed |
| PDF | `pdftotext -layout` from poppler | `pypdf` |
| `.docx`, `.odt` | `pandoc` | stdlib zip plus XML reader |
| `.doc` | `pandoc` | none; legacy binary format |
| Markdown, text, rst, org | read as-is | none needed |
| YouTube | `youtube-transcript-api` | oEmbed metadata, filed as a stub |
| YouTube playlist | `yt-dlp` | scrape the page, first ~100 videos |

The fallbacks are what make the brain usable out of the box. Requiring poppler meant `px0 brain add paper.pdf` failed on a stock machine with nothing but an "install poppler-utils" message. Requiring pandoc made every `.docx` unreadable.

`_extract_zip_xml_document` is the stdlib route for Office and OpenDocument files. Both formats are zip archives holding one XML document, so paragraph text is reachable with `zipfile` and `xml.etree` alone.

## What a brain file looks like

```markdown
---
source: https://example.com/post
retrieved: 2026-08-27
kind: blog
title: How consistent hashing actually works
---
The body text.
```

`kind` is one of `blog`, `paper`, `doc`, `video`, or `stub`. It is what `--kind` filters on, and a file px0 did not write carries no kind, so any such filter excludes it -- there is nothing to match on.

Files land in a folder chosen by the suffix table, and `--to` overrides it with any relative path. That flexibility exists because `brain.path` can point at an existing notes vault, and a vault has its own structure: `--to "Personal/Reading"` should work.

`resolve_folder` validates textually rather than via `Path.resolve()`, so a folder that does not exist yet still validates and a symlinked brain root is not mistaken for an escape. Absolute paths, drive letters, `~`, and any `..` component are refused, and `_dest_path` checks once more that the file lands under the brain root.

## HTML extraction

`_html_to_text` strips `script`, `style`, `nav`, `footer`, `header`, and `aside`, then prefers `<article>` or `<main>`.

The interesting part is how it joins text:

```python
for el in main.find_all(_BLOCK_TAGS):
    if el.find(_BLOCK_TAGS) is not None:
        continue  # a block containing other blocks; its children speak for it
    line = _tidy_inline_spacing(el.get_text(" ", strip=True))
```

Join within a block, split between blocks. Taking `get_text("\n")` over the whole subtree instead put every inline element on its own line, so one sentence containing two links arrived as three paragraphs. That reads badly and, worse, chops a sentence across chunk boundaries at index time.

`_tidy_inline_spacing` then closes the gaps the space separator leaves around inline tags, so a footnote reads `hashing [1] is` rather than `hashing [ 1 ] is`.

## Stubs and refresh

Plenty of YouTube videos have no published transcript. That is an ordinary outcome, so the file is written as a stub with the metadata that oEmbed does provide, and a note saying to try again later.

A broken transcript library is not an ordinary outcome, so `AttributeError` and `ImportError` are re-raised as `IngestError` rather than folded into "no transcript". Swallowing everything meant that a dependency whose API had shifted turned every single video into a metadata-only stub, with nothing anywhere saying why.

That is also why the dependency pin in `pyproject.toml` carries a comment: the instance-based `.fetch()` this code calls did not exist before 1.0, and the 0.6 static `get_transcript` is gone in 1.x, so a resolver landing on 0.6 would make every video ingest as a stub.

`refresh` re-fetches an already-ingested source and rewrites the file in place, handling each kind the library holds. Only stubs used to be supported, which made the command reject every other file with "is not a stub" despite advertising a re-fetch.

`stale(home, config, days)` returns files whose `retrieved` date is older than the cutoff, plus every stub regardless of age, plus anything with an unreadable date. A stub is always worth another try.

## Batching and reindex cost

Reindexing rewrites the whole table, so doing it per file makes a batch quadratic in the size of the library. Draining a 100-video playlist into an established brain spent almost all its time rebuilding an index it was about to discard.

So `add`, `refresh`, and `remove` all take `reindex=False`, and `add_many`, `refresh_many`, and `process_ingest_queue` reindex exactly once at the end.

## The playlist queue

A playlist is not ingested inline. `add` writes a job file into `.state/ingest/` and raises an `IngestError` telling the user to start the daemon.

`process_ingest_queue` drains those jobs during nightly housekeeping. It is idempotent -- a video whose destination file already exists is skipped -- and it retries up to `MAX_INGEST_ATTEMPTS` before moving the job into `.state/ingest/failed/` with the last error recorded.

Two failure modes are reported rather than hidden. An empty enumeration is treated as a failure, not a finished job: it means the page shape changed, the playlist is private, or the fetch was rate-limited, and deleting the job would make the playlist disappear without a word. A result at exactly the first-page limit is counted as truncated, because a playlist cut off at 100 videos has not been fully ingested and "job done" would say otherwise.

`enumerate_playlist` prefers `yt-dlp` when installed, because YouTube renders only the first page into the HTML and loads the rest through a continuation API whose token a plain GET does not get. Rather than reverse-engineer that, use the tool that tracks it for a living when the user happens to have it -- the same arrangement as `pdftotext` and `pandoc`.

`yt-dlp` verifies TLS against its own bundled certifi and offers no CA-bundle option, so it cannot be pointed at `connectors.ca_bundle`. On an intercepting network it fails outright and `_enumerate_playlist_ytdlp` returns `None`, so the scrape still runs. Forcing it with `--no-check-certificates` would trade a partial playlist for unverified HTTPS, which is not a trade worth making.

## Retrieval

`retrieval.retrieve(home, config, query, k, local_only, kind)` is the whole interface. Query and filters in, ranked passages out.

Guidelines are never retrieved by similarity, only the brain. A guideline is attached to a workflow at build time by description; pulling one in by keyword match at run time is the failure that made a standup inline a commit-message rubric.

### The backend

Search shells out to the `qmd` CLI (`retrieval.qmd_cmd`), which does hybrid keyword plus vector search with local models. px0 pins and checks a version, and the comment above the pin records what was verified against a real install:

```
* `-n <num>`                max results
* `-c, --collection <name>` collection filter
* `--format <kind>`         cli | json | csv | md | xml | files
  -- there is NO `--json` flag; JSON comes from `--format json`
```

The brain is registered as one collection, `px0-brain`, created idempotently on first use.

### Consent for the models

The vector path needs about 2GB of GGUF models. `_qmd_ensure_embed_consent` prints the exact sizes and asks once, recording the answer in `.state/retrieval-consent.json`.

Declining is a supported state, and the code branches on it at query time:

```python
if _qmd_has_consent(home):
    subcommand, timeout = "query", 300.0
else:
    subcommand, timeout = "search", 60.0
```

`qmd query` is the hybrid path and hangs without the models until px0's subprocess timeout fires, which made the whole backend look broken for anyone who declined. `qmd search` is BM25-only, needs no models, and answers in milliseconds. A store without consent gets keyword search, not a hang.

### Path normalization

qmd reports results as `qmd://<collection>/docs/x.md`. Stripping only the scheme left the collection name on the front, so paths came back as `px0-brain/docs/x.md`.

That was not cosmetic. `local_only` decides what to withhold by checking whether a path sits in the private folder, and a prefixed path never matched -- so private `brain/work/` passages were being returned by default, against the one guarantee that folder carries.

### What is never indexed

`is_ignored` applies two rules. Any dot-directory anywhere in the path is tool state, checked structurally rather than by glob so no pattern list has to enumerate every sync tool and plugin that stores Markdown beside your notes. That covers `.obsidian/`, `.trash/`, `.git/`, and `.stversions/`.

Then the configured globs, defaulting to `*.excalidraw.md` -- a drawing, not prose: a Markdown wrapper around a JSON blob that indexes as thousands of meaningless tokens.

### The private folder

`brain.private_folder` names a subfolder withheld from retrieval and never sent anywhere. It defaults to `work` and can be renamed or disabled with an empty string.

That configurability is a bug fix. The default name collides with ordinary usage: `work/` means "never leaves this machine" to px0 and "my work notes" to every notes app, so a vault with a work folder had all of it silently dropped from every search.

The filter lives in one place, `is_private`, and `retrieve` applies it because qmd has no column to filter on.

### The local reranker

BM25 rewards a passage that says one query term many times. A question with three terms usually wants the passage mentioning all three, even if each only once.

`rerank` scores each candidate on coverage first, then proximity, then a mild length penalty, with the original rank as the tie-break:

```python
coverage = hits / len(terms)
proximity = 1.0 / (1.0 + spread / 400.0)  # terms within a paragraph are one thought
length_penalty = min(1.0, 1200.0 / max(len(text), 1)) ** 0.15
combined = (coverage * 3.0) + proximity + (length_penalty * 0.25)
```

Pure local arithmetic: no model call, no network, and stable for the same input. The length penalty keeps a whole chapter from outranking the paragraph that answers.

Because reranking can only reorder what it is given, `retrieve` over-fetches when it is on: `k * RERANK_FACTOR`, capped at `RERANK_MAX_CANDIDATES`. With rerank off, `k` rows in means `k` rows out.

A `kind` filter over-fetches too, by a factor of five, because qmd cannot filter on px0's frontmatter -- otherwise asking for five papers returns however many of the top five happened to be papers.

## Robust reading

Two functions exist because a brain pointed at someone's own vault is not all px0's output.

`read_text_lossy` reads as UTF-8 with `errors="replace"`. One file saved in another encoding used to abort the caller with `UnicodeDecodeError`, and since reindex walks every file, a single stray byte anywhere made the whole brain unsearchable.

`read_header` treats anything that is not a clean `---`-delimited YAML mapping as a file with no frontmatter, so the body still gets indexed. A note that merely opens with a horizontal rule parses as a bare scalar, and callers all expect a mapping.

## Resolving a path a person typed

`resolve_brain_path` accepts what the user is likely to have in hand: an absolute path, a store-relative one (`brain/blogs/x.md`, the form the docs use), a library-relative one (`blogs/x.md`, the form `px0 brain list` prints), or a bare filename matched anywhere in the library.

Before that, only a path relative to the current working directory worked, so neither the listed nor the documented form did. Directories are excluded from the bare-name match, because `brain refresh docs` used to resolve to the folder and then fail deep inside with `IsADirectoryError`.

## Asking

`ask.ask` is retrieval plus generation over the brain and nothing else. It never touches connectors or guidelines.

It retrieves `k` passages, builds a prompt telling the model to answer using only those passages and to cite sources inline as `path#anchor`, and records the exchange as a run record with an `ask_` prefix. Recording it as a run is what lets `route.repeated_questions` later notice you keep asking the same thing by hand.

No matching passages is an error, not an empty answer, and the message names `px0 brain reindex` as the likely fix.

## Next

[Part 10](10-context.md) covers the other two knowledge stores and how all three end up in one prompt.
