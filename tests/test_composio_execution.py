"""Executing a Composio tool has to survive the two things that broke it.

Both bugs hid behind the same useless message. `px0 workflows run` reported
"Composio API error: Connection error." because `tools.py` built its own SDK
client without the stored CA bundle, so TLS verification failed behind an
intercepting proxy -- while `px0 workflows new` worked, because `catalogue.py`
and `connect.py` do apply it. With that fixed, the execute call turned out never
to send the `user_id` its connected account was created with.
"""

import ssl

import pytest

from px0 import connect as connect_mod, tools


# --- every client must come from the one factory that applies the bundle ----

def test_tools_never_constructs_an_sdk_client_of_its_own():
    """The regression that caused this: a second place that knew how to build
    a client, and forgot the bundle."""
    src = (tools.__file__.replace(".pyc", ".py"))
    text = open(src).read()
    assert "Composio(api_key=" not in text, "build clients via connect.composio_client"
    assert text.count("connect_mod.composio_client(") == 2


def test_the_factory_applies_the_ca_bundle_before_building_a_client(monkeypatch, tmp_home):
    applied = []
    monkeypatch.setattr(connect_mod, "apply_ca_bundle", lambda home: applied.append(home))
    monkeypatch.setattr(connect_mod, "_silence_sdk_logging", lambda: None)

    import composio
    monkeypatch.setattr(composio, "Composio", lambda api_key: ("client", api_key))

    client = connect_mod.composio_client(tmp_home, "cmp_key")

    assert applied == [tmp_home], "the bundle must be applied, not skipped"
    assert client == ("client", "cmp_key")


def test_a_supplied_key_skips_the_lookup(monkeypatch, tmp_home):
    """Callers that already resolved the key must not re-read config."""
    monkeypatch.setattr(connect_mod, "apply_ca_bundle", lambda home: None)
    monkeypatch.setattr(connect_mod, "_silence_sdk_logging", lambda: None)
    import composio
    monkeypatch.setattr(composio, "Composio", lambda api_key: api_key)

    assert connect_mod.composio_client(tmp_home, "explicit") == "explicit"


# --- the same user_id on both sides ----------------------------------------

def test_the_connected_account_and_the_execute_call_share_one_user_id():
    """Composio rejects an execute that omits the account's user id."""
    connect_src = open(connect_mod.__file__.replace(".pyc", ".py")).read()
    tools_src = open(tools.__file__.replace(".pyc", ".py")).read()

    assert 'user_id="px0-local"' not in connect_src, "use the constant, not a literal"
    assert "user_id=COMPOSIO_USER_ID" in connect_src
    assert "user_id=connect_mod.COMPOSIO_USER_ID" in tools_src


# --- "Connection error." must never be the whole story ---------------------

def _wrapped_cert_error():
    """An APIConnectionError-shaped exception: useless surface, real cause below."""
    root = ssl.SSLCertVerificationError(
        "[SSL: CERTIFICATE_VERIFY_FAILED] certificate verify failed: "
        "unable to get local issuer certificate"
    )
    outer = RuntimeError("Connection error.")
    outer.__cause__ = root
    return outer


def test_a_tls_failure_is_named_and_given_its_fix():
    text = connect_mod.describe_api_error(_wrapped_cert_error())

    assert "Connection error." not in text
    assert "certificate" in text.lower()
    assert "connectors.ca_bundle" in text, "the user needs the command, not a diagnosis"


def test_a_non_tls_failure_surfaces_its_root_cause():
    root = TimeoutError("timed out after 30s")
    outer = RuntimeError("Connection error.")
    outer.__cause__ = root

    text = connect_mod.describe_api_error(outer)

    assert "timed out after 30s" in text and "TimeoutError" in text


def test_an_error_that_already_explains_itself_is_not_padded():
    text = connect_mod.describe_api_error(ValueError("400: user id is required"))
    assert text == "400: user id is required"


def test_root_cause_survives_a_cycle():
    """A self-referencing chain must not spin forever."""
    a, b = RuntimeError("a"), RuntimeError("b")
    a.__cause__, b.__cause__ = b, a
    assert connect_mod.root_cause(a) in (a, b)


# --- self-healing retry ----------------------------------------------------

def test_a_cert_failure_recovers_a_bundle_and_retries_once(monkeypatch, tmp_home):
    monkeypatch.setattr(connect_mod, "recover_ca_bundle", lambda home: "/etc/ca.pem")
    calls = []

    def flaky():
        calls.append(1)
        if len(calls) == 1:
            raise _wrapped_cert_error()
        return "ok"

    assert connect_mod.with_cert_recovery(tmp_home, flaky) == "ok"
    assert len(calls) == 2


def test_a_non_cert_failure_is_not_retried(monkeypatch, tmp_home):
    """Retrying a 400 just sends the same bad request twice."""
    monkeypatch.setattr(connect_mod, "recover_ca_bundle",
                        lambda home: pytest.fail("must not hunt for a bundle"))
    calls = []

    def boom():
        calls.append(1)
        raise ValueError("400: user id is required")

    with pytest.raises(ValueError):
        connect_mod.with_cert_recovery(tmp_home, boom)
    assert len(calls) == 1


def test_a_cert_failure_with_no_bundle_to_be_found_still_raises(monkeypatch, tmp_home):
    monkeypatch.setattr(connect_mod, "recover_ca_bundle", lambda home: None)

    with pytest.raises(RuntimeError):
        connect_mod.with_cert_recovery(tmp_home, lambda: (_ for _ in ()).throw(_wrapped_cert_error()))
