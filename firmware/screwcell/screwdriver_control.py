#!/usr/bin/env python3
"""
screwdriver_control.py — BTS7960 brushed-motor controller for Raspberry Pi,
tuned to the SPICE analysis of the real rig.

RIG (see robotics/wirecell/firmware/screwdriver_motor_setup.md):
  - Motor : CAT Power 36V brushed DC, run UNDER-VOLTED at 24V (safe: BTS7960 max 27V)
  - Supply: Mean Well EDR-120-24 (24V / 5A / 120W; constant-current overload, auto-recovery)
  - Driver: BTS7960 / IBT-2 ; Pi GPIO is 3.3V logic, which the BTS7960 accepts
  - HARDWARE: a 1000–2200µF / 50V bulk cap across B+/B− is REQUIRED (SPICE: a
    brake/disable spike hits 32.7V at 100µF → trips the EDR OVP & the 27V driver
    limit; ≥1000µF → 26.7V, safe). 100nF across the motor for brush noise.

Wiring (BCM pin numbers):
  GPIO12 -> RPWM   (forward PWM, hardware PWM0)
  GPIO13 -> LPWM   (reverse PWM, hardware PWM1)
  GPIO5  -> R_EN   (forward enable)
  GPIO6  -> L_EN   (reverse enable)
  3.3V   -> VCC ;  GND -> GND  (also tie Pi GND to the supply −V at B−)
  Stage-2 (optional, current sensing / clutch-slip):
  R_IS/L_IS -> 10k+10k divider -> MCP3008 CH0/CH1 ; MCP3008 on SPI0 (CE0)

WHY SOFT-START (SPICE): a hard step to full duty demands ~47A inrush (9× the
EDR's 5A). At standstill back-EMF=0, so to keep inrush under the supply limit the
FIRST duty step must satisfy  duty*Vsupply/Rm < I_limit.  We compute that ceiling
below and cap the first ramp step to it; the EDR's constant-current limit is the
backstop. With the MCP3008 present we additionally hold the ramp whenever measured
current exceeds the budget — a closed-loop soft-start that survives a stalled start.

Requires pigpio:  sudo apt install pigpio python3-pigpio ;  sudo pigpiod
Run:  python3 screwdriver_control.py --demo   (or --drive / --back / --monitor / --calibrate)
"""
import argparse
import time

try:
    import pigpio
except ImportError:
    pigpio = None
try:
    import spidev
except ImportError:
    spidev = None

# ---- Pin configuration (BCM numbering) ----
RPWM = 12   # forward PWM (hardware PWM0)
LPWM = 13   # reverse PWM (hardware PWM1)
R_EN = 5    # forward enable
L_EN = 6    # reverse enable

# ---- Rig constants (from the SPICE model — measure to refine) ----
SUPPLY_V       = 24.0    # EDR-120-24
MOTOR_R_OHM    = 0.30    # armature resistance (SPICE / multimeter across the motor)
INRUSH_LIMIT_A = 4.5     # stay just under the EDR's 5A constant-current limit

# Max duty (%) you may apply at standstill without exceeding the inrush budget:
#   duty/100 * SUPPLY_V / MOTOR_R_OHM < INRUSH_LIMIT_A
MAX_START_DUTY = max(1.0, INRUSH_LIMIT_A * MOTOR_R_OHM / SUPPLY_V * 100.0)  # ~5.6%

# ---- Ramp / PWM tuning ----
PWM_FREQUENCY = 16000   # 16 kHz: smooth, above audible whine, under the 25 kHz BTS7960 limit
MAX_DUTY      = 100     # hard speed ceiling (lower e.g. to 40 for first runs)
RAMP_STEP     = 2.0     # %/tick — KEEP <= MAX_START_DUTY so the first step is inrush-safe
RAMP_INTERVAL = 0.02    # s between ramp ticks (20 ms)
DEAD_TIME     = 0.3     # s to fully stop (dynamic brake) before reversing — keeps the
                        # ~11J of rotor energy in the motor, not pumped to the rail
MAX_DRIVE_MS  = 2500    # safety watchdog for a rundown (open-loop has no stall sense)

# ---- Stage-2 current sensing (MCP3008) ----
SPI_BUS, SPI_DEV = 0, 0
CH_FWD, CH_REV   = 0, 1
ADC_VREF         = 3.3
# Volts-per-amp at the MCP3008 input: BTS7960 IS ≈ 0.118 V/A on a 1k-sense IBT-2,
# halved by the 10k+10k divider ≈ 0.059 V/A. Calibrate per board.
V_PER_AMP        = 0.059

if RAMP_STEP > MAX_START_DUTY:
    # never let the first step blow the inrush budget
    RAMP_STEP = round(MAX_START_DUTY, 1)


class Screwdriver:
    def __init__(self, use_adc=True):
        if pigpio is None:
            raise SystemExit("pigpio not installed — `sudo apt install python3-pigpio` and `sudo pigpiod`")
        self.pi = pigpio.pi()
        if not self.pi.connected:
            raise SystemExit("pigpio daemon not running — start it with `sudo pigpiod`")
        for pin in (R_EN, L_EN):
            self.pi.set_mode(pin, pigpio.OUTPUT)
            self.pi.write(pin, 0)
        self.pi.hardware_PWM(RPWM, PWM_FREQUENCY, 0)
        self.pi.hardware_PWM(LPWM, PWM_FREQUENCY, 0)
        self._state = {"direction": 0, "duty": 0.0}
        self.spi = None
        self.baseline_a = 0.0
        if use_adc and spidev is not None:
            try:
                self.spi = spidev.SpiDev()
                self.spi.open(SPI_BUS, SPI_DEV)
                self.spi.max_speed_hz = 1_350_000
            except Exception:
                self.spi = None

    # ---- low level ----
    def _duty_pp(self, percent):
        percent = max(0.0, min(MAX_DUTY, percent))
        return int(percent / 100.0 * 1_000_000)

    def enable(self):
        self.pi.write(R_EN, 1)
        self.pi.write(L_EN, 1)

    def disable(self):
        self.pi.write(R_EN, 0)
        self.pi.write(L_EN, 0)

    def _apply(self, direction, percent):
        """SAFETY: only one of RPWM/LPWM ever carries PWM; the other forced to 0.
        Never both high (would shoot-through the BTS7960)."""
        duty = self._duty_pp(percent)
        if direction > 0:
            self.pi.hardware_PWM(LPWM, PWM_FREQUENCY, 0)
            self.pi.hardware_PWM(RPWM, PWM_FREQUENCY, duty)
        elif direction < 0:
            self.pi.hardware_PWM(RPWM, PWM_FREQUENCY, 0)
            self.pi.hardware_PWM(LPWM, PWM_FREQUENCY, duty)
        else:
            self.pi.hardware_PWM(RPWM, PWM_FREQUENCY, 0)
            self.pi.hardware_PWM(LPWM, PWM_FREQUENCY, 0)

    def read_amps(self, channel):
        """Motor current via the BTS7960 IS pin → MCP3008 (stage-2). 0 if no ADC."""
        if self.spi is None:
            return 0.0
        r = self.spi.xfer2([1, (8 + channel) << 4, 0])
        counts = ((r[1] & 3) << 8) | r[2]
        volts = counts / 1023.0 * ADC_VREF
        return max(0.0, volts / V_PER_AMP - self.baseline_a)

    # ---- soft-start ramp ----
    def ramp_to(self, direction, target_percent, current_limit_a=INRUSH_LIMIT_A):
        """Smoothly ramp to a target speed/direction.
        - first step is capped to MAX_START_DUTY (inrush budget)
        - on direction change: ramp down, dynamic-brake dead-time, then ramp up
        - if an ADC is present, HOLD the ramp whenever current exceeds the budget
          (closed-loop soft-start: survives starting into a tight/stalled screw)."""
        target_percent = max(0.0, min(MAX_DUTY, target_percent))

        # direction change → ramp down, brake, dead-time
        if direction != self._state["direction"] and self._state["duty"] > 0:
            while self._state["duty"] > 0:
                self._state["duty"] = max(0.0, self._state["duty"] - RAMP_STEP)
                self._apply(self._state["direction"], self._state["duty"])
                time.sleep(RAMP_INTERVAL)
            self.brake()
            time.sleep(DEAD_TIME)
        self._state["direction"] = direction

        starting_from_zero = self._state["duty"] <= 0.0
        while abs(self._state["duty"] - target_percent) >= RAMP_STEP:
            going_up = self._state["duty"] < target_percent
            if going_up:
                step = RAMP_STEP
                # cap the very first step out of standstill to the inrush budget
                if starting_from_zero and self._state["duty"] == 0.0:
                    step = min(step, MAX_START_DUTY)
                # closed-loop guard: don't add duty while over the current budget
                if self.spi is not None:
                    if self.read_amps(CH_FWD if direction > 0 else CH_REV) > current_limit_a:
                        time.sleep(RAMP_INTERVAL)
                        continue
                self._state["duty"] += step
            else:
                self._state["duty"] -= RAMP_STEP
            self._apply(direction, self._state["duty"])
            time.sleep(RAMP_INTERVAL)
            starting_from_zero = False

        self._state["duty"] = target_percent
        self._apply(direction, self._state["duty"])

    def brake(self):
        """Dynamic brake: both inputs low (motor shorted through the low-side FETs).
        SPICE-confirmed rail-safe — rotor energy burns in the motor, not the supply."""
        self._apply(0, 0)
        self._state["direction"] = 0
        self._state["duty"] = 0.0

    def stop(self):
        self.brake()

    def cleanup(self):
        self.brake()
        self.disable()
        self.pi.hardware_PWM(RPWM, 0, 0)
        self.pi.hardware_PWM(LPWM, 0, 0)
        if self.spi:
            self.spi.close()
        self.pi.stop()

    # ---- stage-2: calibrate + closed-loop rundown ----
    def calibrate(self, samples=200):
        """Free-spin and record the baseline (no-load) current, so clutch-slip is
        detected as a RISE above it. Needs the MCP3008 fitted."""
        if self.spi is None:
            return 0.0
        self.enable()
        self.ramp_to(+1, 50)
        time.sleep(0.2)
        acc = 0.0
        for _ in range(samples):
            acc += self.read_amps(CH_FWD)
            time.sleep(0.002)
        self.brake()
        self.baseline_a = acc / samples
        return self.baseline_a

    def drive_screw(self, duty=60, stall_a=None):
        """One rundown. With an ADC: ramp, then watch current; brake on clutch-slip
        (a rise of >=0.6A over baseline by default) → PASS, or timeout → FAIL.
        Without an ADC: open-loop, drive for the watchdog window trusting the clutch."""
        self.enable()
        thresh = (self.baseline_a + 0.6) if stall_a is None else stall_a
        t0 = time.monotonic()
        self.ramp_to(+1, duty)
        ema = 0.0
        peak = 0.0
        over = 0
        while True:
            ms = (time.monotonic() - t0) * 1000.0
            if self.spi is not None:
                a = self.read_amps(CH_FWD)
                peak = max(peak, a)
                ema = a if ema == 0 else 0.3 * a + 0.7 * ema
                if ema >= thresh:
                    over += 1
                    if over >= 3:
                        self.brake()
                        return {"ok": True, "ms": round(ms), "peak_a": round(peak, 2), "reason": "clutch_slip"}
                else:
                    over = 0
            if ms >= MAX_DRIVE_MS:
                self.brake()
                ok = self.spi is None  # open-loop: timeout is the normal end-of-drive
                return {"ok": ok, "ms": round(ms), "peak_a": round(peak, 2),
                        "reason": "timeout" if self.spi is not None else "open_loop_done"}
            time.sleep(0.003)

    def back_out(self, ms, duty=60):
        self.enable()
        self.ramp_to(-1, duty)
        time.sleep(ms / 1000.0)
        self.brake()

    def monitor(self):
        self.enable()
        self.ramp_to(+1, 50)
        try:
            while True:
                print(f"fwd={self.read_amps(CH_FWD):5.2f}A rev={self.read_amps(CH_REV):5.2f}A "
                      f"(baseline={self.baseline_a:.2f}A, inrush_budget={INRUSH_LIMIT_A}A)")
                time.sleep(0.1)
        except KeyboardInterrupt:
            pass
        finally:
            self.brake()


def main():
    ap = argparse.ArgumentParser(description="BTS7960 + EDR-120-24 screwdriver driver (sim-tuned soft-start)")
    ap.add_argument("--demo", action="store_true", help="forward/full/reverse/stop demo")
    ap.add_argument("--drive", action="store_true", help="one rundown (clutch-slip if ADC fitted)")
    ap.add_argument("--back", action="store_true", help="back a screw out (reverse)")
    ap.add_argument("--calibrate", action="store_true", help="record free-run baseline current")
    ap.add_argument("--monitor", action="store_true", help="live current readout")
    ap.add_argument("--ms", type=int, default=600)
    ap.add_argument("--duty", type=float, default=60)
    args = ap.parse_args()

    print(f"[soft-start] first-step ceiling = {MAX_START_DUTY:.1f}% duty "
          f"(keeps standstill inrush < {INRUSH_LIMIT_A}A on a {SUPPLY_V:.0f}V / {MOTOR_R_OHM}Ω motor)")
    sd = Screwdriver()
    try:
        if args.calibrate:
            print(f"baseline = {sd.calibrate():.2f} A")
        elif args.monitor:
            sd.monitor()
        elif args.back:
            sd.back_out(args.ms, args.duty); print(f"backed out {args.ms} ms")
        elif args.drive:
            r = sd.drive_screw(args.duty)
            print(("PASS" if r["ok"] else "FAIL"), f"{r['ms']}ms peak={r['peak_a']}A ({r['reason']})")
        else:  # demo
            sd.enable()
            print("soft-start forward 60%…"); sd.ramp_to(+1, 60); time.sleep(2)
            print("up to full…");            sd.ramp_to(+1, MAX_DUTY); time.sleep(2)
            print("reverse 50% (ramp-down + dead-time)…"); sd.ramp_to(-1, 50); time.sleep(2)
            print("stop."); sd.ramp_to(0, 0); sd.brake()
    except KeyboardInterrupt:
        print("\ninterrupted.")
    finally:
        sd.cleanup()
        print("cleaned up, motor disabled.")


if __name__ == "__main__":
    main()
