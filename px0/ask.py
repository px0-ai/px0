"""px0 brain ask: retrieval plus generation over brain/, nothing else. Never
touches connectors or guidelines."""

from datetime import datetime, timezone
from pathlib import Path

from px0 import harness, retrieval, runs as runs_mod


class AskError(Exception):
    """Raised when ask() cannot answer: empty/missing index or no matching passages."""
    pass


def ask(home: Path, config: dict, question: str, k: int = 5,
        kind: str | None = None) -> dict:
    """Retrieves the top-k passages from brain/, asks the harness to
    answer using only those passages, and records the exchange as a run.

    Raises AskError if nothing matches.
    Returns {"answer", "passages", "run_id"}."""
    passages = retrieval.retrieve(home, config, question, k, kind=kind)
    if not passages:
        of_kind = f" of kind {kind!r}" if kind else ""
        raise AskError(
            f"no passages{of_kind} matched this question; the index may be "
            f"stale, try `px0 brain reindex`"
        )

    context = "\n\n".join(
        f"[{p.path}#{p.anchor}]\n{p.text}" for p in passages
    )
    prompt = (
        "Answer the question using ONLY the passages below, from the "
        "user's own brain. Cite sources inline as "
        "path#anchor. If the passages do not contain the answer, say so "
        "plainly instead of guessing.\n\n"
        f"--- passages ---\n{context}\n\n--- question ---\n{question}"
    )
    answer = harness.invoke(config, prompt, timeout=90)

    run_id = runs_mod.new_run_id("ask")
    now = datetime.now(timezone.utc).isoformat()
    record = {
        "id": run_id, "workflow_id": None, "trigger": "ask",
        "start_time": now, "end_time": now, "tool_calls": [],
        "question": question,
        "passages": [{"path": p.path, "anchor": p.anchor, "score": p.score} for p in passages],
        "answer": answer, "outcome": "success",
    }
    runs_mod.write_record(config, record)

    return {"answer": answer, "passages": passages, "run_id": run_id}
