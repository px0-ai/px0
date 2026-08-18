# Phase 3: Daemon and log follow-through

## Status quo this phase changes

Three places in the daemon/logging surface print or accept something the code never actually does:

1. **The background ingest queue is never drained.** `px0/knowledge.py:198-206`: a YouTube playlist source writes a job file to `.state/ingest/*.json` and raises `IngestError` telling the user to "run `px0 daemon start` to process it in the background." But `px0/daemon.py:107-118` (`run_nightly`) never reads `.state/ingest/` -- it only runs the checkpoint scan, `retrieval.reindex`, and `runs_mod.apply_retention`. A queued playlist sits in `.state/ingest/` forever.
2. **`px0 daemon logs` is a stub.** `px0/cli.py:352-355`: `if args.daemon_cmd == "logs": print("this build keeps no separate daemon log; see `px0 runs logs <id>` for the runs it spawned")`. There is genuinely no daemon-level log file anywhere in the codebase (`grep -rn "daemon.log" px0/` finds nothing before this phase).
3. **`px0 runs logs --follow` doesn't follow.** `px0/cli.py:877-879` declares the flag with `help="not supported; prints once"`, and `cmd_runs`'s logs branch (`403-405`) is `print(runs_mod.read_raw_log(config, args.run_id))` -- it ignores `args.follow` entirely.

All three are "the daemon/logging system does less than its own CLI promises" -- one coherent capability: operational visibility that actually works.

## Engineering section

### Dependencies on prior phases

Depends on Phase 1 only for the shared pytest harness. No interface from Phase 1 or Phase 2's application code is consumed. Independent of Phases 4, 5, and 6.

### What already exists (reused, not rebuilt)

- `px0/knowledge.py`'s `enumerate_playlist()` (`166-177`) and `add()` (`186-253`) -- playlist enumeration and single-video ingestion are both fully implemented; this phase only adds the loop that calls them for queued jobs.
- `px0/daemon.py`'s `run_nightly()` (`107-118`) -- the one new call is added inside the existing `try`/`except`-per-step pattern already used for `reindex()` (`113-116`), so one broken video doesn't block the rest of housekeeping, matching that function's own established error-isolation style.
- `px0/runs.py`'s `resolve_logs_path()` (`16-32`) -- reused verbatim as the base directory for the new `daemon.log` file, keeping it out of the store per spec.md:107 ("Run logs and run records do not live in the store at all").
- `px0/paths.py`'s `ingest_dir()` (`52-54`) -- reused; only a sibling `ingest_failed_dir()` is added.

### Components touched

| File | Change |
| --- | --- |
| `px0/knowledge.py` | Add `process_ingest_queue(home, config) -> dict`. |
| `px0/daemon.py` | `run_nightly()` (`107-118`): add `report["ingest_queue"] = knowledge_mod.process_ingest_queue(home, config)` inside a `try`/`except`, same pattern as the reindex step. Add `_log_event(config, message)` and call it from `serve()` (start/stop), `tick()`/`spawn_run()` (each spawn), and `run_nightly()` (start/end). New import: `from px0 import knowledge as knowledge_mod`. |
| `px0/paths.py` | Add `ingest_failed_dir(home)` -> `.state/ingest/failed/`. |
| `px0/runs.py` | Add `tail_lines(path: Path, poll_interval: float = 1.0)` -- a generator yielding new lines appended to `path`, used by both `px0 daemon logs --follow` and `px0 runs logs --follow`. |
| `px0/cli.py` | `cmd_daemon`'s `logs` branch (`352-355`): print `daemon.log`'s content, or tail it under `--follow`. `cmd_runs`'s `logs` branch (`403-405`): honor `--follow` via `runs_mod.tail_lines`. Argparse: add `--follow` to `daemon_sub.add_parser("logs")` (`863`); update the `help` string on `runs logs --follow` (`879`) since it's no longer accurate. |
| `tests/test_ingest_queue.py` (new) | Unit tests for `process_ingest_queue`. |
| `tests/test_daemon_logs.py` (new) | Unit tests for `_log_event` and `tail_lines`. |

### Data model

**Ingest job file** (`.state/ingest/<slug>.json`, existing shape from `px0/knowledge.py:201-202`, extended with two fields this phase adds):

```json
{
  "source": "https://youtube.com/playlist?list=...",
  "kind": "youtube-playlist",
  "to": null,
  "queued_at": "2026-08-15",
  "attempts": 1,
  "last_error": "3 of 12 videos failed: ['https://youtube.com/watch?v=abc: pdftotext not found', ...]"
}
```

`attempts` and `last_error` are absent on first write (existing behavior, `knowledge.py:201-202` unchanged) and added by `process_ingest_queue` only on a partial-failure retry.

**Failed job file** (`.state/ingest/failed/<slug>.json`, new): the same job JSON, written once `attempts` reaches `MAX_INGEST_ATTEMPTS = 3` (a new module constant in `knowledge.py`; chosen because it matches the existing `[connectors] retries = 3` convention used elsewhere in the codebase, `px0/config.py:20`, for consistency rather than a measured number), with the original job file deleted from `.state/ingest/` so it stops being retried nightly.

**`daemon.log`** (new, `<logs.path>/daemon.log`, plain text, one line per event):

```
2026-08-18T09:00:03+00:00 tick: spawned standup-summary (on-time)
2026-08-18T09:00:03+00:00 nightly: checkpoint=2 changed, reindexed=184 passages, retention removed 3 logs
2026-08-18T14:22:10+00:00 stop: SIGTERM received
```

Not versioned, not a run record -- a plain append-only text file, matching the existing `append_raw_log` pattern in `px0/runs.py:70-79` (same idea, different file). No rotation logic is added in this phase; `daemon.log` is small (one line per tick-with-activity, not per poll) and out of scope for the retention system, which only governs `records/` and `runs/` (`px0/runs.py:132-162`) -- flagged as a follow-up if `daemon.log` growth ever becomes a real problem, not invented here as unneeded scope.

### Key flows

**Nightly playlist drain (`run_nightly`, called from `daemon.serve()`'s once-a-day check at `px0/daemon.py:155-158`, and manually via a new code path -- see below):**

1. `process_ingest_queue(home, config)` lists `*.json` in `paths.ingest_dir(home)` (skips the `failed/` subdirectory since `ingest_dir` globs non-recursively... explicitly: use `ingest_dir(home).glob("*.json")`, not `rglob`, so `failed/` is never re-read).
2. For each job: `enumerate_playlist(job["source"])` re-fetches the current video list (playlists can grow between passes; re-enumerating is correct, not wasteful -- it's one HTTP GET per nightly pass per queued playlist).
3. For each video URL: compute its destination via `knowledge._dest_path(home, config, job.get("to") or "docs", video_url)` (mirrors `add()`'s own resolution at `knowledge.py:194-195, 230`) and skip if that file already exists -- this is what makes repeated nightly passes idempotent instead of re-ingesting every video every night.
4. For each not-yet-ingested video: call `knowledge.add(home, config, video_url, to=job.get("to"))` inside a `try/except IngestError`, collecting failures (a stub written for "no transcript yet" is **not** a failure -- `add()` already handles that gracefully by writing a stub, per `knowledge.py:235-241`; only a raised `IngestError`, e.g. from a network failure, counts).
5. If zero failures: delete the job file.
6. If any failures: increment `attempts`; if `attempts < MAX_INGEST_ATTEMPTS`, rewrite the job file with the new `attempts`/`last_error`; if `attempts >= MAX_INGEST_ATTEMPTS`, move it to `ingest_failed_dir(home)` (write there, delete original) so it stops retrying.
7. Returns `{"jobs_processed": n, "videos_ingested": n, "jobs_given_up": n}` for the nightly report.

**Manual drain.** Spec.md doesn't gate playlist processing behind "only nightly" -- `knowledge.py:204`'s own error message says "run `px0 daemon start`" (the long-running daemon), not "wait until tomorrow." Since `run_nightly` is already the housekeeping entry point the daemon calls once per calendar day (`px0/daemon.py:155-158`), and there is no existing "run housekeeping now" CLI verb, this phase does not add one -- a user who wants a queued playlist processed immediately can already ingest individual video URLs directly, exactly as `knowledge.py:205`'s existing message also suggests. Adding an on-demand `px0 knowledge process-queue` verb is a small, low-stakes future addition, not required to close this gap, and is left out to keep this phase's scope to what the spec and the existing error message actually promise.

**`px0 daemon logs [--follow]`:**

1. Without `--follow`: print `daemon.log`'s full content (or "no daemon log yet" if the file doesn't exist -- the daemon has never run).
2. With `--follow`: print existing content, then call `runs_mod.tail_lines(daemon_log_path)`, printing each yielded line until `KeyboardInterrupt` (same UX as `tail -f`).

**`px0 runs logs <id> --follow`:**

1. Without `--follow`: unchanged (`runs_mod.read_raw_log`).
2. With `--follow`: print existing content, then `runs_mod.tail_lines(runs_mod.log_path(config, run_id))`, stopping automatically once `runs_mod.read_record(config, run_id)` shows a terminal `outcome` (not just on `KeyboardInterrupt`, since a finished run's log will never grow again) -- checked every `poll_interval` alongside the tail.

**`tail_lines(path, poll_interval=1.0)`:**

```python
def tail_lines(path: Path, poll_interval: float = 1.0):
    """Yields lines appended to `path` after this call starts, polling
    every poll_interval seconds. Never returns on its own -- the caller
    breaks out (e.g. on a terminal run outcome, or KeyboardInterrupt)."""
    with open(path) as f:
        f.seek(0, 2)  # start at current end-of-file
        while True:
            line = f.readline()
            if line:
                yield line
            else:
                time.sleep(poll_interval)
```

### Non-functional requirements

- `tail_lines`'s `poll_interval` default (1.0s) is not a measured latency budget -- there is no existing polling convention in the codebase to match against for log tailing specifically (the daemon's own `POLL_INTERVAL_SECONDS = 30`, `px0/daemon.py:30`, governs schedule ticking, a different concern). 1.0s is a reasonable interactive-CLI default; revisit only if manual QA finds it too chatty or too laggy.
- `process_ingest_queue` makes one network call per queued playlist per nightly pass (`enumerate_playlist`) plus one call sequence per not-yet-ingested video (`add()`'s existing per-source cost, unchanged) -- no new rate-limiting is added since none exists for `knowledge.add()` today either.

### Failure modes

| Failure | Covered by test? | Error handling | Visible to caller? |
| --- | --- | --- | --- |
| A queued playlist URL 404s on `enumerate_playlist` | Yes | Whole job's exception caught at the job level (not per-video), counted as a failure, retried next night up to `MAX_INGEST_ATTEMPTS` | Yes, in `last_error` and the nightly report |
| One video in a 20-video playlist fails, 19 succeed | Yes | 19 are ingested and not re-attempted (idempotency check); only the 1 failure is retried next pass | Yes, in `last_error` |
| A job fails `MAX_INGEST_ATTEMPTS` times | Yes | Moved to `ingest_failed_dir`, stops consuming nightly-pass time forever | Yes, file is inspectable; not surfaced in `px0 doctor` in this phase (noted as a follow-up, not invented here) |
| `daemon.log` unwritable (disk full, permissions) | No (would require a read-only-filesystem fixture; documented as a known gap) | `_log_event` wraps its write in a `try/except OSError: pass` -- a logging failure must never crash the scheduler loop itself, mirroring the existing philosophy at `daemon.py:113-116` (reindex failure is captured, not raised) | No -- silent by design, since a broken log must not stop scheduling; this asymmetry (silent vs. visible) is deliberate and stated here rather than left implicit |
| `px0 runs logs --follow` on a run id that never gets a terminal outcome (process died without writing one) | No (would require simulating a crashed run; documented as a known gap) | Follows forever until `KeyboardInterrupt`, same as `tail -f` on a file nothing is writing to -- not a crash, just an unbounded wait, which is the expected `tail -f` behavior | Yes, user can Ctrl+C |

### Test plan

Uses the pytest harness established in Phase 1.

| Layer | What | Count |
| --- | --- | --- |
| Unit | `process_ingest_queue` ingests every video in a fake enumerated playlist | +1 |
| Unit | `process_ingest_queue` skips already-ingested videos on a second pass | +1 |
| Unit | Partial failure increments `attempts` and preserves the job file | +1 |
| Unit | `MAX_INGEST_ATTEMPTS` reached moves the job to `ingest_failed_dir` | +1 |
| Unit | `_log_event` writes a timestamped line, swallows `OSError` | +2 |
| Unit | `tail_lines` yields lines appended after the generator starts (not before) | +1 |
| Integration | `run_nightly` includes `ingest_queue` in its report and doesn't raise when a job fails | +1 |
| Integration | `px0 daemon logs` prints `daemon.log` content; `px0 runs logs --follow` stops at a terminal outcome | +2 |

### Rollout

No data migration. `.state/ingest/failed/` is created lazily on first use. Rollback: revert the commit; any job files already moved to `failed/` are inert JSON the old binary simply never reads (matches its pre-phase behavior of never reading the queue at all).

## Product section

**Phase goal:** things the daemon and CLI already claim to do -- process queued playlists in the background, keep a daemon log, follow a log live -- actually happen.

**User story:** the user runs `px0 knowledge add https://youtube.com/playlist?list=...`, sees it queued, starts the daemon, and the next morning every video in that playlist (with a published transcript) is a real knowledge file, not a job sitting untouched in `.state/ingest/`.

**In scope:**
- Nightly draining of `.state/ingest/*.json` playlist jobs, with per-video idempotency and a bounded retry-then-give-up policy.
- `px0 daemon logs` shows real content; `px0 daemon logs --follow` tails it live.
- `px0 runs logs --follow` actually follows instead of printing once.

**Out of scope (deferred, no phase currently planned):**
- An on-demand "process the queue now" CLI verb (see Key flows rationale above).
- `daemon.log` rotation/retention (flagged as a future addition if size ever becomes a real problem).
- Surfacing given-up ingest jobs in `px0 doctor` (a natural follow-up, not required to close the stated gap).

**Acceptance criteria:**
1. Adding a playlist, then running `px0 daemon serve` through one nightly cycle (simulated in tests by calling `run_nightly` directly), results in every enumerable, transcript-available video ingested as a knowledge file.
2. A second nightly pass over the same playlist ingests zero already-present videos (verified by call-count assertions on `knowledge.add` in the idempotency test).
3. `px0 daemon logs` after at least one `tick()`/`run_nightly()` call shows at least one line per event; `px0 daemon logs --follow` prints new lines as they're appended (verified by writing to the file from a second thread/process in the test and asserting the generator yields it within `poll_interval * 2`).
4. `px0 runs logs <id> --follow` on a run whose record already shows `outcome: "success"` returns after printing existing content, without hanging.

## Definition of done

- [ ] AC1-4 above pass.
- [ ] `pytest` green with the new tests.
- [ ] The `runs logs --follow` argparse help string (`px0/cli.py:879`) no longer says "not supported."
- [ ] `px0 daemon logs` no longer prints the "this build keeps no separate daemon log" message.
