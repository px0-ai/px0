"""`consolidate` / `px0 guidelines review`: the one capped review session
over everything pending. Presents new proposals ranked by repetition,
claims due for decay, contradiction pairs, and guideline files no
workflow references. The session closes as one change."""

from collections import Counter
from pathlib import Path

from px0 import config as config_mod
from px0 import proposals as proposals_mod


def build_session(home: Path, config: dict, decay_days: int = 180) -> dict:
    props = proposals_mod.list_proposals(home)
    counts = Counter(p.target_file for p in props)
    ranked = sorted(props, key=lambda p: -counts[p.target_file])

    max_n = config_mod.get(config, "proposals.max_per_consolidation", 10)
    return {
        "proposals": ranked[:max_n],
        "proposals_overflow": max(0, len(ranked) - max_n),
        "decayed_claims": proposals_mod.decayed_claims(home, decay_days),
        "contradictions": proposals_mod.find_contradictions(config, home),
        "unreferenced_files": proposals_mod.unreferenced_guideline_files(home),
    }
