"""px0: a local-first CLI where everything the system does is a workflow.

Everything px0 knows lives in two folders inside the store: guidelines/
for how the user works, knowledge/ for what the user has read and kept.
"""

__version__ = "0.1.0"
SCHEMA_VERSION = 1  # on-disk store schema version; bumped when the store layout changes in a way old stores can't read
