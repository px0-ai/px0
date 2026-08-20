"""px0 guidelines why / px0 runs why: walk the chain for any run, answer, output, or claim."""

from pathlib import Path

from px0 import claims, runs as runs_mod


class WhyError(Exception):
    """Raised when why() cannot resolve the given target id to a claim or run."""
    pass


def why(home: Path, config: dict, target_id: str) -> dict:
    """Resolves a target_id to its provenance: a claim id (containing '#')
    returns its full edit history and current resolution, anything else is
    looked up as a run id and returns that run's record.

    Raises WhyError if the claim has no history or the run id doesn't exist."""
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
