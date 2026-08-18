import pytest
import time
import argparse
import os
import signal
from pathlib import Path
from unittest.mock import MagicMock
from px0 import daemon as daemon_mod, runs as runs_mod, cli, paths, config as config_mod

def test_log_event_writes_to_daemon_log(tmp_home):
    config = {"logs": {"path": str(tmp_home / "logs")}}
    daemon_mod._log_event(config, "test event message")

    log_file = tmp_home / "logs" / "daemon.log"
    assert log_file.exists()
    content = log_file.read_text()
    assert "test event message" in content
    assert "T" in content # ISO timestamp


def test_tail_lines(tmp_path):
    import threading
    log_file = tmp_path / "test.log"
    log_file.write_text("line 1\nline 2\n")

    # Start tailing
    tail_gen = runs_mod.tail_lines(log_file, poll_interval=0.05)

    def append_later():
        time.sleep(0.1)
        with open(log_file, "a") as f:
            f.write("line 3\n")
            f.flush()

    t = threading.Thread(target=append_later)
    t.start()

    line = next(tail_gen)
    assert line == "line 3\n"
    t.join()


def test_run_nightly_with_queue(tmp_home, monkeypatch):
    config = config_mod.load(paths.config_path(tmp_home))
    monkeypatch.setattr(daemon_mod, "_log_event", MagicMock())
    
    # Mock knowledge_mod.process_ingest_queue
    mock_processed = {"jobs_processed": 5, "videos_ingested": 10, "jobs_given_up": 0}
    monkeypatch.setattr("px0.knowledge.process_ingest_queue", lambda *a: mock_processed)

    report = daemon_mod.run_nightly(tmp_home, config)
    assert report["ingest_queue"] == mock_processed


def test_cmd_daemon_logs(tmp_home, monkeypatch, capsys):
    # Mock CLI context
    config = {"logs": {"path": str(tmp_home / "logs")}}
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, config))

    # Create fake log file
    log_dir = tmp_home / "logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    log_file = log_dir / "daemon.log"
    log_file.write_text("2026-08-18T12:00:00+00:00 start: serve started\n")

    args = argparse.Namespace(daemon_cmd="logs", follow=False)
    cli.cmd_daemon(args)

    captured = capsys.readouterr()
    assert "start: serve started" in captured.out


def test_cmd_runs_logs_follow_stops_at_terminal_outcome(tmp_home, monkeypatch, capsys):
    # Mock CLI context
    config = {"logs": {"path": str(tmp_home / "logs")}}
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, config))

    # Pre-create run directory
    run_id = "run_20260818-120000-abcd"
    log_dir = tmp_home / "logs" / "runs" / "2026-08-18"
    log_dir.mkdir(parents=True, exist_ok=True)
    log_file = log_dir / f"{run_id}.log"
    log_file.write_text("Line 1 from run\nLine 2 from run\n")

    # Mock read_record to return success immediately on follow loop
    record_dir = tmp_home / "logs" / "records" / "2026-08-18"
    record_dir.mkdir(parents=True, exist_ok=True)
    record_file = record_dir / f"{run_id}.json"
    record_file.write_text('{"id": "run_20260818-120000-abcd", "outcome": "success"}')

    # Execute cmd_runs logs run_20260818-120000-abcd --follow
    # Since outcome is success, it should read lines, see success, and exit immediately without blocking
    args = argparse.Namespace(runs_cmd="logs", run_id=run_id, follow=True)
    cli.cmd_runs(args)

    captured = capsys.readouterr()
    assert "Line 1 from run" in captured.out
    assert "Line 2 from run" in captured.out
