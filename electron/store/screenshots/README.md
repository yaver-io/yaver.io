# Windows Store screenshot capture

Capture on the exact clean Windows 11 x64 candidate after the automated
preflight passes. Use a synthetic certification account and synthetic project.

Required frames:

1. Signed-in desktop workspace with the local Windows PC selected (agent case).
2. A synthetic coding task with live output visible.
3. A browser preview of a synthetic sample project.
4. Remote-node task or preview (client case).
5. Device switcher showing local and synthetic remote nodes (combined case).
6. Rendering evidence covering Browser Lane and, where applicable, Hermes or
   WebRTC with no unsupported capability implied by the screenshot.

The audited 0.1.11 frame was removed from the repository because it visibly
contained an operator-derived device label, a real machine name, runner errors,
and desktop slivers. Never reuse or upload that frame.

There are currently zero approved Windows Store screenshots. Capture new frames
from the exact candidate with a synthetic certification account and synthetic
device/project names before submission.

Desktop screenshots must be PNG, at least 1366 x 768, and should use a clean
16:9 app-only frame. Do not include desktop slivers, other applications,
notifications, error-state clutter, or controls outside the claimed workflow.
Provide five to eight truthful frames even though Partner Center requires only
one. The current 1080 x 1080 box art and 300 x 300 app tile are usable; a 2:3
poster is still missing and should be added for a complete merchandising set.

Before saving each image, verify that it contains no real name, email, customer,
repository, file path, token, device identifier, IP address, relay hostname,
notification, browser history, or unrelated application. Keep raw screenshots
out of Git until this review is complete; commit only the final Store-safe set.
