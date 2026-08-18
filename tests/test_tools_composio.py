import pytest
import argparse
from unittest.mock import MagicMock
from px0 import tools, connect, cli, credentials as creds_mod, builder as builder_mod

def _setup_active_composio(home):
    connect.setup_composio(home, "test_api_key")
    creds = creds_mod.load(home)
    creds["composio"]["connected_accounts"] = {
        "calendar": "ca_testaccount",
        "gmail": "ca_testaccount",
        "slack": "ca_testaccount",
    }
    creds_mod.save(home, creds)


# --- Unit Tests ---

def test_calendar_list_events_happy_path(tmp_home, fake_composio):
    _setup_active_composio(tmp_home)
    fake_composio.execute_response = {"events": [{"summary": "Test Meeting"}]}

    res = tools.call(tmp_home, {}, "calendar.list_events", {"window": "yesterday"})
    assert res == {"events": [{"summary": "Test Meeting"}]}
    assert fake_composio.last_execute_slug == "GOOGLECALENDAR_EVENTS_LIST"
    assert "timeMin" in fake_composio.last_execute_args


def test_gmail_search_messages_happy_path(tmp_home, fake_composio):
    _setup_active_composio(tmp_home)
    fake_composio.execute_response = {"messages": [{"id": "123"}]}

    res = tools.call(tmp_home, {}, "gmail.search_messages", {"query": "is:unread"})
    assert res == {"messages": [{"id": "123"}]}
    assert fake_composio.last_execute_slug == "GMAIL_FETCH_EMAILS"
    assert fake_composio.last_execute_args == {"query": "is:unread"}


def test_gmail_get_message_happy_path(tmp_home, fake_composio):
    _setup_active_composio(tmp_home)
    fake_composio.execute_response = {"subject": "Hi"}

    res = tools.call(tmp_home, {}, "gmail.get_message", {"id": "123"})
    assert res == {"subject": "Hi"}
    assert fake_composio.last_execute_slug == "GMAIL_GET_EMAIL"
    assert fake_composio.last_execute_args == {"id": "123", "message_id": "123"}


def test_gmail_send_message_happy_path(tmp_home, fake_composio):
    _setup_active_composio(tmp_home)
    fake_composio.execute_response = {"status": "sent"}

    res = tools.call(tmp_home, {}, "gmail.send_message", {"to": "a@b.com", "subject": "Hi", "body": "Hello"})
    assert res == {"status": "sent"}
    assert fake_composio.last_execute_slug == "GMAIL_SEND_EMAIL"
    assert fake_composio.last_execute_args == {
        "recipient_email": "a@b.com",
        "subject": "Hi",
        "body": "Hello"
    }


def test_slack_post_message_happy_path(tmp_home, fake_composio):
    _setup_active_composio(tmp_home)
    fake_composio.execute_response = {"ok": True}

    res = tools.call(tmp_home, {}, "slack.post_message", {"channel": "#dev", "text": "Hi"})
    assert res == {"ok": True}
    assert fake_composio.last_execute_slug == "SLACK_SEND_MESSAGE"
    assert fake_composio.last_execute_args == {
        "channel": "#dev",
        "message": "Hi"
    }


def test_unconfigured_raises_connector_not_configured(tmp_home, fake_composio):
    # No credentials at all
    with pytest.raises(tools.ConnectorNotConfigured):
        tools.call(tmp_home, {}, "slack.post_message", {"channel": "#dev", "text": "Hi"})


def test_unconnected_app_raises_connector_not_configured(tmp_home, fake_composio):
    # Credentials exist but app is not in connected_accounts
    connect.setup_composio(tmp_home, "test_api_key")
    with pytest.raises(tools.ConnectorNotConfigured):
        tools.call(tmp_home, {}, "slack.post_message", {"channel": "#dev", "text": "Hi"})


def test_initiated_connection_raises_connector_not_configured(tmp_home, fake_composio):
    _setup_active_composio(tmp_home)
    fake_composio.status = "INITIATED"
    with pytest.raises(tools.ConnectorNotConfigured, match="is INITIATED, not ACTIVE"):
        tools.call(tmp_home, {}, "slack.post_message", {"channel": "#dev", "text": "Hi"})


def test_failed_connection_raises_connector_not_configured(tmp_home, fake_composio):
    _setup_active_composio(tmp_home)
    fake_composio.status = "FAILED"
    with pytest.raises(tools.ConnectorNotConfigured, match="is FAILED, not ACTIVE"):
        tools.call(tmp_home, {}, "slack.post_message", {"channel": "#dev", "text": "Hi"})


def test_tool_failure_raises_connector_error(tmp_home, fake_composio):
    _setup_active_composio(tmp_home)
    fake_composio.fail_status_code = 500
    with pytest.raises(tools.ConnectorError):
        tools.call(tmp_home, {}, "slack.post_message", {"channel": "#dev", "text": "Hi"})


# --- Integration Tests ---

def test_cmd_connect_gmail_end_to_end(tmp_home, fake_composio, monkeypatch, capsys):
    # Mock CLI context to use tmp_home
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))

    connect.setup_composio(tmp_home, "test_api_key")

    args = argparse.Namespace(target="gmail")
    cli.cmd_connect(args)

    captured = capsys.readouterr()
    assert "https://backend.composio.dev/redirect-mock" in captured.out

    creds = creds_mod.load(tmp_home)
    assert creds["composio"]["connected_accounts"]["gmail"] == "ca_testaccount"


def test_cmd_new_auto_connect_missing_composio_app(tmp_home, fake_composio, monkeypatch, capsys):
    # Mock CLI context
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))

    connect.setup_composio(tmp_home, "test_api_key")

    # Generate a fake plan that requires slack.post_message
    fake_plan = builder_mod.Plan(
        trigger={"manual": True},
        inputs=[],
        tools=["slack.post_message"],
        output={"target": "stdout"},
        body="Send message",
        description="test flow",
        raw={"tools": ["slack.post_message"], "description": "test flow"}
    )
    monkeypatch.setattr(builder_mod, "generate_plan", lambda *a: fake_plan)

    args = argparse.Namespace(description="test description", yes=True, id="test-id")
    
    with pytest.raises(SystemExit) as exc_info:
        cli.cmd_new(args)

    assert exc_info.value.code == cli.EXIT_USER_ERROR
    captured = capsys.readouterr()
    assert "connections needed but not configured: ['slack']" in captured.out
    assert "To connect slack (Composio), open this URL and complete OAuth:" in captured.out
    assert "https://backend.composio.dev/redirect-mock" in captured.out
