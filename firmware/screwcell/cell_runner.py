#!/usr/bin/env python3
"""
cell_runner.py — the screw-driving CELL: Ender (XY/Z positioning) + the BTS7960
screwdriver (screwdriver_control.py) driving each terminal-block screw, logging
PASS/FAIL per screw.

This ties together the two pieces:
  - endercli.Ender          → Ender-3 as a Cartesian robot over serial (NEVER heated)
  - screwdriver_control.Screwdriver → CAT Power 36V motor @24V via BTS7960 + EDR-120-24,
                                       soft-start ramp + dynamic brake + (stage-2)
                                       MCP3008 clutch-slip detection

It REPLACES the old relay on/off (ender_terminalblock_2d.py) with the variable-speed
driver, so each düz/slotted screw uses the spin-while-creeping "find the slot" cycle
and ends on a real clutch-slip (PASS) instead of a fixed dwell.

Per screw (one pole):
  1. Ender: rapid to the pole's Y, bit clear (Z_CLEAR)
  2. Screwdriver: ramp to SEEK_DUTY (slow spin)
  3. Ender: creep Z down to Z_SEEK at a slow feed → the spinning blade cams into the slot
  4. Screwdriver: ramp to DRIVE_DUTY
  5. drive: feed Z down to Z_DRIVE to seat the screw; with an MCP3008, stop on clutch-slip
  6. Screwdriver: dynamic brake; Ender: lift to Z_CLEAR
  7. log PASS (clutch-slip / seated) or FAIL (timeout / no slip)

Coordinate convention (NO bed homing): jog the bit to sit clear above pole 0, then this
program G92-zeros there. Z<0 = down toward the screw; Y increases along the screw row.

Run on the Ender host (magara):  python3 cell_runner.py --screws 5 --pitch 5.8
Dry run (traverse only, no driving): python3 cell_runner.py --screws 5 --dry-run
deps: pyserial, pigpio (+ optional spidev for the MCP3008)
"""
import argparse
import json
import os
import socket
import time
import urllib.request

from endercli import Ender, find_port
try:
    from screwdriver_control import Screwdriver
except Exception:
    Screwdriver = None

# ---- cell geometry / motion (mm, mm/min) — calibrate to your jig ----
PITCH_Y       = 5.8     # screw-to-screw spacing along the row
Z_CLEAR       = 0.0     # bit clear above the head (origin)
Z_SEEK        = -7.0    # depth where the blade reaches the slot
Z_DRIVE       = -10.0   # depth that seats the screw to torque
FEED_TRAVERSE = 3000    # Y rapid between poles
FEED_SEEK     = 60      # slow Z creep so the blade finds the slot
FEED_DRIVE    = 120     # Z feed while seating
SETTLE_S      = 0.2

# ---- screwdriver speeds (% duty) ----
SEEK_DUTY  = 30         # slow spin to find the slot
DRIVE_DUTY = 70         # seat the screw


class ScrewCell:
    def __init__(self, port=None, dry_run=False):
        self.dry_run = dry_run
        self.ender = Ender(port or find_port())
        self.ender.cmd("G90")          # absolute
        self.ender.cmd("G92 X0 Y0 Z0") # zero here (bit clear above pole 0)
        self.sd = None
        if not dry_run:
            if Screwdriver is None:
                raise SystemExit("screwdriver_control unavailable (pigpio?) — use --dry-run to traverse")
            self.sd = Screwdriver()
            self.sd.enable()
            # learn the no-load baseline for clutch-slip detection (if MCP3008 present)
            if self.sd.spi is not None:
                base = self.sd.calibrate()
                print(f"[cell] clutch-slip baseline = {base:.2f} A")

    def _z(self, z, feed):
        self.ender.cmd(f"G1 Z{z:.3f} F{feed}", quiet=True)
        self.ender.cmd("M400", quiet=True)

    def drive_one(self, pole_y):
        """Run one screw at Y=pole_y. Returns a result dict."""
        # 1. rapid to the pole, bit clear
        self.ender.cmd(f"G0 Y{pole_y:.3f} Z{Z_CLEAR:.3f} F{FEED_TRAVERSE}", quiet=True)
        self.ender.cmd("M400", quiet=True)
        if self.dry_run:
            self._z(Z_SEEK, FEED_SEEK); time.sleep(0.4); self._z(Z_CLEAR, FEED_TRAVERSE)
            return {"y": pole_y, "ok": True, "reason": "dry_run"}

        t0 = time.monotonic()
        try:
            # 2-3. SEEK: spin slow + creep down so the blade cams into the slot
            self.sd.ramp_to(+1, SEEK_DUTY)
            self._z(Z_SEEK, FEED_SEEK)
            # 4. DRIVE: spin up
            self.sd.ramp_to(+1, DRIVE_DUTY)
            # 5. seat: with an ADC, watch clutch-slip while feeding to depth; else feed + dwell
            if self.sd.spi is not None:
                self._z(Z_DRIVE, FEED_DRIVE)          # feed to depth (non-blocking move queued)
                res = self.sd.drive_screw(duty=DRIVE_DUTY)  # blocks until clutch-slip or timeout
                ok, reason, peak = res["ok"], res["reason"], res.get("peak_a")
            else:
                self._z(Z_DRIVE, FEED_DRIVE)
                time.sleep(0.4)                        # open-loop: trust the clutch
                ok, reason, peak = True, "clutch_open_loop", None
        finally:
            # 6. brake + lift clear
            self.sd.brake()
            self._z(Z_CLEAR, FEED_TRAVERSE)
        return {"y": pole_y, "ok": ok, "reason": reason, "peak_a": peak, "ms": round((time.monotonic() - t0) * 1000)}

    def run(self, n_screws, pitch=PITCH_Y):
        results = []
        for i in range(n_screws):
            y = i * pitch
            r = self.drive_one(y)
            tag = "PASS" if r["ok"] else "FAIL"
            print(f"  screw {i:2d} @Y{y:6.2f}  {tag}  ({r.get('reason')}"
                  f"{'' if r.get('peak_a') is None else f', peak={r['peak_a']}A'}"
                  f"{'' if 'ms' not in r else f', {r['ms']}ms'})")
            results.append({"i": i, **r})
        return results

    def calibrate_z(self, step=0.25, max_drop=14.0):
        """Auto-find the screw-head contact Z: spin the bit slowly and creep down,
        watching the MCP3008 current — it rises sharply when the blade touches the
        head. Prints suggested Z_SEEK / Z_DRIVE. Needs the MCP3008 (stage-2)."""
        if self.dry_run or self.sd is None or self.sd.spi is None:
            raise SystemExit("calibrate_z needs the MCP3008 (current sensing)")
        self.ender.cmd(f"G0 Y0 Z{Z_CLEAR:.3f} F{FEED_TRAVERSE}", quiet=True)
        self.ender.cmd("M400", quiet=True)
        self.sd.ramp_to(+1, SEEK_DUTY)
        time.sleep(0.4)
        base = sum(self.sd.read_amps(0) for _ in range(40)) / 40.0
        contact = None
        z = Z_CLEAR
        while z > Z_CLEAR - max_drop:
            z -= step
            self._z(z, FEED_SEEK)
            a = sum(self.sd.read_amps(0) for _ in range(8)) / 8.0
            if a > base + 0.4:            # current jumped → blade is on the head
                contact = z
                break
        self.sd.brake()
        self._z(Z_CLEAR, FEED_TRAVERSE)
        if contact is None:
            print(f"[cal] no contact within {max_drop}mm (base={base:.2f}A) — check the jig height")
            return None
        print(f"[cal] contact at Z={contact:.2f} (base={base:.2f}A). Suggested:"
              f"  Z_SEEK={contact:.2f}  Z_DRIVE={contact - 3.0:.2f}")
        return contact

    def push_to_yaver(self, label, results, ficheno=None, product=None, agent_url=None):
        """POST the block's PASS/FAIL to the local Yaver agent (screw_cell_record op).
        Auth = the agent's bearer token from ~/.yaver/config.json."""
        passed = sum(1 for r in results if r["ok"])
        token = ""
        try:
            with open(os.path.expanduser("~/.yaver/config.json")) as f:
                token = json.load(f).get("auth_token", "")
        except Exception:
            pass
        if not token:
            print("[yaver] no auth token (~/.yaver/config.json) — run `yaver auth`")
            return
        base = (agent_url or "http://127.0.0.1:18080").rstrip("/")
        body = json.dumps({
            "verb": "screw_cell_record",
            "payload": {
                "label": label, "ficheno": ficheno, "productId": product,
                "screws": len(results), "passed": passed,
                "host": socket.gethostname(), "results": results,
            },
        }).encode()
        req = urllib.request.Request(
            base + "/ops", data=body,
            headers={"Content-Type": "application/json", "Authorization": f"Bearer {token}"})
        try:
            with urllib.request.urlopen(req, timeout=15) as resp:
                print(f"[yaver] recorded ({resp.status}) — {passed}/{len(results)} PASS")
        except Exception as e:
            print(f"[yaver] push failed: {e}")

    def cleanup(self):
        if self.sd:
            self.sd.cleanup()
        # park clear; leave Ender powered for the next block
        try:
            self.ender.cmd(f"G0 Z{Z_CLEAR:.3f} F{FEED_TRAVERSE}", quiet=True)
        except Exception:
            pass


def main():
    ap = argparse.ArgumentParser(description="Screw-driving cell: Ender + BTS7960 screwdriver")
    ap.add_argument("--screws", type=int, default=5, help="number of screws in the block")
    ap.add_argument("--pitch", type=float, default=PITCH_Y, help="Y pitch (mm)")
    ap.add_argument("--port", help="Ender serial port (auto if omitted)")
    ap.add_argument("--dry-run", action="store_true", help="traverse + dip only, no driving")
    ap.add_argument("--calibrate-z", action="store_true", help="auto-find the screw-head contact Z (needs MCP3008)")
    ap.add_argument("--log", help="write per-screw results to this JSON file")
    ap.add_argument("--label", help="block/job label for the Yaver record")
    ap.add_argument("--ficheno", help="production order this block belongs to (auto-count + flag)")
    ap.add_argument("--product", help="product code")
    ap.add_argument("--yaver", nargs="?", const="http://127.0.0.1:18080",
                    help="push results to the local Yaver agent screw_cell_record op (optional URL; default 127.0.0.1:18080)")
    args = ap.parse_args()

    cell = ScrewCell(port=args.port, dry_run=args.dry_run)
    try:
        if args.calibrate_z:
            cell.calibrate_z()
            return
        print(f"[cell] {'DRY-RUN ' if args.dry_run else ''}block: {args.screws} screws @ {args.pitch}mm pitch")
        results = cell.run(args.screws, args.pitch)
        passed = sum(1 for r in results if r["ok"])
        print(f"[cell] done — {passed}/{len(results)} PASS")
        if args.log:
            with open(args.log, "w") as f:
                json.dump({"screws": args.screws, "pitch": args.pitch, "results": results}, f, indent=2)
            print(f"[cell] log → {args.log}")
        if args.yaver and not args.dry_run:
            cell.push_to_yaver(args.label or args.ficheno or "screw-block", results,
                               ficheno=args.ficheno, product=args.product, agent_url=args.yaver)
    except KeyboardInterrupt:
        print("\n[cell] interrupted.")
    finally:
        cell.cleanup()
        print("[cell] cleaned up.")


if __name__ == "__main__":
    main()
