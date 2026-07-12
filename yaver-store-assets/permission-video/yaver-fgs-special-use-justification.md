# Permission justification — android.permission.FOREGROUND_SERVICE_SPECIAL_USE

## "What tasks require this permission?" → Other

On-device tool: the user starts an on-device coding agent running a user-started mobile development task; it runs to completion with an ongoing notification and a completion notification, and the user can stop it at any time.

## "Describe your app's use of this permission…"

Yaver is an on-device tool. When the user explicitly starts a task, Yaver runs an on-device coding agent running a user-started mobile development task. The work is stateful and can take minutes: it streams progress to the user through an ongoing notification, and posts a completion notification when it finishes.

The task must run in a foreground service and cannot be paused, deferred, or restarted: it is a single, user-initiated session that holds in-flight state and live connections. If the OS froze or killed the process when the user switched away — which Android does to ordinary background work within seconds — the running task would be lost and the user's work discarded. The service is SandboxService; the foreground state and wake lock exist specifically so the user-started session survives while the app is backgrounded, and the user remains in control via the persistent notification and an in-app Stop control.

This use case is not covered by any standard foreground service type (it is not media playback, location, data sync, camera, microphone, phone call, connected device, health, or remote messaging). It is declared as specialUse with the subtype "on_device_coding_agent".

## Demo video shot-list

- 1. User opens Yaver and starts an on-device coding agent running a user-started mobile development task
- 2. The task begins real work — the task shows live progress ("Running")
- 3. The ongoing foreground notification shows the process is being kept alive
- 4. User leaves the app — without the foreground service Android would kill the process and lose the in-flight work
- 5. The task keeps running in the background and completes
- 6. the user gets a “Task finished” notification while the app is backgrounded
- 7. User can stop the task anytime — the service and notification end
