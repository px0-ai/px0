"""The guideline file format: `name`/`description` frontmatter over the rules.

The frontmatter is px0's index. A build matches a new workflow against the
descriptions and never against the bodies, so what the format has to guarantee
is that the description survives a round trip, that the body is what a run
inlines, and that a file written before the format existed still reads back.
"""

from px0 import doctor, guidelines as guidelines_mod, paths


def _write(home, rel, text):
    dest = paths.guidelines_dir(home) / rel
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(text)
    return dest


# --- round trip -------------------------------------------------------------

def test_a_rendered_guideline_parses_back_to_what_went_in(tmp_home):
    text = guidelines_mod.render(
        "commit-messages",
        "How to word a commit message. Use when the workflow writes one.",
        "## Imperative mood summary line\n\nAdd, not Added.\n")

    g = guidelines_mod.parse(_write(tmp_home, "commit-messages.md", text), "commit-messages.md")

    assert g.name == "commit-messages"
    assert g.description == "How to word a commit message. Use when the workflow writes one."
    assert g.body == "## Imperative mood summary line\n\nAdd, not Added.\n"
    assert g.summary == g.description
    assert g.described


def test_a_long_description_is_not_folded_into_something_unparseable(tmp_home):
    """yaml wraps long scalars by default, which a naive reader then truncates."""
    long = ("How to summarize a long document without losing its numbers, its "
            "caveats, or the one sentence that says why any of it matters. Use "
            "when the workflow produces a digest for someone who will not read "
            "the source.")
    text = guidelines_mod.render("summarization", long, "## Lead with the takeaway\n\nb\n")

    g = guidelines_mod.parse(_write(tmp_home, "summarization.md", text), "summarization.md")

    assert g.description == long


def test_the_name_comes_from_the_filename_not_the_folder(tmp_home):
    assert guidelines_mod.name_for("code-review/go.md") == "go"
    assert guidelines_mod.name_for("voice.md") == "voice"


# --- files that predate the format -----------------------------------------

def test_a_file_without_frontmatter_is_read_as_all_body(tmp_home):
    dest = _write(tmp_home, "voice.md", "## Say it plainly\n\nShort sentences.\n")

    g = guidelines_mod.parse(dest, "voice.md")

    assert g.name == "voice", "the filename stands in for the missing name"
    assert g.description == ""
    assert g.body.startswith("## Say it plainly"), "no content is lost"
    assert g.summary == "Say it plainly", "the first rule stands in for the description"
    assert not g.described


def test_broken_frontmatter_degrades_to_no_frontmatter(tmp_home):
    """One stray colon must not take `px0 guidelines list` down."""
    dest = _write(tmp_home, "voice.md", "---\nname: voice: bad\n---\n\n## H\n\nb\n")

    g = guidelines_mod.parse(dest, "voice.md")

    assert g.name == "voice" and not g.described
    assert "## H" in g.body


def test_frontmatter_that_is_not_a_mapping_is_ignored(tmp_home):
    dest = _write(tmp_home, "voice.md", "---\n- just a list\n---\n\n## H\n\nb\n")

    assert not guidelines_mod.parse(dest, "voice.md").described


# --- what the store hands to a build ----------------------------------------

def test_load_all_keys_by_store_relative_path(tmp_home):
    _write(tmp_home, "voice.md", guidelines_mod.render("voice", "d", "## H\n\nb\n"))
    _write(tmp_home, "code-review/go.md", guidelines_mod.render("go", "d", "## H\n\nb\n"))

    assert set(guidelines_mod.load_all(tmp_home)) == {"voice.md", "code-review/go.md"}


def test_work_guidelines_are_listed_but_never_attachable(tmp_home):
    """`work/` is the never-offered folder, as it is under `brain/`."""
    _write(tmp_home, "work/voice.md", guidelines_mod.render("voice", "d", "## H\n\nb\n"))
    _write(tmp_home, "voice.md", guidelines_mod.render("voice", "d", "## H\n\nb\n"))

    assert set(guidelines_mod.load_all(tmp_home)) == {"work/voice.md", "voice.md"}
    assert [g.rel for g in guidelines_mod.attachable(tmp_home)] == ["voice.md"]
    assert guidelines_mod.load_all(tmp_home)["work/voice.md"].is_work


def test_an_empty_store_loads_nothing_rather_than_failing(tmp_home):
    assert guidelines_mod.load_all(tmp_home) == {}
    assert guidelines_mod.attachable(tmp_home) == []


# --- what a run inlines -----------------------------------------------------

def test_a_run_inlines_the_body_and_not_the_frontmatter(tmp_home):
    _write(tmp_home, "voice.md", guidelines_mod.render(
        "voice", "How I write prose", "## Say it plainly\n\nShort sentences.\n"))

    body = guidelines_mod.body_of(tmp_home, "voice.md")

    assert body == "## Say it plainly\n\nShort sentences.\n"
    assert "How I write prose" not in body, "the index is not the content"


# --- doctor -----------------------------------------------------------------

def test_doctor_names_the_files_a_build_cannot_match_on(tmp_home):
    _write(tmp_home, "old.md", "## Say it plainly\n\nb\n")
    _write(tmp_home, "new.md", guidelines_mod.render("new", "d", "## H\n\nb\n"))

    check = doctor._check_guideline_descriptions(tmp_home)

    assert check["ok"] is True, "a file that still works is not a broken store"
    assert check["files"] == ["old.md"]
    assert "px0 guidelines edit" in check["fix"]


def test_doctor_says_nothing_to_fix_when_every_guideline_is_described(tmp_home):
    _write(tmp_home, "new.md", guidelines_mod.render("new", "d", "## H\n\nb\n"))

    check = doctor._check_guideline_descriptions(tmp_home)

    assert check["files"] == [] and "fix" not in check
