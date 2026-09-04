"""HTTP Server for px0 web interface."""

import os
import signal
import socketserver
import subprocess
import sys
import threading
import urllib.parse
from http import HTTPStatus
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

from px0 import (
    authoring,
    daemon as daemon_mod,
    paths,
    runner,
    runs as runs_mod,
    workflow as workflow_mod,
)
from px0.web import views


class WebUIHandler(SimpleHTTPRequestHandler):
    home: Path
    config: dict

    def do_GET(self) -> None:
        parsed_url = urllib.parse.urlparse(self.path)
        path = parsed_url.path

        # Static assets
        if path.startswith("/static/"):
            asset_name = path[len("/static/"):]
            static_dir = Path(__file__).parent / "static"
            asset_path = static_dir / asset_name
            if asset_path.exists() and asset_path.is_file():
                content_type = "text/plain"
                if asset_name.endswith(".css"):
                    content_type = "text/css"
                elif asset_name.endswith(".js"):
                    content_type = "application/javascript"
                data = asset_path.read_bytes()
                self._send_response(HTTPStatus.OK, data, content_type)
                return
            else:
                self._send_response(HTTPStatus.NOT_FOUND, b"Asset not found", "text/plain")
                return

        # Daemon polling badge
        if path == "/api/daemon/badge":
            d_status = daemon_mod.status(self.home, self.config)
            html_out = views.render_daemon_badge(d_status)
            self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
            return

        # HTMX partial view updates
        if path == "/api/views/dashboard":
            html_out = views.render_dashboard(self.home, self.config)
            self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
            return
        elif path == "/api/views/workflows":
            html_out = views.render_workflows_list(self.home, self.config)
            self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
            return
        elif path == "/api/views/schedules":
            html_out = views.render_schedules_list(self.home, self.config)
            self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
            return
        elif path == "/api/views/runs":
            html_out = views.render_runs_list(self.config)
            self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
            return
        elif path == "/api/views/daemon":
            html_out = views.render_daemon_view(self.home, self.config)
            self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
            return

        # Modal endpoints
        if path.startswith("/api/workflows/") and path.endswith("/run-modal"):
            wf_id = path[len("/api/workflows/"):-len("/run-modal")]
            try:
                wf = workflow_mod.load(self.home, wf_id)
                html_out = views.render_run_modal(wf)
                self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
            except Exception as e:
                self._send_response(HTTPStatus.NOT_FOUND, str(e).encode(), "text/plain")
            return

        if path.startswith("/api/workflows/"):
            wf_id = path[len("/api/workflows/"):]
            try:
                wf = workflow_mod.load(self.home, wf_id)
                html_out = views.render_workflow_detail_modal(wf)
                self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
            except Exception as e:
                self._send_response(HTTPStatus.NOT_FOUND, str(e).encode(), "text/plain")
            return

        if path.startswith("/api/schedules/") and path.endswith("/edit"):
            wf_id = path[len("/api/schedules/"):-len("/edit")]
            try:
                wf = workflow_mod.load(self.home, wf_id)
                html_out = views.render_schedule_edit_modal(wf)
                self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
            except Exception as e:
                self._send_response(HTTPStatus.NOT_FOUND, str(e).encode(), "text/plain")
            return

        if path.startswith("/api/runs/"):
            run_id = path[len("/api/runs/"):]
            html_out = views.render_run_detail_modal(self.config, run_id)
            self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
            return

        # Full page views (browser navigation / direct URL access)
        d_status = daemon_mod.status(self.home, self.config)
        if path in ("", "/"):
            content = views.render_dashboard(self.home, self.config)
            full_html = views.page_shell(content, active_tab="dashboard", daemon_status=d_status)
            self._send_response(HTTPStatus.OK, full_html.encode(), "text/html")
            return
        elif path == "/workflows":
            content = views.render_workflows_list(self.home, self.config)
            full_html = views.page_shell(content, active_tab="workflows", daemon_status=d_status)
            self._send_response(HTTPStatus.OK, full_html.encode(), "text/html")
            return
        elif path == "/schedules":
            content = views.render_schedules_list(self.home, self.config)
            full_html = views.page_shell(content, active_tab="schedules", daemon_status=d_status)
            self._send_response(HTTPStatus.OK, full_html.encode(), "text/html")
            return
        elif path == "/runs":
            content = views.render_runs_list(self.config)
            full_html = views.page_shell(content, active_tab="runs", daemon_status=d_status)
            self._send_response(HTTPStatus.OK, full_html.encode(), "text/html")
            return
        elif path == "/daemon":
            content = views.render_daemon_view(self.home, self.config)
            full_html = views.page_shell(content, active_tab="daemon", daemon_status=d_status)
            self._send_response(HTTPStatus.OK, full_html.encode(), "text/html")
            return

        self._send_response(HTTPStatus.NOT_FOUND, b"Page not found", "text/plain")

    def do_POST(self) -> None:
        parsed_url = urllib.parse.urlparse(self.path)
        path = parsed_url.path
        query = urllib.parse.parse_qs(parsed_url.query)

        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length).decode() if content_length > 0 else ""
        form_data = urllib.parse.parse_qs(body)

        # Toggle enable/disable for a workflow
        if path.startswith("/api/workflows/") and path.endswith("/toggle"):
            wf_id = path[len("/api/workflows/"):-len("/toggle")]
            try:
                wf = workflow_mod.load(self.home, wf_id)
                new_state = not wf.enabled
                text = wf.path.read_text()
                updated_text = authoring.set_frontmatter_key(text, "enabled", new_state)
                authoring.write_file(
                    self.home,
                    wf.path,
                    updated_text,
                    evidence=f"workflow {wf.id} {'enabled' if new_state else 'disabled'} via web ui"
                )
                daemon_mod.restart_if_running(self.home, self.config)
                
                # Check if caller was on workflows view or schedules view
                referer = self.headers.get("Referer", "")
                if "schedules" in referer:
                    html_out = views.render_schedules_list(self.home, self.config)
                else:
                    wf_updated = workflow_mod.load(self.home, wf_id)
                    html_out = views.render_workflow_row(wf_updated)

                self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
            except Exception as e:
                self._send_response(HTTPStatus.INTERNAL_SERVER_ERROR, str(e).encode(), "text/plain")
            return

        # Update schedule for a workflow
        if path.startswith("/api/schedules/") and path.endswith("/update"):
            wf_id = path[len("/api/schedules/"):-len("/update")]
            new_schedule = form_data.get("schedule", [""])[0].strip()
            try:
                wf = workflow_mod.load(self.home, wf_id)
                text = wf.path.read_text()
                parts = text.split("---", 2)
                if len(parts) >= 3:
                    import re
                    front, body = parts[1], parts[2]
                    if re.search(r"^\s*schedule\s*:", front, flags=re.MULTILINE):
                        front = re.sub(r"(^\s*schedule\s*:).*$", rf'\g<1> "{new_schedule}"', front, flags=re.MULTILINE)
                    elif re.search(r"^\s*trigger\s*:", front, flags=re.MULTILINE):
                        front = re.sub(r"(^\s*trigger\s*:.*$)", rf'\g<1>\n  schedule: "{new_schedule}"', front, flags=re.MULTILINE)
                    else:
                        front = front + f'\ntrigger:\n  schedule: "{new_schedule}"\n'
                    updated_text = "---" + front + "---" + body
                else:
                    updated_text = text

                authoring.write_file(
                    self.home,
                    wf.path,
                    updated_text,
                    evidence=f"schedule updated to '{new_schedule}' for {wf.id} via web ui"
                )
                daemon_mod.restart_if_running(self.home, self.config)

                
                # Re-render schedules list and clear modal
                html_out = views.render_schedules_list(self.home, self.config)
                # We also trigger modal closure via JS or header
                self.send_response(HTTPStatus.OK)
                self.send_header("Content-Type", "text/html")
                self.send_header("HX-Trigger", "closeModalTrigger")
                html_out += "<script>closeModal();</script>"
                self.send_header("Content-Length", str(len(html_out.encode())))
                self.end_headers()
                self.wfile.write(html_out.encode())
            except Exception as e:
                self._send_response(HTTPStatus.INTERNAL_SERVER_ERROR, str(e).encode(), "text/plain")
            return

        # Trigger workflow run
        if path.startswith("/api/workflows/") and path.endswith("/trigger"):
            wf_id = path[len("/api/workflows/"):-len("/trigger")]
            try:
                dry_run = form_data.get("dry_run", ["false"])[0] == "true"
                cli_inputs = {}
                for k, v in form_data.items():
                    if k.startswith("var_") and v:
                        cli_inputs[k[len("var_"):]] = v[0]

                # Run detached in a background thread so UI stays responsive
                def _bg_run():
                    try:
                        runner.run(
                            self.home,
                            self.config,
                            wf_id,
                            trigger="manual",
                            cli_inputs=cli_inputs,
                            dry_run=dry_run
                        )
                    except Exception as ex:
                        pass

                t = threading.Thread(target=_bg_run, daemon=True)
                t.start()

                resp_html = f'''
                <div style="background: var(--success-bg); border: 1px solid rgba(113, 176, 113, 0.4); padding: 12px; border-radius: 4px; color: var(--success);">
                  Run initiated successfully in the background! Check <a href="/runs" hx-get="/api/views/runs" hx-target="#main-view" onclick="closeModal()" style="color: var(--accent); text-decoration: underline;">Runs</a> for results.
                </div>
                '''
                self._send_response(HTTPStatus.OK, resp_html.encode(), "text/html")
            except Exception as e:
                err_html = f'''
                <div style="background: var(--danger-bg); border: 1px solid rgba(224, 108, 117, 0.4); padding: 12px; border-radius: 4px; color: var(--danger);">
                  Failed to start run: {views._escape(str(e))}
                </div>
                '''
                self._send_response(HTTPStatus.OK, err_html.encode(), "text/html")
            return

        # Daemon actions: start, stop, tick
        if path == "/api/daemon/action":
            act = query.get("act", [""])[0]
            if act == "tick":
                try:
                    state = daemon_mod.load_schedule_state(self.home)
                    daemon_mod.tick(self.home, self.config, state)
                    msg = '<div style="margin-top: 10px; color: var(--success);">Schedule tick completed successfully.</div>'
                    self._send_response(HTTPStatus.OK, msg.encode(), "text/html")
                except Exception as e:
                    msg = f'<div style="margin-top: 10px; color: var(--danger);">Tick error: {views._escape(str(e))}</div>'
                    self._send_response(HTTPStatus.OK, msg.encode(), "text/html")
                return
            elif act == "start":
                try:
                    status = daemon_mod.status(self.home, self.config)
                    if not status.get("alive"):
                        px0_bin = sys.executable
                        args = [px0_bin, "-m", "px0.cli", "daemon", "serve"]
                        env = {**os.environ, "PX0_HOME": str(self.home)}
                        subprocess.Popen(
                            args,
                            env=env,
                            stdin=subprocess.DEVNULL,
                            stdout=subprocess.DEVNULL,
                            stderr=subprocess.DEVNULL,
                            start_new_session=True
                        )
                    import time; time.sleep(0.5)
                    html_out = views.render_daemon_view(self.home, self.config)
                    self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
                except Exception as e:
                    self._send_response(HTTPStatus.INTERNAL_SERVER_ERROR, str(e).encode(), "text/plain")
                return
            elif act == "stop":
                try:
                    status = daemon_mod.status(self.home, self.config)
                    if status.get("alive") and status.get("pid"):
                        os.kill(status["pid"], signal.SIGTERM)
                    import time; time.sleep(0.5)
                    html_out = views.render_daemon_view(self.home, self.config)
                    self._send_response(HTTPStatus.OK, html_out.encode(), "text/html")
                except Exception as e:
                    self._send_response(HTTPStatus.INTERNAL_SERVER_ERROR, str(e).encode(), "text/plain")
                return

        self._send_response(HTTPStatus.NOT_FOUND, b"Endpoint not found", "text/plain")

    def _send_response(self, status: HTTPStatus, body: bytes, content_type: str) -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format: str, *args: Any) -> None:
        # Suppress noisy standard request logging to keep console clean
        pass


def start_server(home: Path, config: dict, host: str = "127.0.0.1", port: int = 8080, open_browser: bool = True) -> None:
    handler = WebUIHandler
    handler.home = home
    handler.config = config

    server_address = (host, port)
    # Allow port reuse
    socketserver.TCPServer.allow_reuse_address = True
    try:
        httpd = ThreadingHTTPServer(server_address, handler)
    except OSError as e:
        # If default port is in use, try next ports
        if port == 8080:
            for next_port in range(8081, 8090):
                try:
                    server_address = (host, next_port)
                    httpd = ThreadingHTTPServer(server_address, handler)
                    port = next_port
                    break
                except OSError:
                    continue
            else:
                raise e
        else:
            raise e

    url = f"http://{host}:{port}/"
    print(f"\n🚀 px0 web dashboard running at: {url}")
    print("Press Ctrl+C to stop the server.\n")

    if open_browser:
        try:
            import webbrowser
            webbrowser.open(url)
        except Exception:
            pass

    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\nStopping web dashboard...")
    finally:
        httpd.server_close()
