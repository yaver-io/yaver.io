#!/usr/bin/env python3
"""Adapter between Yaver's mouth-frame protocol and an Auto-AVSR checkout.

The agent writes one JSON request to stdin and expects one JSON result on
stdout. Full-face video is never accepted. A temporary mouth-only MP4 exists
only for the duration of the upstream Auto-AVSR invocation.
"""
from __future__ import annotations

import base64
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


def fail(message: str) -> None:
    print(json.dumps({"error": message}), file=sys.stderr)
    raise SystemExit(2)


def request_from_stdin() -> dict:
    try:
        value = json.load(sys.stdin)
    except Exception as exc:
        fail(f"invalid request: {exc}")
    if value.get("language") != "en" or value.get("width") != 96 or value.get("height") != 96:
        fail("expected English 96x96 mouth crops")
    frames = value.get("frames")
    if not isinstance(frames, list) or not 8 <= len(frames) <= 250:
        fail("expected 8 to 250 mouth frames")
    return value


def write_mouth_video(request: dict, path: Path) -> None:
    try:
        import cv2
        import numpy as np
    except ImportError:
        fail("opencv-python and numpy are required by the local VSR adapter")
    writer = cv2.VideoWriter(str(path), cv2.VideoWriter_fourcc(*"mp4v"), int(request.get("fps", 25)), (96, 96), False)
    if not writer.isOpened():
        fail("could not create temporary mouth-only video")
    try:
        for encoded in request["frames"]:
            raw = base64.b64decode(encoded, validate=True)
            if len(raw) != 96 * 96:
                fail("invalid gray8 frame length")
            writer.write(np.frombuffer(raw, dtype=np.uint8).reshape((96, 96)))
    finally:
        writer.release()


def parse_transcript(output: str) -> str:
    # Auto-AVSR forks differ in logging. Prefer an explicit prediction label,
    # otherwise take the final non-log line. The adapter never guesses text.
    matches = re.findall(r"(?:prediction|transcript|hypothesis)\s*[:=]\s*(.+)", output, flags=re.IGNORECASE)
    if matches:
        return matches[-1].strip().strip('"')
    lines = [line.strip() for line in output.splitlines() if line.strip() and not line.lstrip().startswith(("[", "INFO", "DEBUG"))]
    return lines[-1].strip('"') if lines else ""


def infer(request: dict) -> dict:
    root = Path(os.environ.get("AUTO_AVSR_ROOT", "")).expanduser()
    config = os.environ.get("AUTO_AVSR_CONFIG", "")
    model = os.environ.get("AUTO_AVSR_MODEL", "")
    if not root.is_dir() or not config or not Path(model).is_file():
        fail("set AUTO_AVSR_ROOT, AUTO_AVSR_CONFIG, and AUTO_AVSR_MODEL to a locally licensed installation")
    infer_script = root / "infer.py"
    if not infer_script.is_file():
        fail(f"Auto-AVSR infer.py was not found under {root}")
    with tempfile.TemporaryDirectory(prefix="yaver-vsr-") as temp:
        video = Path(temp) / "mouth.mp4"
        write_mouth_video(request, video)
        process = subprocess.run(
            [sys.executable, str(infer_script), f"config_filename={config}", f"data_filename={video}", "detector=none"],
            cwd=root,
            text=True,
            capture_output=True,
            timeout=80,
            check=False,
        )
        if process.returncode != 0:
            fail((process.stderr or process.stdout or "Auto-AVSR failed")[-1200:])
        text = parse_transcript(process.stdout)
        if not text:
            fail("Auto-AVSR returned no transcript")
        return {"text": text, "alternatives": [], "durationMs": 0}


if __name__ == "__main__":
    print(json.dumps(infer(request_from_stdin())))
