#!/usr/bin/env python3
"""yaver → LeRobot dataset exporter. Converts the on-disk demo episodes (the
jpg+jsonl layout the Go DemoRecorder / yaver_sim_demo_gen.py write) into a
LeRobotDataset that lerobot-train consumes to train ACT / Diffusion Policy /
SmolVLA.

The action for imitation is the NEXT joint state (kinesthetic/teleop convention:
the arm followed the demonstrator, so action[t] := state[t+1]). Images are stored
as frames (use_videos=False) to avoid an ffmpeg video-encode dependency.

Written for lerobot 0.5.1:  from lerobot.datasets.lerobot_dataset import LeRobotDataset
Run inside the lerobot venv:
    python yaver_lerobot_export.py --in ~/.yaver/arm-demos/reach-target \
        --repo-id yaver/reach-target --root /tmp/yaver-lerobot/reach-target
"""
import argparse
import json
import os
import sys

import numpy as np
from PIL import Image
from lerobot.datasets.lerobot_dataset import LeRobotDataset


def load_episode(ep_dir):
    meta = json.load(open(os.path.join(ep_dir, "meta.json")))
    states = [json.loads(l) for l in open(os.path.join(ep_dir, "states.jsonl")) if l.strip()]
    frames = sorted(os.listdir(os.path.join(ep_dir, "frames")))
    return meta, states, frames


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--in", dest="inp", required=True, help="task dir of episode_NNN folders")
    ap.add_argument("--repo-id", default="yaver/reach-target")
    ap.add_argument("--root", required=True, help="local dataset output dir")
    ap.add_argument("--fps", type=int, default=10)
    args = ap.parse_args()

    eps = sorted(d for d in os.listdir(args.inp) if d.startswith("episode_"))
    if not eps:
        sys.exit("no episode_* dirs in %s" % args.inp)

    # joint order from the first episode's meta
    meta0, _, frames0 = load_episode(os.path.join(args.inp, eps[0]))
    joint_names = [j["name"] for j in meta0["joints"]]
    dof = len(joint_names)
    img0 = np.array(Image.open(os.path.join(args.inp, eps[0], "frames", frames0[0])).convert("RGB"))
    h, w, _ = img0.shape

    features = {
        "observation.images.main": {"dtype": "image", "shape": (h, w, 3), "names": ["height", "width", "channels"]},
        "observation.state": {"dtype": "float32", "shape": (dof,), "names": joint_names},
        "action": {"dtype": "float32", "shape": (dof,), "names": joint_names},
    }
    if os.path.exists(args.root):
        sys.exit("root %s exists — remove it first (LeRobotDataset.create won't overwrite)" % args.root)
    ds = LeRobotDataset.create(repo_id=args.repo_id, fps=args.fps, features=features,
                               root=args.root, use_videos=False)

    total = 0
    for ep in eps:
        ep_dir = os.path.join(args.inp, ep)
        meta, states, frames = load_episode(ep_dir)
        prompt = meta.get("prompt") or "reach the target"
        n = min(len(states), len(frames))
        for t in range(n - 1):  # drop last (no next-state action)
            img = np.array(Image.open(os.path.join(ep_dir, "frames", frames[t])).convert("RGB"))
            state = np.array([states[t]["joints"].get(j, 0.0) for j in joint_names], dtype=np.float32)
            action = np.array([states[t + 1]["joints"].get(j, 0.0) for j in joint_names], dtype=np.float32)
            ds.add_frame({
                "observation.images.main": img,
                "observation.state": state,
                "action": action,
                "task": prompt,
            })
            total += 1
        ds.save_episode()
        print("exported %s (%d frames)" % (ep, n - 1), file=sys.stderr)

    print("DONE: %d episodes, %d frames -> %s (repo_id=%s)" % (len(eps), total, args.root, args.repo_id))


if __name__ == "__main__":
    main()
