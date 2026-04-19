# Yaver Python SDK

Embed Yaver's local-first agent runtime into your Python applications. Zero dependencies (stdlib only).

## Install

```bash
pip install yaver
```

## Quick Start

```python
from yaver import YaverClient

client = YaverClient("http://localhost:18080", "your-auth-token")

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
