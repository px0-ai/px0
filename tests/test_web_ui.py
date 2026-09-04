import os
import socketserver
import threading
import time
import urllib.request
import urllib.parse
from http.server import ThreadingHTTPServer
from pathlib import Path

import pytest

from px0 import authoring, config as config_mod, daemon as daemon_mod, workflow as workflow_mod
from px0.web import server as web_server


@pytest.fixture
def web_test_env(tmp_path):
    home = tmp_path / ".px0"
    home.mkdir(parents=True)
    (home / "workflows").mkdir(parents=True)
    (home / "state").mkdir(parents=True)
    (home / "runs").mkdir(parents=True)
    (home / "logs").mkdir(parents=True)

    config = {
        "store": {"home": str(home)},
        "runs": {"storage": "filesystem", "path": str(home / "runs")},
        "logs": {"path": str(home / "logs")},
    }
    config_mod.save(home / "config.toml", config)

    # Create a test workflow
    wf_text = """---
description: Daily test workflow
trigger:
  schedule: "0 9 * * *"
enabled: true
---
echo "Hello from test"
"""
    (home / "workflows" / "daily-test.md").write_text(wf_text)

    # Pick an available port
    with socketserver.TCPServer(("127.0.0.1", 0), None) as s:
        port = s.server_address[1]

    handler = web_server.WebUIHandler
    handler.home = home
    handler.config = config

    httpd = ThreadingHTTPServer(("127.0.0.1", port), handler)
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()

    time.sleep(0.1)

    base_url = f"http://127.0.0.1:{port}"
    yield {"home": home, "config": config, "base_url": base_url, "port": port}

    httpd.shutdown()
    httpd.server_close()


def test_static_assets(web_test_env):
    base_url = web_test_env["base_url"]
    
    # Test htmx.min.js
    with urllib.request.urlopen(f"{base_url}/static/htmx.min.js") as resp:
        assert resp.status == 200
        content = resp.read()
        assert len(content) > 1000
        assert resp.headers.get("Content-Type") == "application/javascript"

    # Test style.css
    with urllib.request.urlopen(f"{base_url}/static/style.css") as resp:
        assert resp.status == 200
        content = resp.read().decode()
        assert ":root" in content
        assert resp.headers.get("Content-Type") == "text/css"


def test_full_pages(web_test_env):
    base_url = web_test_env["base_url"]
    
    for path in ["/", "/workflows", "/schedules", "/runs", "/daemon"]:
        with urllib.request.urlopen(f"{base_url}{path}") as resp:
            assert resp.status == 200
            html = resp.read().decode()
            assert "<!DOCTYPE html>" in html
            assert "px0" in html
            assert "/static/htmx.min.js" in html


def test_api_views(web_test_env):
    base_url = web_test_env["base_url"]

    # Dashboard view
    with urllib.request.urlopen(f"{base_url}/api/views/dashboard") as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "Total Workflows" in html
        assert "daily-test" in html or "Active Schedules" in html

    # Workflows view
    with urllib.request.urlopen(f"{base_url}/api/views/workflows") as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "daily-test" in html
        assert "ENABLED" in html

    # Schedules view
    with urllib.request.urlopen(f"{base_url}/api/views/schedules") as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "0 9 * * *" in html
        assert "daily-test" in html

    # Daemon badge
    with urllib.request.urlopen(f"{base_url}/api/daemon/badge") as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "daemon:" in html


def test_workflow_modals(web_test_env):
    base_url = web_test_env["base_url"]

    # Workflow detail modal
    with urllib.request.urlopen(f"{base_url}/api/workflows/daily-test") as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "Daily test workflow" in html
        assert "modal-content" in html

    # Workflow run modal
    with urllib.request.urlopen(f"{base_url}/api/workflows/daily-test/run-modal") as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "Run Workflow" in html
        assert "Dry run" in html

    # Schedule edit modal
    with urllib.request.urlopen(f"{base_url}/api/schedules/daily-test/edit") as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "0 9 * * *" in html
        assert "Edit Schedule" in html


def test_workflow_toggle(web_test_env):
    base_url = web_test_env["base_url"]
    home = web_test_env["home"]

    # Toggle off
    req = urllib.request.Request(f"{base_url}/api/workflows/daily-test/toggle", method="POST", data=b"")
    with urllib.request.urlopen(req) as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "DISABLED" in html

    wf = workflow_mod.load(home, "daily-test")
    assert wf.enabled is False

    # Toggle on
    req = urllib.request.Request(f"{base_url}/api/workflows/daily-test/toggle", method="POST", data=b"")
    with urllib.request.urlopen(req) as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "ENABLED" in html

    wf = workflow_mod.load(home, "daily-test")
    assert wf.enabled is True


def test_schedule_update(web_test_env):
    base_url = web_test_env["base_url"]
    home = web_test_env["home"]

    data = urllib.parse.urlencode({"schedule": "*/15 * * * *"}).encode()
    req = urllib.request.Request(f"{base_url}/api/schedules/daily-test/update", method="POST", data=data)
    with urllib.request.urlopen(req) as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "*/15 * * * *" in html

    wf = workflow_mod.load(home, "daily-test")
    assert wf.trigger.get("schedule") == "*/15 * * * *"


def test_daemon_tick(web_test_env):
    base_url = web_test_env["base_url"]
    req = urllib.request.Request(f"{base_url}/api/daemon/action?act=tick", method="POST", data=b"")
    with urllib.request.urlopen(req) as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "Schedule tick completed" in html

def test_workflow_run_trigger(web_test_env):
    base_url = web_test_env["base_url"]
    data = urllib.parse.urlencode({"dry_run": "true"}).encode()
    req = urllib.request.Request(f"{base_url}/api/workflows/daily-test/trigger", method="POST", data=data)
    with urllib.request.urlopen(req) as resp:
        assert resp.status == 200
        html = resp.read().decode()
        assert "Run initiated successfully" in html
