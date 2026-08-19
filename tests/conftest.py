import pytest
from pathlib import Path
import json
import requests
import httpx
from px0 import store, config as config_mod

class MockResponse:
    def __init__(self, status_code, json_data, text_data=""):
        self.status_code = status_code
        self.json_data = json_data
        self.text = text_data or json.dumps(json_data)

    def json(self):
        return self.json_data


class FakeComposio:
    def __init__(self):
        self.api_key = "cmp_testkey"
        self.auth_config_id = "ac_testconfig"
        self.connected_account_id = "ca_testaccount"
        self.status = "ACTIVE"
        self.last_execute_slug = None
        self.last_execute_args = None
        self.execute_response = {"success": True}
        self.fail_status_code = None

    def handle_request(self, method, url, **kwargs):
        if self.fail_status_code:
            return MockResponse(self.fail_status_code, {}, "Mock Failure")

        # 0. Toolkits (healthcheck)
        if "/toolkits" in url:
            return MockResponse(200, {
                "name": "github",
                "slug": "github",
                "description": "GitHub integration"
            })

        # 1. Auth configs
        if "/auth_configs" in url:
            payload = kwargs.get("json", {})
            toolkit_slug = payload.get("toolkit", {}).get("slug", "unknown")
            return MockResponse(201, {
                "toolkit": {"slug": toolkit_slug},
                "auth_config": {"id": self.auth_config_id, "auth_scheme": "OAUTH2", "is_composio_managed": True}
            })

        # 2. Connected accounts link
        if "/connected_accounts/link" in url:
            return MockResponse(201, {
                "link_token": "mock_token",
                "redirectUrl": "https://backend.composio.dev/redirect-mock",
                "redirect_url": "https://backend.composio.dev/redirect-mock",
                "expires_at": "2026-08-18",
                "connectedAccountId": self.connected_account_id,
                "connected_account_id": self.connected_account_id
            })


        # 3. Connected accounts status query or list
        if "/connected_accounts" in url and method == "GET":
            if "?" in url or not url.endswith(self.connected_account_id):
                return MockResponse(200, {"items": [], "total_pages": 1, "page_info": None})
            return MockResponse(200, {"status": self.status})
        
        # 3.5 Connected accounts link POST
        if "/connected_accounts" in url and method == "POST":
            return MockResponse(201, {
                "link_token": "mock_token",
                "redirectUrl": "https://backend.composio.dev/redirect-mock",
                "redirect_url": "https://backend.composio.dev/redirect-mock",
                "expires_at": "2026-08-18",
                "connectedAccountId": self.connected_account_id,
                "connected_account_id": self.connected_account_id
            })


        # 4. Execute tool
        if "/tools" in url:
            self.last_execute_slug = url.split("/tools/execute/")[-1]
            payload = kwargs.get("json", {})
            self.last_execute_args = payload.get("arguments", {})
            return MockResponse(200, {'successful': True, 'data': self.execute_response})

        # 5. Fallback
        return MockResponse(404, {"error": "Not Found"})


@pytest.fixture
def tmp_home(tmp_path):
    """Creates a temporary initialized store and returns its Path."""
    home_path = tmp_path / "px0_home"
    home_path.mkdir()
    store.init(home_path)
    return home_path


@pytest.fixture
def fake_composio(monkeypatch):
    """Mocks requests.request, requests.get, and requests.post to redirect to FakeComposio."""
    fake = FakeComposio()

    def mock_request(method, url, **kwargs):
        if "backend.composio.dev" in url:
            return fake.handle_request(method, url, **kwargs)
        raise RuntimeError(f"Unexpected external request: {method} {url}")

    def mock_get(*args, **kwargs):
        # Check if first arg is session
        url = args[1] if len(args) > 1 and isinstance(args[0], requests.Session) else args[0]
        return mock_request("GET", url, **kwargs)

    def mock_post(*args, **kwargs):
        url = args[1] if len(args) > 1 and isinstance(args[0], requests.Session) else args[0]
        return mock_request("POST", url, **kwargs)

    def mock_request_method(*args, **kwargs):
        if len(args) > 2 and isinstance(args[0], requests.Session):
            method, url = args[1], args[2]
        else:
            method, url = args[0], args[1]
        return mock_request(method, url, **kwargs)

    monkeypatch.setattr(requests, "request", mock_request_method)
    monkeypatch.setattr(requests, "get", mock_get)
    monkeypatch.setattr(requests, "post", mock_post)
    monkeypatch.setattr(requests.Session, "request", mock_request_method)
    monkeypatch.setattr(requests.Session, "get", mock_get)
    monkeypatch.setattr(requests.Session, "post", mock_post)

    def mock_httpx_send(self, request, *args, **kwargs):
        url = str(request.url)
        if "backend.composio.dev" in url or "api.github.com" in url:
            # Parse json body if present
            try:
                body = json.loads(request.read().decode("utf-8")) if request.content else {}
            except Exception:
                body = {}
            mock_resp = fake.handle_request(request.method, url, json=body)
            # Create httpx.Response
            content = mock_resp.text.encode("utf-8")
            return httpx.Response(mock_resp.status_code, content=content, request=request)
        raise RuntimeError(f"Unexpected external request: {request.method} {url}")

    monkeypatch.setattr(httpx.Client, "send", mock_httpx_send)


    return fake
