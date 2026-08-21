"""px0: a local-first CLI where everything the system does is a workflow.

Everything px0 knows lives in two folders inside the store: guidelines/
for how the user works, brain/ for what the user has read and kept.
"""

from pathlib import Path

_version_file = Path(__file__).parent.parent / "VERSION"
if _version_file.exists() and _version_file.read_text().strip():
    __version__ = _version_file.read_text().strip()
else:
    try:
        from importlib.metadata import version as _dist_version
        __version__ = _dist_version("px0")
    except Exception as _err:
        raise RuntimeError("px0 version could not be determined. VERSION file missing and package not installed.") from _err

SCHEMA_VERSION = 2  # on-disk store schema version; bumped when the store layout changes in a way old stores can't read
