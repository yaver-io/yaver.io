`stackDetectInvalidate(root string)` was added in-scope under `desktop/agent/stack_detect.go`, but wiring it into `POST /projects/refresh` was not done in this run because the handler lives in out-of-scope `desktop/agent/httpserver.go`.

The intended follow-up is to update `handleProjectsRefresh` so an explicit refresh invalidates cached stack detections for the refreshed roots before or alongside `discoverProjects()`. That keeps the HTTP refresh semantics aligned with the new fingerprint cache without violating the scope allowlist from this task.
