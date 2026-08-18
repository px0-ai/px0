import pytest
import json
from pathlib import Path
from px0 import knowledge, paths, config as config_mod

def test_process_ingest_queue_happy_path(tmp_home, monkeypatch):
    config = config_mod.load(paths.config_path(tmp_home))

    # Create a job file in ingest_dir
    job_dir = paths.ingest_dir(tmp_home)
    job_dir.mkdir(parents=True, exist_ok=True)
    job_file = job_dir / "test-playlist.json"
    job_file.write_text(json.dumps({
        "source": "https://youtube.com/playlist?list=PL123",
        "kind": "youtube-playlist",
        "to": "blogs",
        "queued_at": "2026-08-18"
    }))

    # Mock enumerate_playlist and add
    videos = ["https://youtube.com/watch?v=abc", "https://youtube.com/watch?v=def"]
    monkeypatch.setattr(knowledge, "enumerate_playlist", lambda *a: videos)

    added = []
    def mock_add(home, cfg, source, to=None):
        added.append((source, to))
        dest = knowledge._dest_path(home, cfg, to or "docs", source)
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text("canned content")
    monkeypatch.setattr(knowledge, "add", mock_add)

    res = knowledge.process_ingest_queue(tmp_home, config)
    assert res["jobs_processed"] == 1
    assert res["videos_ingested"] == 2
    assert res["jobs_given_up"] == 0

    assert added == [
        ("https://youtube.com/watch?v=abc", "blogs"),
        ("https://youtube.com/watch?v=def", "blogs")
    ]

    # Job file should be unlinked/deleted on success
    assert not job_file.exists()


def test_process_ingest_queue_skips_already_ingested(tmp_home, monkeypatch):
    config = config_mod.load(paths.config_path(tmp_home))

    job_dir = paths.ingest_dir(tmp_home)
    job_dir.mkdir(parents=True, exist_ok=True)
    job_file = job_dir / "test-playlist.json"
    job_file.write_text(json.dumps({
        "source": "https://youtube.com/playlist?list=PL123",
        "kind": "youtube-playlist",
        "to": "blogs",
        "queued_at": "2026-08-18"
    }))

    videos = ["https://youtube.com/watch?v=abc", "https://youtube.com/watch?v=def"]
    monkeypatch.setattr(knowledge, "enumerate_playlist", lambda *a: videos)

    # Pre-create the first video's dest path
    dest1 = knowledge._dest_path(tmp_home, config, "blogs", "https://youtube.com/watch?v=abc")
    dest1.parent.mkdir(parents=True, exist_ok=True)
    dest1.write_text("existing")

    added = []
    def mock_add(home, cfg, source, to=None):
        added.append((source, to))
        dest = knowledge._dest_path(home, cfg, to or "docs", source)
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text("canned")
    monkeypatch.setattr(knowledge, "add", mock_add)

    res = knowledge.process_ingest_queue(tmp_home, config)
    assert res["jobs_processed"] == 1
    assert res["videos_ingested"] == 1 # Only 1 actually ingested because other skipped
    assert added == [("https://youtube.com/watch?v=def", "blogs")]


def test_process_ingest_queue_partial_failure_increments_attempts(tmp_home, monkeypatch):
    config = config_mod.load(paths.config_path(tmp_home))

    job_dir = paths.ingest_dir(tmp_home)
    job_dir.mkdir(parents=True, exist_ok=True)
    job_file = job_dir / "test-playlist.json"
    job_file.write_text(json.dumps({
        "source": "https://youtube.com/playlist?list=PL123",
        "kind": "youtube-playlist",
        "to": "blogs",
        "queued_at": "2026-08-18",
        "attempts": 0
    }))

    videos = ["https://youtube.com/watch?v=abc", "https://youtube.com/watch?v=def"]
    monkeypatch.setattr(knowledge, "enumerate_playlist", lambda *a: videos)

    def mock_add(home, cfg, source, to=None):
        if "abc" in source:
            raise knowledge.IngestError("Stub or transcript missing")
        dest = knowledge._dest_path(home, cfg, to or "docs", source)
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text("canned")
    monkeypatch.setattr(knowledge, "add", mock_add)

    res = knowledge.process_ingest_queue(tmp_home, config)
    assert res["jobs_processed"] == 1
    assert res["videos_ingested"] == 1
    assert res["jobs_given_up"] == 0

    # Job file should STILL exist but updated attempts and last_error
    assert job_file.exists()
    job_data = json.loads(job_file.read_text())
    assert job_data["attempts"] == 1
    assert "Stub or transcript missing" in job_data["last_error"]


def test_process_ingest_queue_max_attempts_moves_to_failed(tmp_home, monkeypatch):
    config = config_mod.load(paths.config_path(tmp_home))

    job_dir = paths.ingest_dir(tmp_home)
    job_dir.mkdir(parents=True, exist_ok=True)
    job_file = job_dir / "test-playlist.json"
    job_file.write_text(json.dumps({
        "source": "https://youtube.com/playlist?list=PL123",
        "kind": "youtube-playlist",
        "to": "blogs",
        "queued_at": "2026-08-18",
        "attempts": 2
    }))

    monkeypatch.setattr(knowledge, "enumerate_playlist", lambda *a: ["https://youtube.com/watch?v=abc"])
    def mock_add(home, cfg, source, to=None):
        raise knowledge.IngestError("Permanent fail")
    monkeypatch.setattr(knowledge, "add", mock_add)

    res = knowledge.process_ingest_queue(tmp_home, config)
    assert res["jobs_processed"] == 1
    assert res["videos_ingested"] == 0
    assert res["jobs_given_up"] == 1

    # Original job file is deleted
    assert not job_file.exists()

    # Job file is moved to failed/
    failed_file = paths.ingest_failed_dir(tmp_home) / "test-playlist.json"
    assert failed_file.exists()
    job_data = json.loads(failed_file.read_text())
    assert job_data["attempts"] == 3
    assert "Permanent fail" in job_data["last_error"]
