# Phase 4: Real `skills build` compilation

## Status quo this phase changes

`px0/skills.py` is 27 lines. Its own docstring is accurate about what it does: "Copies every `guidelines/*.md` file into `skills/`, mirroring the relative path, except files under a top-level `work/` folder. Overwrites existing files." (`px0/skills.py:1-5, 12-15`). It performs a flat file copy -- no frontmatter, no bundling format, nothing a coding agent's skill-discovery mechanism recognizes. Spec.md's built-in workflow table (line 617) calls this "Compile guidelines into harness skill bundles," and spec.md:769 says work-folder exclusion applies "from skill bundles written into repositories" -- both imply skill bundles have a real, harness-recognized shape, not an arbitrary copy.

## What "harness skill bundle" means, verified (not guessed)

px0's harnesses are `claude`, `gemini`, `pi`, `opencode` (`px0/harness.py:14-19`). Of these, only Claude Code's skill format is documented and verifiable without guessing: a directory `<skill-name>/SKILL.md` with YAML frontmatter, discoverable from `~/.claude/skills/<skill-name>/` (personal, all projects) or `.claude/skills/<skill-name>/` (project-only), confirmed live at `code.claude.com/docs/en/skills`. All frontmatter fields are optional; `name` (defaults to the directory name) and `description` (what Claude uses to decide when to auto-load the skill) are the only two this phase needs. Gemini/pi/opencode have no equivalent documented format reachable during this planning pass -- this phase targets Claude Code only, explicitly, rather than inventing an undocumented format for the other three.

## Why this exists alongside `guidelines:` inlining (the actual product point)

`guidelines:` frontmatter (spec.md:220-236, `px0/runner.py:150-164`) already inlines guideline text into a workflow's prompt deterministically -- that mechanism is complete and untouched by this phase. Skill bundles solve a *different* gap: guidelines are invisible to the user's own interactive `claude` sessions (coding directly in a repo, not through `px0 run`). Compiling them into `~/.claude/skills/` makes Claude auto-load "how the user writes Go code reviews" while the user is just talking to `claude` normally, closing that gap without changing how workflows already work.

## Assumptions (stated explicitly, low-stakes)

1. **`description` is derived mechanically from the guideline file's section headings**, not from a model call. Spec.md:214-219 already establishes the codebase's convention that "everything the old design put in frontmatter is derived from position," and guideline files have no title/summary metadata to draw from (spec.md:201, "no frontmatter and no required structure"). Using the harness to generate a description would turn a currently instant, offline command into one that shells out to the model backend and costs tokens on every build -- a meaningfully different operational profile this phase does not introduce without being asked to. `description` = `"Guidelines: " + "; ".join(section headings)`, truncated to 300 characters with a trailing `"..."` if cut (300 is a practical display-length choice, not a hard platform limit -- Claude Code's own cap on `description` is 1,536 characters, so 300 leaves large headroom while staying skimmable in a skill listing).
2. **Skill directory naming**: a guideline file's relative path, slashes replaced with `-`, extension dropped -- `code-review/go.md` -> `code-review-go`. No collisions are possible since guideline paths are already unique.
3. **Auto-symlink into `~/.claude/skills/` only when the configured harness is Claude.** `px0/skills.py`'s existing scope is `~/.px0/skills/` (spec.md:100, "build output, derived," inside the store) -- that alone is inert since no external tool reads from inside `~/.px0/`. This phase additionally maintains a symlink `~/.claude/skills/px0-<name>` -> `<store>/skills/<name>` for each built skill, but only when `harness.resolve_harness_cmd(config.model.harness_cmd)` starts with `claude` (checked once per build, not per skill) -- writing into another tool's personal config directory for a harness the user isn't even using would be a surprising, unrequested side effect. The `px0-` prefix avoids colliding with the user's own skills or another plugin's namespace.
4. **Stale output is pruned.** The existing `build()` never deletes a skill whose source guideline file was removed or renamed (not previously flagged as a bug since a flat copy has no metadata to notice staleness with; a bundle with frontmatter does). This phase prunes `skills/<name>/` directories (and their `~/.claude/skills/px0-<name>` symlinks) with no matching guideline source, in the same `build()` call, since it's the same "compile" operation and trivial once the bundle format exists -- not a second phase.

## Engineering section

### Dependencies on prior phases

Depends on Phase 1 only for the shared pytest harness. Independent of Phases 2, 3, 5, and 6.

### What already exists (reused, not rebuilt)

- `px0/claims.py`'s `extract_sections()` and `Section` (`45-58`, `24-42`) -- reused verbatim to get each guideline file's heading list for the derived `description`. This is exactly the "don't design what already exists" case: section-splitting is already solved for claim addressing and needs no new logic here.
- `px0/paths.py`'s `guidelines_dir()`/`skills_dir()` (`17-19`, `27-29`) -- unchanged.
- `px0/harness.py`'s `resolve_harness_cmd()` (`56-60`) and `KNOWN_HARNESSES` (`14-19`) -- reused to detect whether the configured harness is Claude.
- The existing `work/` exclusion (`px0/skills.py:20-22`) -- unchanged, carried into the new implementation.

### Components touched

| File | Change |
| --- | --- |
| `px0/skills.py` | Rewrite `build()`: for each `guidelines/*.md` (excluding `work/`), compute the skill name, derive `description` via `claims.extract_sections`, render `SKILL.md` (frontmatter + original guideline body as the skill's instructions), write to `skills/<name>/SKILL.md`. Add `_prune_stale(home)` (deletes orphaned `skills/<name>/` dirs and symlinks). Add `_sync_claude_symlink(home, name)` (creates/repairs the symlink; no-op if harness isn't claude, or removes an existing symlink if the harness was switched away from claude -- see Key flows). |
| `px0/cli.py` | `cmd_skills` (`621-627`): no signature change, but the printed output now reflects directories, not flat files (e.g. `built skills/code-review-go/SKILL.md`). |
| `tests/test_skills_build.py` (new) | Unit tests for description derivation, symlink creation/removal, and pruning. |

No new files beyond the test file; no new public classes (`_prune_stale`/`_sync_claude_symlink` are private module functions, matching `skills.py`'s existing single-function style).

### Data model

`SKILL.md` (new format, replaces the flat-copied `.md` file):

```markdown
---
name: px0-code-review-go
description: "Guidelines: Wrap errors with %w; Context is the first parameter"
---

## Wrap errors with %w

Wrap errors with `fmt.Errorf("...: %w", err)` so callers can use `errors.Is` and `errors.As`.
Bare `%v` wrapping discards the chain.

## Context is the first parameter

`context.Context` is always the first parameter and is never stored in a struct.
```

The body is the guideline file's content, byte-for-byte, unchanged -- only frontmatter is added. `name` is set explicitly (matching the `px0-` prefix used for the symlink) rather than left to default to the directory name, since Claude Code's directory-name default and this phase's own directory name are the same string anyway; setting it explicitly makes the mapping self-documenting inside the file.

### Key flows

**`px0 skills build`:**

1. List `guidelines/**/*.md`, excluding anything under a top-level `work/` folder (unchanged rule).
2. For each file: `sections = claims.extract_sections(text)`; `description = "Guidelines: " + "; ".join(s.heading for s in sections)`, truncated per Assumption 1.
3. Write `skills/<name>/SKILL.md` with the frontmatter above and the original body.
4. `_prune_stale(home)`: list existing `skills/*/` directories, delete any whose name has no corresponding guideline source file this pass.
5. If the configured harness is Claude (checked once): for every skill directory now present, ensure `~/.claude/skills/px0-<name>` exists as a symlink to `<store>/skills/<name>`; for every skill directory pruned in step 4, remove the corresponding symlink if it exists and still points into this store (a symlink pointing elsewhere -- e.g. the user replaced it by hand -- is left alone, not clobbered).
6. If the configured harness is not Claude: skip symlink creation entirely; if any `~/.claude/skills/px0-*` symlinks exist from a prior build under a different harness config, leave them as-is (switching harnesses doesn't retroactively delete a working integration the user may still want) -- this phase only *adds* symlinks when the harness is Claude, it never removes them for a harness-mismatch reason, only for a pruned-source reason (step 5's second half).

**A guideline file is deleted, then `px0 skills build` runs again:**

1. Its `skills/<name>/` directory is pruned (step 4).
2. Its `~/.claude/skills/px0-<name>` symlink is removed (step 5), so Claude Code stops offering a skill for guidance that no longer exists.

### Non-functional requirements

- `build()` remains a pure filesystem operation with no network or model-backend call, preserving its current near-instant runtime (spec.md doesn't state a latency budget for `skills build`; "near-instant" is the existing behavior this phase must not regress, not a new number to invent).
- Symlink creation is idempotent: re-running `build()` twice in a row with no guideline changes produces no filesystem writes on the second run beyond `SKILL.md` overwrites (matching the existing "overwrites existing files" behavior, `px0/skills.py:15`).

### Failure modes

| Failure | Covered by test? | Error handling | Visible to caller? |
| --- | --- | --- | --- |
| `~/.claude/skills/` doesn't exist yet (fresh machine, Claude Code never run) | Yes | Created with `mkdir(parents=True, exist_ok=True)`, same as every other directory-creation call in this codebase (e.g. `px0/store.py:56-66`) | No -- succeeds silently, matches existing directory-creation conventions |
| A guideline file has zero headings (plain prose, no `##`) | Yes | `description` falls back to `"Guidelines from " + relative_path` (a guideline file with no sections is unusual but not invalid per spec.md:201's "no required structure") | No -- degrades gracefully, not an error |
| `~/.claude/skills/px0-<name>` already exists as a real directory (not a symlink), e.g. the user manually created a same-named skill | Yes | Left untouched, not overwritten; `build()` logs (prints) a warning line naming the conflict rather than silently failing or destroying user content | Yes, printed |
| Symlink target's guideline source is renamed (not deleted) via `px0 guidelines alias` | No (would require exercising the full rename-aliasing pipeline from Phase-independent existing code; documented as a known gap) | The old skill name is pruned and a new one built at the new name -- from `skills build`'s point of view this is indistinguishable from delete-then-create, which is the correct behavior for a derived, non-versioned artifact | Yes, via the printed prune/build lines |

### Test plan

Uses the pytest harness established in Phase 1.

| Layer | What | Count |
| --- | --- | --- |
| Unit | `description` derivation from headings, and truncation at 300 chars | +2 |
| Unit | `description` fallback when a file has zero headings | +1 |
| Unit | `work/`-folder exclusion still holds (regression) | +1 |
| Unit | `_prune_stale` removes an orphaned skill directory and its symlink | +1 |
| Unit | Symlink created when harness is `claude`, skipped when harness is `gemini` | +2 |
| Unit | Existing non-symlink directory at the target path is left alone with a warning | +1 |
| Integration | `px0 skills build` end-to-end on the starter guidelines produces valid `SKILL.md` frontmatter (parsed back with `yaml.safe_load`) | +1 |

### Rollout

No versioning impact (`skills/` is explicitly not versioned, spec.md:127). Re-running `build()` on an old flat-copy output simply overwrites each file with the new frontmatter'd version on next build; no migration step is needed since `skills/` is fully derived and rebuildable from `guidelines/` at any time (same property `px0 search reindex` relies on for the retrieval index). Rollback: revert the commit; the next `build()` reverts to flat copies. Any `~/.claude/skills/px0-*` symlinks left behind by a rolled-back version point at whatever `skills/<name>/` now contains (a flat `.md` file, not a `SKILL.md` inside a directory) -- Claude Code simply won't find a `SKILL.md` there and ignores it, which is a harmless dangling reference, not a crash.

## Product section

**Phase goal:** the user's own interactive `claude` sessions (not just px0 workflow runs) automatically load the same guidelines px0 workflows already follow.

**User story:** the user is reviewing a Go PR by hand in a `claude` session inside their editor, not through `px0 run pr-precheck`. Because `px0 skills build` compiled `guidelines/code-review/go.md` into `~/.claude/skills/px0-code-review-go/`, Claude auto-loads it when the conversation is about reviewing Go code, without the user needing to invoke px0 at all.

**In scope:**
- Real `SKILL.md` bundles with `name`/`description` frontmatter, one per guideline topic file.
- Auto-symlinking into `~/.claude/skills/` when the configured harness is Claude.
- Pruning stale bundles/symlinks when a guideline file is deleted.

**Out of scope (deferred, no phase currently planned):**
- Skill bundle formats for gemini/pi/opencode (undocumented; would need a phase of its own once/if each publishes a comparable mechanism).
- `disable-model-invocation` or other advanced frontmatter fields -- guideline skills are meant to auto-load, so the minimal two-field frontmatter is sufficient and nothing else is needed.

**Acceptance criteria:**
1. `px0 skills build` on the starter guidelines (`guidelines/commit-messages.md`, `guidelines/code-review/{go,python,common}.md` -- confirm exact starter set from `px0/starters.py` at implementation time, not guessed here) produces one `SKILL.md` per file with valid, parseable YAML frontmatter containing `name` and `description`.
2. With `model.harness_cmd` resolving to a Claude invocation, after `px0 skills build`, `~/.claude/skills/px0-code-review-go` exists and is a symlink resolving to `<store>/skills/code-review-go`.
3. With `model.harness_cmd` resolving to `gemini -p`, no `~/.claude/skills/px0-*` symlinks are created by a fresh build.
4. Deleting `guidelines/code-review/go.md` and re-running `px0 skills build` removes both `skills/code-review-go/` and its symlink.
5. `work/`-folder guideline files are still excluded from every build (regression, spec.md:769).

## Definition of done

- [ ] AC1-5 above pass.
- [ ] `pytest` green with the new tests.
- [ ] Manual QA: run `px0 skills build` with a real Claude Code install, open a fresh `claude` session in a repo with a Go PR, confirm the compiled skill is offered in `/skills` listing (this one check needs a live Claude Code install and is not automatable in CI).
