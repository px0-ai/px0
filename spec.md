# Tech Spec: px0

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#tech-spec-px0)

A local-first CLI where everything the system does is a workflow, and everything the system knows lives in two folders: `guidelines/` for how the user works, `knowledge/` for what the user has read and kept. Workflows are executable and run manually or on a cron schedule. Guidelines are declared per workflow and inlined into prompts; knowledge is retrieved. Workflows and guidelines are versioned by the tool itself, with no dependency on any external version control system.

Version: 0.6 draft. Status: ready for implementation.

## Goals

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#goals)

-   Every capability is expressed as a workflow file the user can read, edit, copy, and schedule.
-   A workflow can be created from a natural-language description via a builder that checks feasibility, sets up the external connections it needs, scopes the tools it may use, and picks the guidelines it should follow.
-   All prescriptive and reference material lives in plain Markdown files with no required metadata.
-   Every create, edit, and delete of a workflow or guideline produces a new version kept by the tool, so history, diffing, and reverting work on a bare directory with nothing installed alongside it.
-   Runs entirely on the user's machine. External systems are fetched on demand, never mirrored.
-   Workflows can act, not just draft. A workflow may call any tool its connection exposes, including tools that post, comment, or send, bounded by a per-workflow allowlist.
-   The system learns: material the user ingests, and the edits the user makes to proposed text, become evidence for guideline proposals.
-   Installation is one command, and the tool keeps itself current with `px0 update`.

## Non-goals

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#non-goals)

-   No always-on background intelligence. The daemon installed with the tool is a scheduler, log keeper, and version checkpointer only; all fetching, generation, and acting happen inside discrete workflow runs.
-   No local mirror of GitHub, calendar, or issue trackers, and no persistent connector state of any kind. Everything a run needs from an external system is fetched inside that run.
-   No git, and no integration with git. Versioning is the tool's own, and a store that happens to sit inside someone's git repository is treated as an ordinary directory.
-   No versioning of `knowledge/`. The library is append-mostly reference material, and versioning it would put a hashing scan of thousands of files on the hot path of every command.
-   No team-shared stores, no hosted service, no IDE plugin in v1.

## Core concepts

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#core-concepts)

Three primitives.

| Primitive | Nature | Location | Versioned | Analogy |
| --- | --- | --- | --- | --- |
| Workflow | Executable, has a trigger | `workflows/` | Yes | A verb: do this |
| Guideline | Prescriptive prose | `guidelines/` | Yes | A rule: do it this way |
| Knowledge | Reference material | `knowledge/` | No | A library: this exists |

A workflow is something the system does: generate a standup summary, precheck a PR, draft a review, post a weekly update. A guideline is how the user works: how commit messages are written, what a Go review checks. Knowledge is what the user has read and kept: papers, blog posts, internal docs.

## Installation

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#installation)

px0 installs with one command.

```shell
curl -fsSL https://px0.sh/install.sh | sh
```

The script is the supported install path for every platform and does the whole job:

1.  Detects OS and architecture, refuses unsupported combinations by name rather than failing halfway.
2.  Fetches the release manifest for the channel (`stable` by default), downloads the matching binary with its checksum and signature, and verifies both against a public key embedded in the script before anything is written outside the temp directory.
3.  Installs the binary to `PX0_PREFIX`, default `~/.local/bin`, and warns if that directory is not on `PATH` with the exact line to add.
4.  Installs the runtime dependencies px0 cannot assume: the Bun runtime and a pinned `qmd` version, both into `~/.local/share/px0/runtime/` rather than globally, so px0 never fights with a Bun the user already has and uninstalling px0 leaves no orphans. An existing compatible Bun is detected and reused.
5.  Runs `px0 init` to scaffold the store, then offers to install the daemon, printing the unit file it would write and skipping on refusal.
6.  Prints what was installed, where, and the three commands worth running first.

Embedding models are not downloaded at install time. They arrive on first index, with explicit consent and a printed size, so the install stays small for anyone who never uses retrieval.

Knobs, all environment variables so the piped-script form stays usable: `PX0_VERSION` pins an exact version, `PX0_CHANNEL` selects a channel, `PX0_PREFIX` moves the binary, `PX0_NO_DAEMON` skips step 5's daemon offer, and `PX0_NO_RUNTIME` skips step 4 for people who manage Bun and qmd themselves. `install.sh --uninstall` removes the binary, the runtime directory, and the daemon unit, and never touches the store; removing the store is a separate, explicitly printed command.

The same manifest and signature machinery backs `px0 update`, so installing and updating are one mechanism with two entry points.

## Store layout

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#store-layout)

The store is a single directory, default `~/.px0`, overridable with `PX0_HOME`. It is plain files plus a version history the tool maintains itself.

    ~/.px0/
      workflows/          # versioned
        standup-summary.md
        pr-precheck.md
        review-pr.md
        consolidate.md
        skills-build.md
      guidelines/         # versioned
        commit-messages.md
        pr-descriptions.md
        code-review/
          go.md
          python.md
          common.md
      knowledge/          # not versioned
        docs/
          payments-architecture.md
        blogs/
          how-shopify-scaled-database.md
        papers/
          raft-paper.md
      outputs/
        standup-2026-08-17.md
      skills/             # build output, derived
      .state/             # runtime internals and version history
      config.toml         # versioned
    

`workflows/`, `guidelines/`, `knowledge/`, and `outputs/` are content. `skills/` is derived build output. `.state/` is everything the runtime needs and the user does not read by hand: the store lock, the version history, pending proposals, the schedule state, the ingest queue, the retrieval index, and `credentials.toml`.

Run logs and run records do not live in the store at all. They go to a configurable log directory, `[logs] path` in `config.toml`, defaulting to `/var/log/px0` when writable and `~/.local/state/px0/logs` otherwise, so raw prompts and raw connector responses never land in a folder the user might copy or sync.

Rules:

-   `workflows/`, `guidelines/`, and `knowledge/` are user-editable source. `outputs/`, `skills/`, and `.state/` are tool-managed.
-   Automatic processes never edit a file under `guidelines/`. They write pending edits to `.state/proposals/`, which surface in consolidation and apply only on acceptance.
-   `.state/versions/` travels with the store, so copying the directory copies its history. `.state/credentials.toml` also lives there at mode 0600, so a wholesale copy carries secrets too; `px0 store export <dir>` writes content plus version history with credentials excluded, and is the supported way to move a store to another machine.

## Versioning

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#versioning)

Versioning is a first-class feature of the tool, not a delegation to git.

### Scope

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#scope)

Versioned: `workflows/`, `guidelines/`, and `config.toml`. These are small, hand-edited, machine- proposed, and the things a user needs to undo.

Not versioned: `knowledge/`, `outputs/`, `skills/`. Knowledge is bulk reference material that arrives through `px0 knowledge add` and is rebuildable from its source URL; versioning it would mean hashing a vault of thousands of files on the hot path of every command for history nobody asks for. Outputs are products of runs, and skills are compiled artifacts.

The practical consequence to know: a knowledge file deleted or overwritten by hand is gone, and `px0 knowledge add` on a URL already in the library asks before replacing rather than silently overwriting.

### Model

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#model)

Two levels, one for files and one for sessions.

| Level | Id form | Granularity |
| --- | --- | --- |
| Version | `<path>@v7` | One file, one state of that file |
| Change | `chg_2026-08-17-004` | One session, all files it touched, atomically |

A version is an immutable snapshot of one file's bytes. Versions are numbered per file from `v1`, never renumbered, never rewritten. A change groups the versions produced together: a consolidation session that accepts six proposals across four topic files is one change containing four new versions.

Every version carries metadata: timestamp, actor (`user:manual`, `run:<run-id>`, `builder`, `consolidate`, `update`), the change it belongs to, and, where the mutation came from a proposal, the evidence that motivated it. This is what makes a claim's history readable later, and it is why the versioning layer, not the run record, is where provenance survives.

Deletion is a version too: a tombstone version marking the file removed. The file's history stays addressable, and restoring it is a revert like any other.

### Capturing hand edits

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#capturing-hand-edits)

Hand-editing files is a designed path, so px0 cannot rely on being the one making changes. Before any command that reads store content, the runner scans `workflows/`, `guidelines/`, and `config.toml`, comparing size and mtime against the manifest and hashing only where they differ. Anything changed outside the tool is captured as a new version attributed to `user:manual` in a change of its own. The daemon runs a full-hash scan nightly to catch what mtime tricks miss.

Because the versioned set excludes `knowledge/`, this scan covers tens of files, not thousands, and stays cheap enough to run unconditionally.

### Storage

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#storage)

Versions are content-addressed: blobs under `.state/versions/objects/<hash>`, zstd-compressed, deduplicated, with a manifest in `.state/versions/manifest.sqlite` mapping paths to ordered versions and their metadata. Identical content stored twice costs one blob, so reverting back and forth is free.

Default retention is everything. `[versions]` in `config.toml` can cap history, and `px0 versions prune` applies the policy, never touching the current version of any live file.

### Commands

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#commands)

```shell
px0 versions list guidelines/code-review/go.md        # every version, actor, change, timestamp
px0 versions show guidelines/code-review/go.md@v3     # that version's content
px0 versions diff guidelines/code-review/go.md v2 v7  # unified diff between two versions
px0 versions revert guidelines/code-review/go.md --to v3
px0 versions prune [--dry-run]

px0 changes list [--since 7d] [--actor consolidate]
px0 changes show chg_2026-08-17-004                   # every file and diff in the change
px0 changes revert chg_2026-08-17-004                 # undo the whole session
```

Revert never rewrites history. Reverting `go.md` to `v3` writes `v3`'s content as a new version `v8`, attributed to the revert, so the intervening states remain readable. Reverting a change does the same for every file it touched, as one new change. There is no destructive operation in the versioning surface except `prune`, which is explicit and policy-driven.

### Section-level history and renames

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#section-level-history-and-renames)

Guideline claims are addressed as `<path>#<heading-slug>`, and their history is derived by walking that file's versions and matching the section. `px0 guidelines log <claim-id>` prints where the section first appeared, every version that changed it, and the evidence attached to each. `px0 guidelines revert <claim-id> --to v3` restores just that section, producing a new file version containing the old section and the current everything-else.

Renames are resolved at version-capture time, not guessed at read time. When a version's diff shows one section's heading replaced and another's body unchanged or close to it, the capturer computes token-level Jaccard similarity between the old and new bodies after normalizing whitespace, case, and code-span punctuation:

-   Similarity at or above 0.7: recorded as a rename. The manifest stores an alias from the old claim id to the new one. Old ids keep resolving, pending proposals retarget, and the claim's log shows a rename rather than a death and an unrelated birth.
-   Below 0.7: treated as a deletion plus a new claim, which is the honest reading when the text was substantially rewritten.

Aliases are visible and correctable, because a similarity threshold will be wrong sometimes: `px0 guidelines alias list` shows recorded aliases, `px0 guidelines alias link <old> <new>` adds one the capturer missed, and `px0 guidelines alias unlink <old>` removes one it invented. Aliases are metadata, so correcting them never rewrites a version.

## Guidelines

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#guidelines)

`guidelines/` is the prescriptive layer: how the user works, organized as topic files, nested into folders where a topic splits by language or domain. `commit-messages.md` is one topic; `code-review/go.md`, `code-review/python.md`, and `code-review/common.md` are one topic split by language plus a shared file.

Guideline files are plain Markdown with no frontmatter and no required structure. A file holds one topic; a section (a heading and its text) holds one claim. That is the entire format. `go.md` looks like this and nothing more:

```md
## Wrap errors with %w

Wrap errors with `fmt.Errorf("...: %w", err)` so callers can use `errors.Is` and `errors.As`.
Bare `%v` wrapping discards the chain.

## Context is the first parameter

`context.Context` is always the first parameter and is never stored in a struct.
```

Everything the old design put in frontmatter is derived from position or from version history:

-   Scope comes from the path. A folder named after a configured repo applies to that repo, and anything under a folder named `work/` never leaves the machine (excluded from skill bundles written into repositories and from any output or tool call whose destination is not local).
-   Identity comes from position too: a claim is addressed as `<path>#<heading-slug>`, with rename aliasing as described above.
-   Bookkeeping (evidence, provenance permalinks, when a claim was last reinforced) lives in version metadata, reconstructed on demand by `px0 why` and `px0 guidelines log`.

### How guidelines reach a workflow

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#how-guidelines-reach-a-workflow)

A workflow names the guideline files it follows, explicitly, in its frontmatter:

```yaml
guidelines:
  - code-review/common.md
  - code-review/go.md
```

Paths are relative to `guidelines/`. There are no folder references, no wildcards, and no inference at run time: the list is exactly what gets inlined, in the order given. Built-in workflows ship with their lists hardcoded, and for a workflow created by `px0 new` the builder picks the relevant files during generation and writes them in, where the user can see and edit them.

At render time the runner inlines each named file's full content at the top of the prompt, or at a `{{guidelines}}` marker if the body contains one. A named file that does not exist is a validation error, caught before the run starts rather than silently producing a prompt missing its rules. Work-scoped files are dropped, with a note in the run record, when the run's output destination or tool set is not local.

The tradeoff of an explicit list is that it goes stale: a new topic file created by consolidation is not picked up by existing workflows on its own. Adding it is a one-line edit to the frontmatter, and `consolidate` reports guideline files that no workflow references, so the drift is surfaced rather than discovered late.

### Proposals

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#proposals)

Ingestion, corrections, and verification never touch guideline files. Each proposal is a pending edit in `.state/proposals/`: a diff against a topic file (new section, amended section, or retirement) plus the evidence that motivated it. `consolidate` and `px0 guidelines review` present pending edits; acceptance applies the diff and produces a new version carrying that evidence. A proposal that is neither accepted nor edited is dismissed and the pending edit is deleted; nothing is recorded about the dismissal.

## Knowledge

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#knowledge)

`knowledge/` is the reference library: material the user read and kept, organized by kind into `docs/`, `blogs/`, and `papers/`. Its location is configurable: the default is `knowledge/` inside the store (`~/.px0/knowledge/`), and `[knowledge] path` in `config.toml` can point it anywhere, including an existing notes vault, in which case px0 ingests into and retrieves from that folder without imposing any structure on it beyond its routing subfolders. Folders are user-extensible; anything under the configured path is indexed for retrieval.

Knowledge is not versioned and is never loaded into prompts wholesale; it is reached through retrieval only, which keeps prescriptive rules from being diluted by reference text. Files under `knowledge/work/` follow the same never-leaves-the-machine rule as work guidelines.

### Ingesting external sources

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#ingesting-external-sources)

`px0 knowledge add <source>` files outside material into the library. Accepted sources:

```shell
px0 knowledge add https://blog.example.com/post        # web page -> blogs/
px0 knowledge add ~/papers/raft.pdf                    # pdf -> papers/
px0 knowledge add ~/notes/payments-arch.docx           # document -> docs/
px0 knowledge add https://youtube.com/watch?v=...      # transcript -> docs/
px0 knowledge add https://youtube.com/playlist?list=.. # enumerated into per-video jobs
```

Pipeline, per source:

1.  Detect type and dispatch to an extraction adapter: readability extraction for web pages, `pdftotext`/`pandoc` for documents, and the published transcript for YouTube.
2.  Write the file into the routed folder (`--to docs|blogs|papers` overrides the default), with a short header noting URL and retrieval date, body being the cleaned text or transcript.
3.  A model pass over the file proposes candidate guideline edits: each phrased as a claim, targeting a topic file, with provenance pointing at the knowledge file plus a location anchor (heading, page, or timestamp). These land in `.state/proposals/`.
4.  Proposals wait for `consolidate` or `px0 guidelines review`.

Ingestion is text only: no local transcription ships and audio files are not accepted as sources. When a video has no published transcript, the source is not rejected. px0 writes a stub file holding the URL and whatever metadata the source exposes, typically title, channel, publication date, duration, description, and chapter markers, marked in its header as metadata only. The stub is indexed, so searching for a talk the user watched finds the record and the link even though the text was never available. No proposal pass runs over a stub, since there is no body to draw claims from. `px0 knowledge refresh <path>` re-checks a stub for a transcript published later and upgrades it in place to a full file, running the proposal pass then.

Playlists are queued in `.state/ingest/` and processed by the daemon in the background. `px0 knowledge add --wait` runs inline for small sources. Extraction always runs locally; adding a source never sends its content anywhere except to the configured model backend for the proposal pass, and `--no-propose` skips even that, filing the text alone.

## Local retrieval

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#local-retrieval)

Retrieval applies to `knowledge/` only. Guidelines are never retrieved by similarity; they are inlined deterministically from each workflow's declared list. RAG exists so that workflows, `px0 ask`, and the harness can pull relevant passages out of a growing library.

### Backend: qmd behind an internal interface

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#backend-qmd-behind-an-internal-interface)

The v1 backend is `qmd`: SQLite with FTS5 BM25 for keyword search, vector search over local GGUF embedding models, hybrid rank fusion, and a local reranker, all embedded with no server. It is built for folders of Markdown, which is literally the store.

qmd is a young, single-maintainer project, so the runner never calls it directly. All access goes through an internal `retrieve` interface (query, k, filters in; ranked passages with file path and anchor out), with qmd as the default implementation. Integration takes qmd on its own terms: prefer its programmatic API where one is exposed, fall back to its CLI with JSON output otherwise, and reserve its MCP server mode for the harness connector. The qmd version is pinned by the installer and moved deliberately, not floated.

An index format change on a qmd upgrade forces a full reindex, and that is accepted: the index is derived, `px0 doctor` detects a version mismatch between the pinned qmd and the index on disk, and either prompts or, under `px0 update`, reindexes as part of the upgrade. No index migration path is written or maintained.

### Index

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#index)

-   One qmd collection is pointed at `knowledge/`, all subfolders included. The index lives in `.state/index/`, is derived, and is rebuildable at any time with `px0 search reindex`.
-   Incremental indexing runs on two triggers: completion of any `px0 knowledge add` job, and a nightly daemon pass that walks the knowledge tree for manual edits. This walk is separate from the version checkpoint scan, which covers only workflows and guidelines.
-   Embedding and reranker models are whatever qmd requires, downloaded on first index with explicit consent and a printed size. These are the only models the tool ever puts on disk.

### Filtering and annotation

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#filtering-and-annotation)

Results carry the ingestion date recorded in the file header, so a passage from a three-year-old post is visibly old rather than silently ranked as current. Stub files are labelled as metadata only, so a hit on one is understood as a pointer rather than a source. Files under `knowledge/work/` are returned only to workflows whose output destination and tool set are local.

### Surfaces

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#surfaces)

-   CLI: `px0 search "<query>" [--k 5] [--json]` returns raw ranked passages, plus `px0 search reindex`.
-   CLI: `px0 ask "<question>"` answers in natural language (below).
-   Workflows: an input of the form `retrieve: {query, k}` runs the same interface, and the query string may reference other inputs, so a PR-review workflow can retrieve against the diff it just fetched.
-   Harness: qmd ships an MCP server mode, registered as a connector so the coding agent can query the knowledge base mid-session over stdio.

Retrieval results included in a run are written to the run record, so `px0 why` covers them for as long as that record is retained.

## px0 ask

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#px0-ask)

`px0 ask "<question>"` is question answering over the user's own library: retrieve locally with qmd, then generate an answer with the model backend, citing files.

```shell
px0 ask "what did that Shopify post say about connection pooling?"
px0 ask "how does our payments architecture handle idempotency?" --k 8 --sources
```

Semantics:

1.  The question goes through the `retrieve` interface: hybrid search plus rerank over `knowledge/`.
2.  Top passages and the question are rendered into a fixed answering prompt, and the model backend generates the answer. The prompt instructs the model to answer only from the passages and to say plainly when they do not contain the answer.
3.  The answer cites its sources as `path#anchor` references; `--sources` prints the passages themselves below the answer.
4.  Every ask produces a run record, so `px0 why` works on answers too.

`px0 ask` never touches connectors or guidelines; it is retrieval plus generation over `knowledge/` and nothing else. If the index is missing or stale, it says so and points at `px0 search reindex` rather than answering from nothing.

## Connections, credentials, and tools

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#connections-credentials-and-tools)

### Creating a connection

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#creating-a-connection)

Connections come through one of two providers.

Composio is the default and recommended provider, and it dissolves the entire OAuth problem: app registration, consent flows, token storage, refresh, and per-app scope handling all live on Composio's side, and px0 talks to one API with one key. Setup is once:

```shell
px0 connect setup-composio      # paste the Composio API key; verified and stored
px0 connect github              # prints a Composio auth link; user consents in browser
px0 connect gmail               # same flow, any app in the Composio catalog
px0 connect list                # connections, provider, status
px0 connect remove <service>
```

`px0 connect <app>` creates a connected account through Composio: px0 requests an auth link, the user consents in the browser, and Composio holds the resulting grant. px0 never sees the app's OAuth tokens at all; it holds only the Composio API key. Every app Composio supports is available without px0 shipping a connector for it.

The native provider remains for the minimal path and for people who refuse a third party in the loop: `px0 connect github --native --pat` stores a personal access token locally and uses the GitHub API directly. Native is deliberately narrow (GitHub only in v1), because Composio is the answer for breadth.

Composio is a network dependency for every run that uses one of its connections, and that is accepted knowingly: a scheduled run fails when Composio is unreachable, exactly as it fails when GitHub is. Within a run, a transient connector failure is retried three times with exponential backoff before the run is marked failed. There is no retry queue and no deferred re-execution; the next scheduled fire is the retry.

### Storage: local plaintext, by design

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#storage-local-plaintext-by-design)

What px0 stores locally is small: the Composio API key, plus any native PATs. It lives in `.state/credentials.toml`, in plain text, with file mode 0600. This is a deliberate choice: no keychain integration, no encryption at rest, no passphrase prompt.

```toml
[composio]
api_key = "cmp_..."

[github]
kind = "native-pat"
token = "ghp_..."
expires_at = 2026-11-01
```

Two tradeoffs, stated honestly. Locally, anything running as the user can read this file, same as `~/.aws/credentials` or `~/.config/gh`; what the design guarantees is mode 0600, verified by `px0 doctor`, and a `px0 store export` that omits the file so moving a store never moves secrets. With Composio, a third party holds the OAuth grants and proxies the API traffic; that is the price of never touching OAuth plumbing, and the native provider exists for anyone who declines it.

### Scopes

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#scopes)

For Composio connections, the auth link is created with the minimum scopes the planned tools need, and the granted scopes are printed after consent. A workflow that posts needs write scope, so write scope is requested when, and only when, a planned tool requires it. For native PATs, `px0 connect` prints the recommended minimal scope set for the user to mint against. `px0 doctor` flags a connection holding scopes that no tool in any workflow's allowlist uses.

### Refresh and rotation

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#refresh-and-rotation)

-   Composio connections: refresh is Composio's job, done server-side, invisible to px0. A revoked or expired grant surfaces as a failed run telling the user to run `px0 connect <service>` again, and `px0 doctor` checks each connection's status against Composio.
-   The Composio API key itself: rotated with `px0 connect setup-composio`, and rotating it in the Composio dashboard is the kill switch that severs every connection at once.
-   Native PATs: no refresh exists, so rotation is the path. `px0 doctor` and `px0 connect list` show days-to-expiry, the daemon warns from seven days out, and `px0 connect rotate github` replaces the token atomically.

### Connections expose tools

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#connections-expose-tools)

A connection is not just an auth grant; it exposes a set of typed tools, and those tools are the units workflows are scoped to. Composio connections get their tool catalog from Composio's own action registry; native connections get tools from px0's built-in adapter. Either way the runner normalizes them into one namespace and exposes them through one per-run MCP endpoint. Whatever the connection's grant permits, a workflow can be given.

    github.list_my_prs          github.get_pr_diff        github.list_review_comments
    github.get_pr               github.create_review_comment
    calendar.list_events        gmail.search_messages     gmail.get_message
    gmail.send_message          slack.post_message
    

`px0 tools list [service]` shows every tool available across connections with a one-line description, its provider, its parameters, and whether it reads or writes. Write tools are marked in the listing, named in the run record when called, and printed in the plan the builder shows before generating a workflow, so granting one is a visible act rather than an accident.

This one namespace is the only vocabulary in the system for reaching an external service. Inputs call tools from it, allowlists name tools from it, and there is no second set of connector verbs to learn or maintain.

### Tool scoping at execution

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#tool-scoping-at-execution)

A workflow declares the tools the model may call in a `tools:` allowlist. At run time the runner exposes exactly that list to the model, as a per-run MCP endpoint containing only those tools, bound to the connection's credentials. A workflow with `tools: [github.get_pr_diff]` cannot list PRs, cannot read email, cannot post anything, and cannot see that those tools exist. A workflow with no `tools:` key gets no tools at all and runs on its prefetched `inputs` alone.

This is the same least-privilege shape as scopes, one level down: scopes bound what the connection can ever do, the allowlist bounds what this workflow can do with it. Since the allowlist is the only thing standing between a scheduled run and a write action, it is the field to read first when reviewing any workflow file.

`px0 run <id> --dry-run` executes the run with write tools stubbed: calls are recorded with their arguments and return a synthetic success, so a posting workflow can be exercised once before it is scheduled.

## Workflow model

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#workflow-model)

### Definition format

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#definition-format)

A workflow is a Markdown file with YAML frontmatter. The frontmatter is the machine contract; the body is the instruction the model receives.

```yaml
---
id: standup-summary
kind: workflow
version: 1
description: Draft yesterday's standup update from commits, PRs, and reviews.
trigger:
  manual: true
  schedule: "0 9 * * 1-5"    # optional; standard five-field cron, machine local time
guidelines:
  - commit-messages.md
  - code-review/common.md
inputs:
  - id: activity
    tool: github.list_my_activity
    args:
      repos: "{{config.connectors.github.repos}}"
      since: -1d
  - id: events
    tool: calendar.list_events
    args:
      window: yesterday
    optional: true
tools: [github.get_pr]        # optional; tools the model may call mid-run
output:
  target: file                # stdout | file
  path: outputs/standup-{date}.md
  format: markdown
timeout: 120s
---
Write my standup update in first person, three sections: yesterday, today, blockers.
Yesterday comes from {{activity}}, with meetings from {{events}}.
Today comes from open PRs and assigned issues.
Keep it under 120 words.
```

Frontmatter fields:

-   `trigger.manual` and `trigger.schedule` may both be set. A workflow with neither can only be invoked as a step of another workflow. Cron expressions are evaluated in the machine's local timezone, as the OS reports it, including DST transitions; there is no timezone field.
-   `guidelines` is the explicit list of guideline files inlined into the prompt, paths relative to `guidelines/`, described above. Omitted or empty means no guidelines are inlined.
-   `inputs` are deterministic prefetch, run before generation, each with an `id` that binds its result into the body as `{{id}}`. An input is exactly one of four kinds:
    -   `tool:` with `args:`, calling any tool in the normalized namespace,
    -   `retrieve: {query, k}`, pulling passages from the knowledge library,
    -   `source: stdin`, binding piped text,
    -   `workflow: <id>`, running another workflow and binding its output. Input tools must be read tools; anything that writes belongs in `tools:`, where the decision to call it is the model's and is recorded as such. `args` values may reference earlier inputs and config keys by the same `{{ }}` syntax. Results live in memory for the duration of the run and are discarded afterwards.
-   `tools` is the allowlist: what the model may call while generating. Inputs are for what is known upfront; tools are for what is not, and for anything the workflow does to an external system.
-   `output.target` is `stdout` for interactive runs and piping, or `file` with a templated `path`, resolved against `[output] path`. Scheduled runs must target `file`; the produced text is also available via `px0 runs output <run-id>` while the run record is retained. Posting is not an output target: a workflow that publishes does so through a write tool.

### Execution pipeline

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#execution-pipeline)

`px0 run <id>` executes these stages. Each stage failure is written to the run record and aborts the run.

1.  Load and validate the workflow file against the schema: every `guidelines[]` path exists, every `inputs[].tool` and `tools[]` entry exists in the current namespace, and no input tool writes.
2.  Acquire the store lock (flock on `.state/lock`), checkpoint hand edits to workflows and guidelines into versions, and release. The lock is reacquired later only to route a `file` output, so concurrent runs of different workflows overlap freely.
3.  Check connection status and native PAT expiry, then resolve inputs in declaration order, substituting `{{ }}` references as each completes. A failed optional input degrades with a note in the output; a failed required input aborts after the retry policy is exhausted.
4.  Render the prompt: inline the `guidelines` list in order, then inputs, then the body. Each inlined file, its claim ids, and the version it was read at go into the run record.
5.  Start the per-run tool endpoint containing exactly the `tools:` allowlist, if any.
6.  Invoke the model backend; the model may call allowlisted tools during generation, and every tool call, including its arguments and whether it wrote, goes into the run record.
7.  Route the output to its target and record it, retrievable later with `px0 runs output <run-id>`.
8.  Close the run record: workflow id, trigger, whether the run was late, inputs resolved, guidelines inlined with versions, tool calls made, model, token counts, duration, outcome.

### Model backend

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#model-backend)

The backend is the user's coding agent CLI, shelled out to in non-interactive mode (for example `claude -p`) with the per-run tool endpoint attached as an MCP server. It is configured in `config.toml` as `harness_cmd`. There is no direct-API backend: reusing the harness reuses the user's existing auth, model choice, and rate limits.

The runner treats the backend as text and tool-calls in, text out. The exact invocation, the expected output framing, and the failure modes are pinned in the runner and verified by `px0 doctor`, which runs a trivial prompt through the configured harness and reports what came back. Workflows must not depend on harness-specific features beyond that contract.

### The builder

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#the-builder)

`px0 new "<description>"` turns a sentence into a working workflow:

```shell
px0 new "every friday afternoon, summarize the PRs I reviewed this week and post it to #eng"
```

The builder runs four phases, interactively:

1.  Plan. The model backend parses the description into a draft contract: trigger (`0 16 * * 5`), required connections (github, slack), candidate inputs and tools, output target. The plan is printed for confirmation, with any write tool it intends to grant called out by name.
2.  Feasibility. Every requirement is checked against the tool namespace: does the service exist in the Composio catalog or the native adapter, does it expose a tool with the parameters the plan needs, is the trigger expressible. Anything infeasible is named specifically ("no tool exposes this; closest available: `github.list_review_comments`") rather than silently dropped. If the core of the request is infeasible, the builder stops here and says so.
3.  Connect. For each required service without a connection, the builder runs the same flow as `px0 connect`: a Composio auth link with only the scopes the planned tools need, write scope included only when a planned tool writes. Existing connections are reused, never re-authed.
4.  Generate. The builder writes the workflow file: minimal `tools:` allowlist, `inputs` with concrete tool names and args, a `guidelines:` list chosen by matching the task against the topic files present in the store and shown to the user for confirmation, and a `file` output target with a templated path when the workflow is scheduled. It then dry-runs once with write tools stubbed, shows the draft output and the calls it would have made, and saves on confirmation, producing a `v1` attributed to `builder`.

The builder never grants a workflow more than the plan requires: no unrequested write tools, no extra scopes, no wildcard allowlists. Widening is a manual edit to the file, which is the point.

### Composition and piping

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#composition-and-piping)

Workflows compose in two equivalent ways.

Shell piping, for ad hoc chains:

```shell
px0 run collect-week-activity --output stdout | px0 run weekly-digest --stdin
```

`--stdin` binds the piped text to the workflow input declared `source: stdin`. `--output stdout` overrides the declared target for that invocation.

Declared pipelines, for chains that run on a schedule:

```yaml
---
id: friday-wrap
kind: workflow
trigger:
  schedule: "0 16 * * 5"
pipeline: [collect-week-activity, weekly-digest, standup-summary]
output:
  target: file
  path: outputs/friday-wrap-{date}.md
---
```

Pipeline semantics:

-   Stages run sequentially; each stage's output binds to the next stage's `stdin` input.
-   Intermediate outputs live in memory and are written only to the run log; only the terminal stage's output is routed to the declared target.
-   The pipeline gets one run id; each stage is a child record, so `px0 why` can walk into any stage.
-   A stage failure aborts downstream stages and marks the run failed. Tool calls already made by earlier stages are not undone; a pipeline that writes should put the writing stage last.
-   Pipelines cannot nest in v1. A pipeline may reference only leaf workflows, and cycles are rejected at validation.

### Scheduling

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#scheduling)

Scheduling is owned by `px0d`, a small daemon installed by the install script or by `px0 daemon install`: a systemd user unit on Linux, including WSL2 with systemd enabled, and a launchd agent on macOS. Where user services are unavailable, `px0 daemon install --fallback-cron` writes a managed crontab block instead, with reduced semantics (no missed-fire recovery, no log rotation, no background ingest queue).

The daemon is deliberately dumb. It does five things and nothing else:

-   Watches `workflows/` and evaluates each `trigger.schedule` cron expression in machine local time, spawning `px0 run <id> --quiet` as a child process at fire time.
-   Recovers missed fires from the current day, described below.
-   Checks Composio connection status ahead of scheduled runs and warns on native PAT expiry.
-   Runs the background ingest queue, the nightly knowledge reindex, the nightly version checkpoint, and the update check.
-   Enforces log retention.

Missed-fire recovery, since laptops sleep:

-   The daemon records the last fire time per workflow in `.state/schedule.json`.
-   On start or wake, for each scheduled workflow it computes the fires that fell between the last recorded fire and now, discarding anything earlier than local midnight of the current day. A standup missed on Tuesday is not produced on Thursday; a standup missed at 09:00 runs when the machine wakes at 11:40.
-   Each missed fire from the current day is recovered as its own run, in chronological order. There is no coalescing: a workflow that missed six fires today runs six times on wake.
-   A recovered run is marked late in its run record and its output header states both times ("scheduled 09:00, ran 11:40"), so a stale standup is visibly stale.
-   Recovery is a scheduling behavior, not a per-workflow field; there is nothing to configure.

A scheduled run that fails is marked in its run record; `px0 runs list --failed` and the runs TUI surface it, and `px0 doctor` reports failures since last checked. `px0 daemon status` shows liveness, next fire times, missed fires pending recovery, and drift between declared schedules and loaded ones; `px0 daemon start|stop|restart|logs` manage the service.

### Runs: history, re-execution, and logs

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#runs-history-re-execution-and-logs)

`px0 runs` opens a TUI over run history; every action has a CLI equivalent.

-   The list view shows runs newest first: workflow, trigger (manual, schedule, late, pipeline, ingest, ask), start time, duration, outcome, and a marker when the run called a write tool. Filterable by workflow, outcome, write activity, and date range.
-   Enter on a run opens detail: the rendered prompt, guidelines inlined with the version each was read at, inputs resolved, tool calls with their arguments, connector timings, and the output or failure. From detail, one keystroke each to re-run (`r`), page the raw log (`l`), print the output (`o`), or open the provenance chain (`w`).
-   Re-run executes the workflow fresh with current inputs, including its write tools. `--replay` is deliberately absent: inputs are not cached, so an identical replay is impossible.
-   CLI equivalents: `px0 runs list [--workflow id] [--failed] [--since 7d]`, `px0 runs show <run-id>`, `px0 runs output <run-id>`, `px0 runs rerun <run-id>`, `px0 runs logs <run-id> [--follow]`.

Two artifacts exist per run, both under `[logs] path`, both subject to retention policy.

| Artifact | Path | Content |
| --- | --- | --- |
| Run record | `<logs>/records/<date>/<id>.json` | Structured: ids, timings, tool calls, outcome, output |
| Raw log | `<logs>/runs/<date>/<id>.log` | Raw: prompts, connector responses, stderr |

The daemon enforces retention daily: rotation caps a single log file's size, deletion removes artifacts older than `retention_days`, failed runs are kept longer via `retention_days_failed`, and records outlive logs via `record_retention_days`. Runs that called a write tool are exempt from deletion, because the answer to what this thing posted should not expire on a disk policy.

Run records cover what the system did outside the store. Versions cover what it did to workflows and guidelines. The two together are the account, and only the first ages out.

## Built-in workflows

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#built-in-workflows)

The system ships its own behavior as workflow files in the same folder, editable like any other, each with its `guidelines:` list hardcoded to the topic files it needs.

| Workflow | Trigger | Purpose |
| --- | --- | --- |
| `standup-summary` | cron weekdays 09:00 | Draft standup from yesterday's activity |
| `pr-precheck` | manual | Run code-review guidelines against a local diff |
| `review-pr` | manual, takes URL | Draft review comments for a PR; posts only if granted |
| `consolidate` | cron weekly + manual | The review session over proposals, decay, contradictions |
| `skills-build` | after consolidate | Compile guidelines into harness skill bundles |
| `weekly-digest` | cron Friday 16:00 | Week in review: merged, reviewed, learned |

The shipped `review-pr` and `weekly-digest` files declare read-only allowlists and write their output to a file; posting is a one-line edit to `tools:` plus a connection with write scope.

`consolidate` presents, in one capped session: new proposals ranked by repetition, claims due for decay (measured from the last version that changed the section), contradiction pairs, guideline files no workflow references, and drift between edited skill bundles and their source claims. The session closes as one change, so a consolidation is revertible in a single command.

## Learning loop

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#learning-loop)

One mechanism, three signal sources, all landing in pending proposals.

-   Ingestion: the proposal pass over each newly added knowledge file, evidence being the source file plus a location anchor.
-   Review decisions: edits the user makes to proposed text in `px0 guidelines review` and `consolidate` are applied and stored as versions carrying the evidence, so the shape of a claim converges over successive sessions.
-   Verification: `pr-precheck` results double as evidence. A claim the user's own diffs repeatedly violate is flagged in consolidation rather than silently trusted.

Automatic processes only file proposals; the user disposes of them in `consolidate`. Nothing polls an external system for evidence, so the loop advances exactly as fast as the user reads, ingests, and prechecks.

## Provenance

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#provenance)

`px0 why <id>` walks the chain for any run, answer, output, or claim.

-   For a run, answer, or output, it reads the run record: which workflow, which guidelines were inlined at which versions, which inputs were resolved, which tools were called and what they did, which retrieved passages were used. It says plainly when a record has aged out.
-   For a claim id, it reads version history: every version of that file that touched the section, the evidence recorded with each, and any rename aliases, with permalinks from the evidence.

`px0 guidelines log <claim-id>` and `px0 guidelines revert <claim-id>` are the section-level view over the same history. Both work on a bare store with nothing else installed, which is the point of owning the versioning layer rather than borrowing one.

## Self-update

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#self-update)

px0 updates itself, through the same manifest and signature machinery as the installer.

```shell
px0 update                      # check, download, verify, swap, migrate, restart daemon
px0 update --check              # report available version and changelog, change nothing
px0 update --channel beta       # switch channels
px0 update rollback             # restore the previous binary and its store schema
px0 version                     # binary version, store schema version, harness, qmd, Bun
```

Semantics:

1.  The current binary queries the release manifest for the configured channel and compares semantic versions. `--check` stops here.
2.  The artifact is downloaded to a temp path, its checksum and signature verified against a pinned public key, and rejected outright on mismatch.
3.  The daemon is stopped, the binary replaced atomically (write beside, rename over), and the previous binary retained as the rollback target.
4.  Pinned runtime dependencies are upgraded if the new release pins different versions of Bun or qmd, into the same `~/.local/share/px0/runtime/` the installer uses. A qmd upgrade that changes the index format triggers a reindex, reported as part of the upgrade.
5.  Store migrations run if the new binary declares a higher schema version than `.state/schema`. Migrations are forward-only and each is recorded as a change, so the pre-migration state is revertible.
6.  The daemon is reinstalled if its unit definition changed, then restarted, and `px0 doctor --quick` confirms the result.

Policy: the daemon checks weekly and surfaces an available update in `px0 doctor` and the runs TUI. It never installs on its own unless `[update] auto_install` is set, which defaults to false, because a tool that can post to Slack on a schedule should not change its own behavior unattended. Built-in workflow files are refreshed on update only when the user has not modified them, which the version history answers exactly; a modified built-in is left alone and the new upstream version is offered as a diff in `px0 doctor`.

## CLI surface

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#cli-surface)

```shell
px0 init [dir]                  # scaffold store, install starters and daemon
px0 new "<description>"         # builder: plan, check feasibility, connect, generate
px0 run <workflow> [--quiet] [--stdin] [--output stdout] [--dry-run]
px0 ask "<question>" [--k 8] [--sources]
px0 list [workflows|guidelines|knowledge]
px0 connect setup-composio | <service> [--native --pat] | list | rotate <service> | remove <service>
px0 tools list [service]        # every tool across connections, read/write marked
px0 daemon install|status|start|stop|restart|logs
px0 runs [list|show|output|rerun|logs]
px0 knowledge add <source> [--wait|--no-propose] | refresh <path>
px0 guidelines review|log|revert|link|alias
px0 versions list|show|diff|revert|prune
px0 changes list|show|revert
px0 search "<query>" [--k 5]    # raw passages; also: px0 search reindex
px0 skills build
px0 why <id>
px0 store export <dir>          # content and history, credentials excluded
px0 update [--check|--channel|rollback]
px0 version
px0 doctor                      # credentials, daemon, harness, index, versions, locks, schema
```

All commands support `--json`. Exit codes: 0 success, 1 user error, 2 connector failure, 3 model backend failure, 4 store or version integrity failure.

## Configuration

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#configuration)

```toml
# ~/.px0/config.toml
[model]
harness_cmd = "claude -p"

[knowledge]
path = "~/.px0/knowledge"       # any folder works, e.g. an existing notes vault

[output]
path = "~/.px0/outputs"

[connectors]
provider = "composio"           # composio | native
retries = 3                     # per-run transient retries, exponential backoff

[connectors.github]
repos = ["org/service-a", "org/service-b"]

[proposals]
max_per_consolidation = 10

[versions]
keep_all = true                 # false enables the cap below
max_versions_per_file = 200

[logs]
path = "/var/log/px0"           # falls back to ~/.local/state/px0/logs if not writable
retention_days = 14
retention_days_failed = 60
record_retention_days = 365
max_file_size_mb = 20

[update]
channel = "stable"              # stable | beta
check = true
auto_install = false

[retrieval]
backend = "qmd"
k_default = 5
rerank = true
```

`config.toml` holds no credentials; they live in `.state/credentials.toml` at mode 0600. `config.toml` is itself versioned, so a bad edit is revertible.

## Security posture

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#security-posture)

-   Local execution only; the only network calls are connector fetches, connector writes, the model backend, the update check, and knowledge ingestion.
-   Writes are possible and scoped, not prevented. A workflow can post, comment, or send exactly when its allowlist names a write tool and the connection holds the scope for it. Write tools are marked in `px0 tools list`, called out in the builder's plan, stubbed under `--dry-run`, flagged in the run listing, and their run records are never deleted by retention.
-   Least privilege twice: connection scopes bound what a service grant can ever do; per-workflow `tools:` allowlists bound what a single run can do with it. Input tools are read-only by validation, so unattended writing is always a decision recorded in the workflow file.
-   Write calls are not deduplicated. A late run, a manual run, and a `runs rerun` of the same workflow each call their write tools, so a workflow that posts can post more than once in a day. The allowlist is the control; there is no idempotency layer behind it.
-   Credentials held locally are one Composio API key and any native PATs: plaintext by explicit decision, mode 0600, inside `.state/`, excluded from `px0 store export`, verified by `px0 doctor`.
-   Raw prompts and connector responses live only in the configurable log directory, outside the store.
-   Install and update artifacts are checksum and signature verified against a pinned key before execution or replacement, and the previous binary is retained for rollback.
-   `work/` folders under `guidelines/` and `knowledge/` are excluded from skill bundles, from outputs whose destination is not local, and from any run whose allowlist contains a write tool. They still reach the model backend, which is the one exit that exists by design.
-   The daemon is inspectable: `px0 daemon status` shows exactly what is scheduled to run and when, and the service definition it installs is a plain unit file the user can read.

## Milestones

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#milestones)

1.  Store, schemas, `install.sh`, `px0 init`, the versioning layer (blobs, manifest, checkpoint scan, list, show, diff, revert, changes), `px0 run` with the harness backend, `guidelines:` inlining, `standup-summary` and `pr-precheck` working manually, piping.
2.  `px0 connect` with Composio setup and the native GitHub PAT path, the normalized tool namespace with read/write marking and parameter listing, `px0 tools list`, tool-based inputs, per-run allowlists, `--dry-run`.
3.  `px0d` with install, missed-fire recovery, and log retention; `px0 runs` list, output, and logs; run records and `px0 why`; `px0 guidelines review`; `px0 update` with signature verification, migrations, and rollback.
4.  `consolidate` and the proposal flow end to end, `px0 guidelines review`, section-level history, rename aliasing, decay, and contradiction pairs, with `pr-precheck` verification as the first proposal source.
5.  `px0 knowledge add` for web pages and local documents; `px0 search` and `px0 ask` with qmd behind the retrieve interface; declared pipelines; `px0 runs` TUI with re-run.
6.  The builder (`px0 new`): plan, feasibility, connect, generate, including guideline selection.
7.  YouTube ingestion with transcript stubs, playlists and the background queue; the qmd MCP connector; `skills-build`.

## Resolved decisions

[](https://gist.github.com/arpitbbhayani/7d6a17cfd6d68e4741d356c1fdcd7420#resolved-decisions)

-   Naming: the CLI is `px0`, the daemon is `px0d`, the store defaults to `~/.px0`.
-   Distribution: a curl-pipe `install.sh` is the supported install path, installing the signed binary plus pinned Bun and qmd into a px0-owned runtime directory, then scaffolding the store and offering the daemon. Models download lazily on first index.
-   Versioning is owned by px0 and covers `workflows/`, `guidelines/`, and `config.toml` only. Create, edit, and delete each produce a version; sessions group into changes; revert writes a new version rather than rewriting history; hand edits are captured by a checkpoint scan. No git dependency, no git integration.
-   Knowledge is not versioned. It is bulk, rebuildable from source, and versioning it would put a large hashing scan on every command.
-   Guidelines reach a workflow through an explicit `guidelines:` list in frontmatter: hardcoded in built-ins, chosen by the builder for generated workflows, editable by hand, with a consolidation report on unreferenced files to surface staleness. No folder globs, no language-context inference.
-   Simplicity over metadata: guideline files carry no frontmatter; scope and identity derive from path and heading, and history derives from versions.
-   Renames are detected at capture time by body similarity at a 0.7 threshold, recorded as aliases, and correctable with `px0 guidelines alias`.
-   Inputs are tool calls, not a connector verb vocabulary, and are read-only by validation.
-   Writes are in scope, bounded by per-workflow allowlists rather than a global prohibition, and are not deduplicated: no idempotency keys, no write caps, no confirmation on late runs.
-   No polling of external systems for evidence. Proposals come from ingestion, from the user's own edits during review, and from `pr-precheck` verification.
-   Connectors: Composio is the default provider; a narrow native path (GitHub PAT) exists for third-party refusers. The network dependency on Composio is accepted; transient failures retry in-run, and the next scheduled fire is the only other retry.
-   Model backend: the user's coding agent CLI, and nothing else. No direct-API path.
-   Scheduling: missed fires are recovered on wake for the current day only, each as its own run in order, marked late in the record and the output header. No coalescing, nothing to configure.
-   Retrieval: local only, over `knowledge/` alone, via qmd behind an internal `retrieve` interface. A qmd index format change forces a full reindex, which is accepted rather than migrated around.
-   Text only: ingestion is text in, text out. A video without a published transcript is filed as a metadata stub holding the URL and what could be found, indexed but not proposed from, and upgradeable later with `px0 knowledge refresh`.
-   Platform: WSL2 with systemd enabled is treated as Linux; the cron fallback covers everything older or stranger.
