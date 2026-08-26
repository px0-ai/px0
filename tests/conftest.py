import pytest
import json
import requests
import httpx
from px0 import store

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


@pytest.fixture(autouse=True)
def _clean_composio_env(monkeypatch):
    monkeypatch.delenv("COMPOSIO_API_KEY", raising=False)


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


# --- brain document builders ------------------------------------------------
#
# Real files, built in-process, so the ingest tests exercise the actual
# extraction code paths without needing poppler, pandoc, or a network fetch.

def build_minimal_pdf(text: str) -> bytes:
    """A genuinely valid single-page PDF whose only content is `text`.

    Hand-rolled rather than generated by a library so the PDF tests have no
    external dependency; pypdf and pdftotext both extract from it.
    """
    content = f"BT /F1 12 Tf 72 720 Td ({text}) Tj ET".encode()
    objs = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
        b"/Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
        b"<< /Length " + str(len(content)).encode() + b" >>\nstream\n"
        + content + b"\nendstream",
    ]
    out = bytearray(b"%PDF-1.4\n")
    offsets = []
    for i, body in enumerate(objs, start=1):
        offsets.append(len(out))
        out += f"{i} 0 obj\n".encode() + body + b"\nendobj\n"
    xref_at = len(out)
    out += f"xref\n0 {len(objs) + 1}\n".encode() + b"0000000000 65535 f \n"
    for off in offsets:
        out += f"{off:010d} 00000 n \n".encode()
    out += (f"trailer\n<< /Size {len(objs) + 1} /Root 1 0 R >>\nstartxref\n"
            f"{xref_at}\n%%EOF\n").encode()
    return bytes(out)


def build_docx(paragraphs: list[str]) -> bytes:
    """A structurally complete .docx holding `paragraphs`."""
    import zipfile
    from io import BytesIO

    w = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
    body = "".join(
        f'<w:p><w:r><w:t xml:space="preserve">{p}</w:t></w:r></w:p>' for p in paragraphs
    )
    document = (
        f'<?xml version="1.0" encoding="UTF-8"?>'
        f'<w:document xmlns:w="{w}"><w:body>{body}</w:body></w:document>'
    )
    content_types = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
        '<Default Extension="xml" ContentType="application/xml"/>'
        '<Override PartName="/word/document.xml" ContentType="application/vnd'
        '.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>'
    )
    rels = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
        '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument'
        '/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>'
    )
    buf = BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.writestr("[Content_Types].xml", content_types)
        zf.writestr("_rels/.rels", rels)
        zf.writestr("word/document.xml", document)
    return buf.getvalue()


def build_odt(paragraphs: list[str]) -> bytes:
    """A structurally complete .odt holding `paragraphs`."""
    import zipfile
    from io import BytesIO

    text_ns = "urn:oasis:names:tc:opendocument:xmlns:text:1.0"
    office_ns = "urn:oasis:names:tc:opendocument:xmlns:office:1.0"
    body = "".join(f"<text:p>{p}</text:p>" for p in paragraphs)
    content = (
        f'<?xml version="1.0" encoding="UTF-8"?>'
        f'<office:document-content xmlns:office="{office_ns}" xmlns:text="{text_ns}">'
        f"<office:body><office:text>{body}</office:text></office:body>"
        f"</office:document-content>"
    )
    buf = BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.writestr("mimetype", "application/vnd.oasis.opendocument.text")
        zf.writestr("content.xml", content)
    return buf.getvalue()


@pytest.fixture
def brain_config(tmp_home):
    """The config for `tmp_home`, loaded from its own config.toml."""
    from px0 import config as config_mod, paths as paths_mod
    return config_mod.load(paths_mod.config_path(tmp_home))


@pytest.fixture
def no_external_tools(monkeypatch):
    """Hides pdftotext, pandoc, and yt-dlp so the built-in paths are exercised.

    Without this the tests would pass or fail depending on what happens to be
    installed on the machine running them.
    """
    import shutil as shutil_mod
    real_which = shutil_mod.which
    monkeypatch.setattr(
        "px0.brain.shutil.which",
        lambda name, *a, **k: None if name in ("pdftotext", "pandoc", "yt-dlp") else real_which(name, *a, **k),
    )


@pytest.fixture
def quiet_spinner(monkeypatch):
    """Silences ui.spinner so CLI-level assertions see only real output."""
    import contextlib
    from px0 import ui

    @contextlib.contextmanager
    def _quiet(*a, **k):
        class _S:
            def stop(self, *a, **k): pass
        yield _S()

    monkeypatch.setattr(ui, "spinner", _quiet)


@pytest.fixture(autouse=True)
def _no_real_harness_calls(monkeypatch, request):
    """Stops a test from shelling out to the real coding-agent binary.

    `harness.invoke` runs `claude -p` as a subprocess. Anything that reaches it
    unmocked turns a unit test into a live model call -- slow, non-deterministic,
    and dependent on whoever's machine is running the suite. A test that wants
    harness behaviour mocks it explicitly; this only catches the ones that did
    not mean to call it at all.

    Opt out with @pytest.mark.allow_harness for a test that deliberately
    exercises the harness plumbing itself.
    """
    if request.node.get_closest_marker("allow_harness"):
        return

    from px0 import harness

    def _refuse(config, prompt, timeout=120):
        raise harness.HarnessError(
            "test called harness.invoke without mocking it -- mock it, or mark "
            "the test with @pytest.mark.allow_harness"
        )

    def _refuse_detailed(config, prompt, timeout=120):
        # Runs go through `invoke_detailed`, not `invoke`. Guarding only the
        # latter left the runner free to shell out to the real binary.
        raise harness.HarnessError(
            "test called harness.invoke_detailed without mocking it -- mock it, "
            "or mark the test with @pytest.mark.allow_harness"
        )

    monkeypatch.setattr(harness, "invoke", _refuse)
    monkeypatch.setattr(harness, "invoke_detailed", _refuse_detailed)
