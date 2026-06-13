#!/usr/bin/env python3
"""
endercli.py — command-line control for the Ender cell (Marlin/Klipper serial)
-----------------------------------------------------------------------------
Quick jogging, homing, gripper, raw G-code, and an interactive REPL.

examples:
    ./endercli.py jog X 10              # relative move X +10mm
    ./endercli.py jog Z -1 --feed 600
    ./endercli.py home                 # G28 all
    ./endercli.py home Z
    ./endercli.py grip close
    ./endercli.py gcode "M114"
    ./endercli.py status               # position + endstops
    ./endercli.py repl                 # interactive; type gcode or x+ / y- / z+ ...

    ./endercli.py --port /dev/ttyUSB0 jog Y -5
    (port auto-detected if omitted)

deps: pip install pyserial
"""
import argparse, glob, sys, time

try:
    import serial
except ImportError:
    print("pip install pyserial", file=sys.stderr); sys.exit(1)

GRIP_PIN = 8

def find_port():
    for pat in ("/dev/ttyUSB*", "/dev/ttyACM*", "/dev/tty.*usb*"):
        hits = sorted(glob.glob(pat))
        if hits:
            return hits[0]
    return None

class Ender:
    def __init__(self, port, baud=115200):
        self.ser = serial.Serial(port, baud, timeout=2)
        time.sleep(2); self.ser.reset_input_buffer()
    def cmd(self, line, wait=True, quiet=False):
        self.ser.write((line.strip() + "\n").encode())
        if not wait:
            return ""
        out, t0 = [], time.time()
        while time.time() - t0 < 8:
            ln = self.ser.readline().decode(errors="ignore").strip()
            if ln:
                out.append(ln)
                if ln.startswith("ok") or "error" in ln.lower():
                    break
        r = "\n".join(out)
        if not quiet and r:
            print(r)
        return r
    def jog(self, axis, dist, feed=1500):
        self.cmd("G91", quiet=True); self.cmd(f"G0 {axis.upper()}{dist} F{feed}"); self.cmd("G90", quiet=True)
    def home(self, axes=""):
        self.cmd("G28 " + axes)
    def grip(self, state):
        self.cmd(f"M42 P{GRIP_PIN} S{255 if state=='close' else 0}")
    def status(self):
        self.cmd("M114"); self.cmd("M119")

def repl(e):
    print("REPL — type G-code, or shortcuts: x+ x- y+ y- z+ z- (step set by 's <mm>'), 'g open/close', 'h', 'q'")
    step = 1.0
    while True:
        try:
            line = input("ender> ").strip()
        except (EOFError, KeyboardInterrupt):
            print(); break
        if not line: continue
        if line in ("q", "quit", "exit"): break
        if line == "h": e.home(); continue
        if line.startswith("s "):
            step = float(line.split()[1]); print(f"step={step}mm"); continue
        if line.startswith("g "):
            e.grip(line.split()[1]); continue
        m = {"x+":("X",step),"x-":("X",-step),"y+":("Y",step),"y-":("Y",-step),"z+":("Z",step),"z-":("Z",-step)}
        if line in m:
            e.jog(*m[line]); continue
        e.cmd(line)

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port"); ap.add_argument("--baud", type=int, default=115200)
    ap.add_argument("--feed", type=int, default=1500)
    sub = ap.add_subparsers(dest="action", required=True)
    j = sub.add_parser("jog"); j.add_argument("axis"); j.add_argument("dist", type=float)
    h = sub.add_parser("home"); h.add_argument("axes", nargs="?", default="")
    g = sub.add_parser("grip"); g.add_argument("state", choices=["open", "close"])
    c = sub.add_parser("gcode"); c.add_argument("line")
    sub.add_parser("status"); sub.add_parser("repl"); sub.add_parser("ports")
    a = ap.parse_args()

    if a.action == "ports":
        print("\n".join(sorted(glob.glob("/dev/ttyUSB*")+glob.glob("/dev/ttyACM*")+glob.glob("/dev/tty.*usb*")) or ["(none)"])); return

    port = a.port or find_port()
    if not port:
        print("no serial port found; pass --port", file=sys.stderr); sys.exit(1)
    e = Ender(port, a.baud)

    if   a.action == "jog":    e.jog(a.axis, a.dist, a.feed)
    elif a.action == "home":   e.home(a.axes)
    elif a.action == "grip":   e.grip(a.state)
    elif a.action == "gcode":  e.cmd(a.line)
    elif a.action == "status": e.status()
    elif a.action == "repl":   repl(e)

if __name__ == "__main__":
    main()
