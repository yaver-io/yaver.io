package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const embeddedVSRAdapter = `#!/usr/bin/env python3
from __future__ import annotations
import base64, json, os, re, subprocess, sys, tempfile
from pathlib import Path

def fail(message):
    print(json.dumps({"error": message}), file=sys.stderr)
    raise SystemExit(2)

def request_from_stdin():
    try: value = json.load(sys.stdin)
    except Exception as exc: fail(f"invalid request: {exc}")
    if value.get("language") != "en" or value.get("width") != 96 or value.get("height") != 96:
        fail("expected English 96x96 mouth crops")
    frames = value.get("frames")
    if not isinstance(frames, list) or not 8 <= len(frames) <= 250: fail("expected 8 to 250 mouth frames")
    return value

def write_mouth_video(request, path):
    try:
        import cv2
        import numpy as np
    except ImportError: fail("opencv-python-headless and numpy are required by the local VSR adapter")
    writer = cv2.VideoWriter(str(path), cv2.VideoWriter_fourcc(*"mp4v"), int(request.get("fps", 25)), (96, 96), False)
    if not writer.isOpened(): fail("could not create temporary mouth-only video")
    try:
        for encoded in request["frames"]:
            raw = base64.b64decode(encoded, validate=True)
            if len(raw) != 96 * 96: fail("invalid gray8 frame length")
            writer.write(np.frombuffer(raw, dtype=np.uint8).reshape((96, 96)))
    finally: writer.release()

def infer(request):
    root = Path(os.environ.get("AUTO_AVSR_ROOT", "")).expanduser()
    config, model = os.environ.get("AUTO_AVSR_CONFIG", ""), os.environ.get("AUTO_AVSR_MODEL", "")
    if not root.is_dir() or not config or not Path(model).is_file():
        fail("configure AUTO_AVSR_ROOT, AUTO_AVSR_CONFIG, and AUTO_AVSR_MODEL with a locally licensed model")
    script = root / "infer.py"
    if not script.is_file(): fail(f"Auto-AVSR infer.py was not found under {root}")
    with tempfile.TemporaryDirectory(prefix="yaver-vsr-") as temp:
        video = Path(temp) / "mouth.mp4"
        write_mouth_video(request, video)
        process = subprocess.run([sys.executable, str(script), f"config_filename={config}", f"data_filename={video}", "detector=none"], cwd=root, text=True, capture_output=True, timeout=80)
        if process.returncode != 0: fail((process.stderr or process.stdout or "Auto-AVSR failed")[-1200:])
        matches = re.findall(r"(?:prediction|transcript|hypothesis)\s*[:=]\s*(.+)", process.stdout, flags=re.I)
        lines = [line.strip() for line in process.stdout.splitlines() if line.strip() and not line.lstrip().startswith(("[", "INFO", "DEBUG"))]
        text = (matches[-1] if matches else lines[-1] if lines else "").strip().strip('"')
        if not text: fail("Auto-AVSR returned no transcript")
        return {"text": text, "alternatives": [], "durationMs": 0}

print(json.dumps(infer(request_from_stdin())))
`

func vsrRuntimePaths() (python, adapter string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", ""
	}
	root := filepath.Join(home, ".yaver", "runtimes", "vsr")
	python = filepath.Join(root, "bin", "python3")
	if runtime.GOOS == "windows" {
		python = filepath.Join(root, "Scripts", "python.exe")
	}
	return python, filepath.Join(root, "inference.py")
}

func vsrRuntimeInstalled() bool {
	python, adapter := vsrRuntimePaths()
	if python == "" {
		return false
	}
	if _, err := os.Stat(python); err != nil {
		return false
	}
	if _, err := os.Stat(adapter); err != nil {
		return false
	}
	return true
}

func runVSRInstall(ctx context.Context, progress func(string)) error {
	python, adapter := vsrRuntimePaths()
	if python == "" {
		return fmt.Errorf("could not resolve the current user's home directory")
	}
	basePython, err := exec.LookPath("python3")
	if err != nil {
		return fmt.Errorf("Python 3 is required for remote lip reading; install python3, then retry POST /install/vsr")
	}
	root := filepath.Dir(filepath.Dir(python))
	if progress != nil {
		progress("Creating the owner-local VSR environment at " + root)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if _, err := os.Stat(python); err != nil {
		cmd := exec.CommandContext(ctx, basePython, "-m", "venv", root)
		if output, runErr := cmd.CombinedOutput(); runErr != nil {
			return fmt.Errorf("create VSR environment: %w: %s", runErr, string(output))
		}
	}
	if err := os.WriteFile(adapter, []byte(embeddedVSRAdapter), 0700); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, python, "-m", "pip", "install", "--disable-pip-version-check", "--upgrade", "numpy", "opencv-python-headless")
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		return fmt.Errorf("install VSR libraries: %w: %s", runErr, string(output))
	}
	if progress != nil {
		progress("VSR adapter and mouth-video libraries are ready.")
		progress("Add a locally licensed Auto-AVSR checkout/model in AUTO_AVSR_ROOT, AUTO_AVSR_CONFIG, and AUTO_AVSR_MODEL to enable inference.")
	}
	return nil
}
