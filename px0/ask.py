"""px0 ask: retrieval plus generation over knowledge/, nothing else. Never
touches connectors or guidelines."""

from datetime import datetime, timezone
from pathlib import Path

from px0 import harness, retrieval, runs as runs_mod


class AskError(Exception):
    pass


def ask(home: Path, config: dict, question: str, k: int = 5) -> dict:
    if retrieval.index_count(home) == 0:
        raise AskError(
            "the knowledge index is empty or missing; run `px0 search reindex` first"
        )

    passages = retrieval.retrieve(home, config, question, k)
    if not passages:
        raise AskError(
            "no passages matched this question; the index may be stale, "
            "try `px0 search reindex`"
        )

    context = "\n\n".join(
        f"[{p.path}#{p.anchor}]\n{p.text}" for p in passages
    )
    prompt = (
        "Answer the question using ONLY the passages below, from the "
        "user's own knowledge library. Cite sources inline as "
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
