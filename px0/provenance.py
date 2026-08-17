"""px0 why: walk the chain for any run, answer, output, or claim."""

from pathlib import Path

from px0 import claims, runs as runs_mod


class WhyError(Exception):
    pass


def why(home: Path, config: dict, target_id: str) -> dict:
    if "#" in target_id:
        log = claims.guidelines_log(home, target_id)
        resolved = claims.resolve_claim(home, target_id)
        if not log:
            raise WhyError(f"no history found for claim {target_id!r}")
        return {"kind": "claim", "claim_id": target_id, "resolved_to": resolved, "history": log}

    try:
        record = runs_mod.read_record(config, target_id)
    except FileNotFoundError as e:
        raise WhyError(str(e))
    return {"kind": "run", "record": record}
