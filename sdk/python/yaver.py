"""
Yaver Python SDK — embed Yaver's P2P connectivity into your Python applications.

Supports two backends:
  1. Native (ctypes) — loads the C shared library built from Go SDK
  2. HTTP — direct HTTP calls to a Yaver agent (no shared library needed)

Quick start (HTTP mode — no build step):

    from yaver import YaverClient

    client = YaverClient("http://localhost:18080", "your-auth-token")
    task = client.create_task("Fix the login bug")
    for line in client.stream_output(task["id"]):
        print(line, end="")

Quick start (native mode — requires libyaver.so):

    from yaver import YaverNativeClient

    client = YaverNativeClient("http://localhost:18080", "your-auth-token")
    task = client.create_task("Fix the login bug")
"""

import json
import time
from typing import Optional, Iterator, Any
from urllib.request import Request, urlopen
from urllib.error import HTTPError


class YaverClient:
    """HTTP-based Yaver client. No shared library needed."""

    def __init__(self, base_url: str, auth_token: str, timeout: float = 30.0):
        self.base_url = base_url.rstrip("/")
        self.auth_token = auth_token
        self.timeout = timeout

    def _request(self, method: str, path: str, body: Any = None) -> dict:
        url = f"{self.base_url}{path}"
        data = json.dumps(body).encode() if body else None
        req = Request(url, data=data, method=method)
        req.add_header("Authorization", f"Bearer {self.auth_token}")
        if data:
            req.add_header("Content-Type", "application/json")
        try:
            with urlopen(req, timeout=self.timeout) as resp:
                return json.loads(resp.read())
        except HTTPError as e:
            error_body = e.read().decode() if e.fp else str(e)
            raise RuntimeError(f"HTTP {e.code}: {error_body}")

    def health(self) -> dict:
        """Check if the agent is reachable."""
        return self._request("GET", "/health")

    def ping(self) -> float:
        """Measure round-trip time in milliseconds."""
        start = time.monotonic()
        self.health()
        return (time.monotonic() - start) * 1000

    def info(self) -> dict:
        """Get agent status information."""
        return self._request("GET", "/info")

    def create_task(
        self,
        prompt: str,
        model: Optional[str] = None,
        runner: Optional[str] = None,
        custom_command: Optional[str] = None,
        verbosity: Optional[int] = None,
        images: Optional[list] = None,
    ) -> dict:
        """Create a new task on the remote agent.

        Args:
            images: List of dicts with keys: base64, mimeType, filename
        """
        body: dict = {"title": prompt}
        if model:
            body["model"] = model
        if runner:
            body["runner"] = runner
        if custom_command:
            body["customCommand"] = custom_command
        if verbosity is not None:
            body["speechContext"] = {"verbosity": verbosity}
        if images:
            body["images"] = images
        result = self._request("POST", "/tasks", body)
        if not result.get("ok"):
            raise RuntimeError(result.get("error", "Unknown error"))
        return {
            "id": result["taskId"],
            "status": result["status"],
            "runner_id": result.get("runnerId", ""),
        }

    def get_task(self, task_id: str) -> dict:
        """Get task details by ID."""
        result = self._request("GET", f"/tasks/{task_id}")
        return result.get("task", result)

    def list_tasks(self) -> list:
        """List all tasks."""
        result = self._request("GET", "/tasks")
        return result.get("tasks", [])

    def stop_task(self, task_id: str) -> None:
        """Stop a running task."""
        result = self._request("POST", f"/tasks/{task_id}/stop")
        if not result.get("ok"):
            raise RuntimeError(result.get("error", "Failed to stop task"))

    def delete_task(self, task_id: str) -> None:
        """Delete a task."""
        self._request("DELETE", f"/tasks/{task_id}")

    def continue_task(self, task_id: str, message: str, images: Optional[list] = None) -> None:
        """Send a follow-up message to a running task."""
        body: dict = {"input": message}
        if images:
            body["images"] = images
        result = self._request("POST", f"/tasks/{task_id}/continue", body)
        if not result.get("ok"):
            raise RuntimeError(result.get("error", "Failed to continue task"))

    def clean(self, days: int = 30) -> dict:
        """Clean up old tasks, images, and logs on the agent."""
        result = self._request("POST", "/agent/clean", {"days": days})
        return result.get("result", {})

    def start_exec(self, command, work_dir=None, timeout=None, env=None):
        """Start a command on the remote agent."""
        body = {"command": command}
        if work_dir: body["workDir"] = work_dir
        if timeout: body["timeout"] = timeout
        if env: body["env"] = env
        result = self._request("POST", "/exec", body)
        if not result.get("ok"):
            raise RuntimeError(result.get("error", "Failed to start exec"))
        return {"execId": result["execId"], "pid": result.get("pid")}

    def get_exec(self, exec_id):
        """Get exec session details."""
        result = self._request("GET", f"/exec/{exec_id}")
        return result.get("exec", result)

    def list_execs(self):
        """List all exec sessions."""
        result = self._request("GET", "/exec")
        return result.get("execs", [])

    def send_exec_input(self, exec_id, input_text):
        """Send stdin input to a running exec session."""
        self._request("POST", f"/exec/{exec_id}/input", {"input": input_text})

    def signal_exec(self, exec_id, signal):
        """Send a signal to a running exec session."""
        self._request("POST", f"/exec/{exec_id}/signal", {"signal": signal})

    def kill_exec(self, exec_id):
        """Kill and remove an exec session."""
        self._request("DELETE", f"/exec/{exec_id}")

    def stream_exec_output(self, exec_id, poll_interval=0.3):
        """Stream exec output. Yields stdout/stderr chunks as they arrive."""
        last_stdout_len = 0
        last_stderr_len = 0
        while True:
            ex = self.get_exec(exec_id)
            stdout = ex.get("stdout", "")
            stderr = ex.get("stderr", "")
            if len(stdout) > last_stdout_len:
                yield {"type": "stdout", "text": stdout[last_stdout_len:]}
                last_stdout_len = len(stdout)
            if len(stderr) > last_stderr_len:
                yield {"type": "stderr", "text": stderr[last_stderr_len:]}
                last_stderr_len = len(stderr)
            if ex.get("status") in ("completed", "failed", "killed"):
                return
            time.sleep(poll_interval)

    def stream_output(self, task_id: str, poll_interval: float = 0.5) -> Iterator[str]:
        """Stream task output. Yields new output as it arrives."""
        last_len = 0
        while True:
            task = self.get_task(task_id)
            output = task.get("output", "")
            if len(output) > last_len:
                yield output[last_len:]
                last_len = len(output)
            status = task.get("status", "")
            if status in ("completed", "failed", "stopped"):
                return
            time.sleep(poll_interval)


class YaverAuthClient:
    """Auth client for the Convex backend."""

    DEFAULT_CONVEX_URL = "https://perceptive-minnow-557.eu-west-1.convex.site"

    def __init__(self, auth_token: str, convex_url: Optional[str] = None, timeout: float = 10.0):
        self.convex_url = (convex_url or self.DEFAULT_CONVEX_URL).rstrip("/")
        self.auth_token = auth_token
        self.timeout = timeout

    def _request(self, method: str, path: str, body: Any = None) -> dict:
        url = f"{self.convex_url}{path}"
        data = json.dumps(body).encode() if body else None
        req = Request(url, data=data, method=method)
        req.add_header("Authorization", f"Bearer {self.auth_token}")
        if data:
            req.add_header("Content-Type", "application/json")
        with urlopen(req, timeout=self.timeout) as resp:
            return json.loads(resp.read())

    def validate_token(self) -> dict:
        """Validate the auth token and return user info."""
        return self._request("GET", "/auth/validate")

    def list_devices(self) -> list:
        """List registered devices."""
        result = self._request("GET", "/devices")
        return result.get("devices", [])

    def get_settings(self) -> dict:
        """Get user settings."""
        result = self._request("GET", "/settings")
        return result.get("settings", {})

    def save_settings(self, settings: dict) -> None:
        """Save user settings."""
        self._request("POST", "/settings", settings)


# ── Native (ctypes) client ────────────────────────────────────────────

try:
    import ctypes
    import ctypes.util
    import os
    import platform

    def _find_lib():
        """Find the libyaver shared library."""
        # Check common locations
        for name in ["libyaver.so", "libyaver.dylib", "libyaver.dll"]:
            # Same directory as this file
            here = os.path.join(os.path.dirname(__file__), name)
            if os.path.exists(here):
                return here
            # SDK build directory
            sdk_path = os.path.join(os.path.dirname(__file__), "..", "go", "clib", name)
            if os.path.exists(sdk_path):
                return sdk_path
        # System library path
        path = ctypes.util.find_library("yaver")
        if path:
            return path
        return None

    class YaverNativeClient:
        """Native Yaver client using the C shared library (libyaver.so/dylib/dll)."""

        def __init__(self, base_url: str, auth_token: str, lib_path: Optional[str] = None):
            path = lib_path or _find_lib()
            if not path:
                raise FileNotFoundError(
                    "libyaver shared library not found. "
                    "Build it: cd sdk/go/clib && go build -buildmode=c-shared -o libyaver.so ."
                )
            self._lib = ctypes.CDLL(path)

            # Set up function signatures
            self._lib.YaverNewClient.argtypes = [ctypes.c_char_p, ctypes.c_char_p]
            self._lib.YaverNewClient.restype = ctypes.c_int
            self._lib.YaverFreeClient.argtypes = [ctypes.c_int]
            self._lib.YaverHealth.argtypes = [ctypes.c_int]
            self._lib.YaverHealth.restype = ctypes.c_char_p
            self._lib.YaverPing.argtypes = [ctypes.c_int]
            self._lib.YaverPing.restype = ctypes.c_char_p
            self._lib.YaverCreateTask.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_char_p]
            self._lib.YaverCreateTask.restype = ctypes.c_char_p
            self._lib.YaverGetTask.argtypes = [ctypes.c_int, ctypes.c_char_p]
            self._lib.YaverGetTask.restype = ctypes.c_char_p
            self._lib.YaverListTasks.argtypes = [ctypes.c_int]
            self._lib.YaverListTasks.restype = ctypes.c_char_p
            self._lib.YaverStopTask.argtypes = [ctypes.c_int, ctypes.c_char_p]
            self._lib.YaverStopTask.restype = ctypes.c_char_p
            self._lib.YaverTranscribe.argtypes = [ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p]
            self._lib.YaverTranscribe.restype = ctypes.c_char_p
            self._lib.YaverSpeak.argtypes = [ctypes.c_char_p]
            self._lib.YaverSpeak.restype = ctypes.c_char_p

            self._id = self._lib.YaverNewClient(
                base_url.encode(), auth_token.encode()
            )

        def __del__(self):
            if hasattr(self, "_lib") and hasattr(self, "_id"):
                self._lib.YaverFreeClient(self._id)

        def _call(self, result_bytes: bytes) -> dict:
            return json.loads(result_bytes.decode())

        def health(self) -> dict:
            return self._call(self._lib.YaverHealth(self._id))

        def ping(self) -> dict:
            return self._call(self._lib.YaverPing(self._id))

        def create_task(self, prompt: str, opts: Optional[dict] = None) -> dict:
            opts_json = json.dumps(opts).encode() if opts else None
            return self._call(self._lib.YaverCreateTask(self._id, prompt.encode(), opts_json))

        def get_task(self, task_id: str) -> dict:
            return self._call(self._lib.YaverGetTask(self._id, task_id.encode()))

        def list_tasks(self) -> list:
            return self._call(self._lib.YaverListTasks(self._id))

        def stop_task(self, task_id: str) -> dict:
            return self._call(self._lib.YaverStopTask(self._id, task_id.encode()))

        def transcribe(self, audio_path: str, provider: str = "whisper", api_key: str = "") -> dict:
            return self._call(self._lib.YaverTranscribe(
                audio_path.encode(), provider.encode(), api_key.encode()
            ))

        def speak(self, text: str) -> dict:
            return self._call(self._lib.YaverSpeak(text.encode()))

except ImportError:
    pass  # ctypes not available — native client disabled


# ─── Authentication helpers (module-level) ────────────────────────────
#
# Three ways to obtain a Yaver session token from a Python script:
#
#   1. login_with_email(email, password)        — direct credential exchange
#   2. signup_with_email(name, email, password) — same, for new accounts
#   3. signin_via_browser()                     — interactive: opens the user's
#      web browser to https://yaver.io/auth?client=cli, runs a tiny local HTTP
#      listener on port 19836, and waits for the OAuth callback to deliver a
#      token. This is the same flow the `yaver` CLI uses.
#
# The browser flow accepts every OAuth provider yaver.io supports (Apple /
# Google / GitHub / GitLab / Microsoft / email). Email-only callers should
# prefer `login_with_email` so no browser is required.

_DEFAULT_CONVEX_SITE_URL = "https://perceptive-minnow-557.eu-west-1.convex.site"
_DEFAULT_WEB_BASE_URL = "https://yaver.io"
_DEFAULT_LOCAL_AUTH_PORT = 19836


def login_with_email(
    email: str,
    password: str,
    convex_site_url: Optional[str] = None,
    timeout: float = 10.0,
) -> str:
    """Sign in with email + password. Returns a session token.

    Raises RuntimeError on auth failure or when the account requires 2FA
    (TOTP must be completed via the browser flow).
    """
    base = (convex_site_url or _DEFAULT_CONVEX_SITE_URL).rstrip("/")
    body = json.dumps({"email": email, "password": password}).encode()
    req = Request(f"{base}/auth/login", data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    try:
        with urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read())
    except HTTPError as e:
        raise RuntimeError(f"Login failed: {e.code} {e.read().decode(errors='ignore')}") from e
    if data.get("requires2fa"):
        raise RuntimeError(
            "2FA is enabled on this account. Use signin_via_browser() instead."
        )
    token = data.get("token")
    if not isinstance(token, str):
        raise RuntimeError("Login response did not include a token")
    return token


def signup_with_email(
    full_name: str,
    email: str,
    password: str,
    convex_site_url: Optional[str] = None,
    timeout: float = 10.0,
) -> str:
    """Create a new Yaver account. Returns a session token."""
    base = (convex_site_url or _DEFAULT_CONVEX_SITE_URL).rstrip("/")
    body = json.dumps(
        {"fullName": full_name, "email": email, "password": password}
    ).encode()
    req = Request(f"{base}/auth/signup", data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    try:
        with urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read())
    except HTTPError as e:
        raise RuntimeError(f"Signup failed: {e.code} {e.read().decode(errors='ignore')}") from e
    token = data.get("token")
    if not isinstance(token, str):
        raise RuntimeError("Signup response did not include a token")
    return token


def signin_via_browser(
    web_base_url: Optional[str] = None,
    port: int = _DEFAULT_LOCAL_AUTH_PORT,
    open_browser: bool = True,
    timeout: float = 300.0,
) -> str:
    """Interactive OAuth: opens https://yaver.io/auth?client=cli in the
    user's default browser and waits for the callback at
    http://127.0.0.1:<port>/callback?token=... — same flow as the
    `yaver auth` CLI command. Returns the issued session token.

    Raises TimeoutError if the user doesn't complete sign-in within
    `timeout` seconds (default 5 min). Raises RuntimeError if the local
    port is unavailable.
    """
    import http.server
    import threading
    import urllib.parse
    import webbrowser

    base = (web_base_url or _DEFAULT_WEB_BASE_URL).rstrip("/")
    auth_url = f"{base}/auth?client=cli"

    captured: dict = {}
    done = threading.Event()

    class _Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args, **_kwargs):  # silence the default stderr log
            return

        def do_GET(self):  # noqa: N802
            parsed = urllib.parse.urlparse(self.path)
            if parsed.path != "/callback":
                self.send_response(404)
                self.end_headers()
                return
            params = urllib.parse.parse_qs(parsed.query)
            token = (params.get("token") or [None])[0]
            if token:
                captured["token"] = token
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.end_headers()
                self.wfile.write(
                    b"<html><body style='font-family:sans-serif;padding:40px;text-align:center'>"
                    b"<h2>Signed in to Yaver</h2><p>You can close this tab.</p>"
                    b"</body></html>"
                )
            else:
                self.send_response(400)
                self.end_headers()
            done.set()

    try:
        server = http.server.HTTPServer(("127.0.0.1", port), _Handler)
    except OSError as e:
        raise RuntimeError(
            f"Could not bind to 127.0.0.1:{port} for the auth callback ({e}). "
            f"Pass `port=...` if another process is using it."
        ) from e

    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        if open_browser:
            try:
                webbrowser.open(auth_url, new=1, autoraise=True)
            except webbrowser.Error:
                pass  # caller can navigate manually
        print(f"[yaver] Open this URL to sign in: {auth_url}")
        print(f"[yaver] Waiting for callback on http://127.0.0.1:{port}/callback ...")
        if not done.wait(timeout=timeout):
            raise TimeoutError(
                f"Sign-in not completed within {timeout:.0f}s. Cancelled."
            )
    finally:
        server.shutdown()
        server.server_close()

    token = captured.get("token")
    if not isinstance(token, str):
        raise RuntimeError("Auth callback did not include a token")
    return token
