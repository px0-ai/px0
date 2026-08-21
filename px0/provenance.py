"""px0 runs why: walk the chain behind the result a run produced."""

from px0 import runs as runs_mod


class WhyError(Exception):
    """Raised when why() cannot resolve the given target id to a run."""
    pass


def why(config: dict, target_id: str) -> dict:
    """Resolves a run id to its record: the workflow version the run used, the
    guidelines it inlined, the passages it retrieved, and the calls it made.

    Raises WhyError if the id is not shaped like a run id, or names a run this
    store has no record of -- a record that aged out under retention reads the
    same as one that never existed, so the message says so.
    """
    try:
        record = runs_mod.read_record(config, target_id)
    except (FileNotFoundError, runs_mod.RunIdError) as e:
        raise WhyError(str(e))
    return {"kind": "run", "record": record}
