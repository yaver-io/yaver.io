#!/usr/bin/env python3
"""Dev-only VSR stand-in: proves the mouth-frame pipeline without a model.

This adapter speaks the exact same stdin/stdout contract as `inference.py`
(one JSON request in, one JSON result out), but performs NO recognition. It
exists so capture -> transport -> agent session -> result can be exercised
end to end from any camera surface while no licensed Auto-AVSR checkpoint is
configured. See docs/silent-input.md.

It is NOT a recognizer and must never ship as the default. It returns a text
string that names itself a stub so no surface can mistake it for transcription.

Enable it explicitly:

    export YAVER_VSR_COMMAND="python3 /path/to/desktop/vsr-runtime/yaver_vsr/dev_stub.py"

The agent only reaches this file when YAVER_VSR_COMMAND is set, so it can
never be selected accidentally.
"""
from __future__ import annotations

import base64
import json
import sys

EXPECTED_FRAME_BYTES = 96 * 96


def fail(message: str) -> None:
    print(json.dumps({"error": message}), file=sys.stderr)
    raise SystemExit(2)


def request_from_stdin() -> dict:
    try:
        value = json.load(sys.stdin)
    except Exception as exc:  # noqa: BLE001 - surface any parse failure verbatim
        fail(f"invalid request: {exc}")
    if value.get("language") != "en" or value.get("width") != 96 or value.get("height") != 96:
        fail("expected English 96x96 mouth crops")
    frames = value.get("frames")
    if not isinstance(frames, list) or not 8 <= len(frames) <= 250:
        fail("expected 8 to 250 mouth frames")
    # Validate the frame contract the real adapter enforces, so a capture bug is
    # caught here (where it is cheap) rather than only once a model is wired in.
    for index, encoded in enumerate(frames):
        try:
            raw = base64.b64decode(encoded, validate=True)
        except Exception:  # noqa: BLE001
            fail(f"frame {index} is not valid base64")
        if len(raw) != EXPECTED_FRAME_BYTES:
            fail(f"frame {index} is {len(raw)} bytes, expected {EXPECTED_FRAME_BYTES}")
    return value


def main() -> None:
    request = request_from_stdin()
    frames = request["frames"]
    fps = int(request.get("fps", 25) or 25)
    seconds = len(frames) / fps if fps else 0.0
    print(
        json.dumps(
            {
                "text": f"[VSR dev stub] received {len(frames)} mouth frames ({seconds:.1f}s) — no licensed model configured",
                "alternatives": [],
                "durationMs": 0,
                "stub": True,
            }
        )
    )


if __name__ == "__main__":
    main()
