# Yaver Python SDK

Embed Yaver's local-first agent runtime into your Python applications. Zero dependencies (stdlib only).

## Install

```bash
pip install yaver
```

## Quick Start

### Getting an auth token

Three ways, depending on context:

```python
import yaver

# 1. Email + password — non-interactive, good for scripts/CI
token = yaver.login_with_email("you@example.com", "your-password")

# 2. Sign up a new account — non-interactive
token = yaver.signup_with_email("Your Name", "you@example.com", "your-password")

# 3. Browser OAuth — interactive, supports Apple/Google/GitHub/GitLab/Microsoft
#    Opens https://yaver.io/auth?client=cli in the default browser and runs
#    a tiny local HTTP listener on 127.0.0.1:19836 to capture the callback.
#    Same flow as the `yaver auth` CLI command.
token = yaver.signin_via_browser()
```

Tokens are long-lived (30 days). Cache them in your app's keystore or env var; a fresh sign-in is only required after expiry or sign-out.

### Using the client

```python
from yaver import YaverClient

client = YaverClient("http://localhost:18080", token)

# Create a task
task = client.create_task("Fix the login bug")
print(f"Task {task['id']} created")

# Stream output
for chunk in client.stream_output(task["id"]):
    print(chunk, end="")

# List all tasks
tasks = client.list_tasks()
```

## Features

- **Task management**: create, list, get, stop, delete, continue tasks
- **Output streaming**: poll-based streaming with configurable interval
- **Auth client**: validate tokens, list devices, manage settings via Convex backend
- **Verbosity control**: set response detail level 0-10
- **Native mode**: optional ctypes bindings via C shared library (libyaver.so)
- **Zero dependencies**: uses only Python stdlib

## Auth Client

```python
from yaver import YaverAuthClient

auth = YaverAuthClient("your-token")
user = auth.validate_token()
devices = auth.list_devices()
settings = auth.get_settings()
```

## Links

- [Yaver](https://yaver.io) — main site
- [GitHub](https://github.com/kivanccakmak/yaver.io) — source code
- [SDK docs](https://github.com/kivanccakmak/yaver.io/tree/main/sdk) — all SDKs
