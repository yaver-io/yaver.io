// yaver_companion — the Yaver wire-harness companion MCU.
//
// Gives the cell the I/O the Ender-3 board lacks: extra GPIO and — the point —
// FORCE/TORQUE sensing so a screw can be driven to a real torque target instead
// of a blind dwell (docs/yaver-companion-mcu.md). Runs on Arduino Uno/Nano,
// RP2040 (Pico) or ESP32. Talks the same line protocol as robot/companion.go
// over USB-serial @115200 (or BLE-serial on ESP32).
//
//   PING             -> PONG
//   ZERO             -> OK            (tare the load cell)
//   SENSE            -> S cur=<mA> force=<g> tq=<Nmm>
//   GPIO <pin> <0|1> -> OK
//   STREAM <hz>      -> repeated "S ..." lines (0 = stop)
//   MATRIXPINS <p..> -> OK <n>        (declare the continuity test-point pins)
//   MATRIX           -> "MX <i> <j..>" per driven point, then "MX DONE"
//
// MATRIX is the harness self-test bed (docs/yaver-harness-automation-thesis.md
// P0): wire each harness test point (via the mating connectors) to one declared
// pin. MATRIX drives each pin LOW in turn with all others INPUT_PULLUP and
// reports which other pins read LOW (= electrically connected). The host diffs
// the reported adjacency against the recipe's from/to list -> opens / shorts /
// mis-wires, 100% inline, no instruments. Index-based (position in MATRIXPINS),
// so the host owns the pin->net map. Portable: uses only INPUT_PULLUP (AVR ok).
//
// Sensors are optional & compile-guarded — wire what you have:
//   INA219 (I2C)  : current on the screwdriver supply  -> torque via motor kT
//   HX711 + cell  : direct force on the jig            -> torque via lever arm
// If both are present the load cell wins (direct measurement).

#define USE_INA219 1   // set 0 if not fitted
#define USE_HX711  1   // set 0 if not fitted

#include <Wire.h>
#if USE_INA219
  #include <Adafruit_INA219.h>
  Adafruit_INA219 ina219;
#endif
#if USE_HX711
  #include "HX711.h"
  HX711 scale;
  const int HX711_DT  = 4;
  const int HX711_SCK = 5;
#endif

// Screwdriver MOSFET/relay (companion-driven option; or drive it from the
// printer FAN port instead and leave this unused).
const int TOOL_PIN = 6;

// --- calibration (set for your hardware) ---------------------------------
// Motor torque constant: N·mm per amp of screwdriver current (from the motor
// datasheet or a one-time calibration against a torque wrench).
const float KT_NMM_PER_A = 320.0;
// Load-cell: counts-per-gram (from `ZERO` + a known weight) and the effective
// lever arm (mm) from the cell to the screw axis, so force -> torque.
float HX_COUNTS_PER_G = 420.0;
const float ARM_MM     = 18.0;
// -------------------------------------------------------------------------

int streamHz = 0;
unsigned long lastStreamMs = 0;

// --- continuity matrix (harness self-test) -------------------------------
#define MAX_MATRIX_PINS 24
int matrixPins[MAX_MATRIX_PINS];
int matrixCount = 0;
const int MATRIX_SETTLE_US = 300;   // let pull-ups/charge settle before read
// -------------------------------------------------------------------------

float readCurrentmA() {
#if USE_INA219
  return ina219.getCurrent_mA();
#else
  return 0;
#endif
}

float readForceG() {
#if USE_HX711
  if (scale.is_ready()) {
    long raw = scale.read_average(2);
    return raw / HX_COUNTS_PER_G;
  }
#endif
  return 0;
}

// Torque estimate (N·mm): prefer the load cell (direct), else current * kT.
float estimateTorqueNmm(float currentmA, float forceG) {
#if USE_HX711
  float forceN = (forceG / 1000.0) * 9.80665;
  if (forceN > 0.01) return forceN * ARM_MM;
#endif
  return (currentmA / 1000.0) * KT_NMM_PER_A;
}

// Drive each declared pin LOW in turn (others INPUT_PULLUP) and report which
// other declared pins read LOW -> electrically connected. Index-based.
void runMatrix() {
  if (matrixCount == 0) { Serial.println("ERR no matrix pins"); return; }
  for (int i = 0; i < matrixCount; i++) pinMode(matrixPins[i], INPUT_PULLUP);
  for (int i = 0; i < matrixCount; i++) {
    pinMode(matrixPins[i], OUTPUT);
    digitalWrite(matrixPins[i], LOW);
    delayMicroseconds(MATRIX_SETTLE_US);
    Serial.print("MX "); Serial.print(i);
    for (int j = 0; j < matrixCount; j++) {
      if (j == i) continue;
      if (digitalRead(matrixPins[j]) == LOW) { Serial.print(' '); Serial.print(j); }
    }
    Serial.println();
    pinMode(matrixPins[i], INPUT_PULLUP);   // restore before next driver
  }
  Serial.println("MX DONE");
}

void emitSense() {
  float cur = readCurrentmA();
  float force = readForceG();
  float tq = estimateTorqueNmm(cur, force);
  Serial.print("S cur="); Serial.print(cur, 1);
  Serial.print(" force=");  Serial.print(force, 1);
  Serial.print(" tq=");     Serial.println(tq, 1);
}

void setup() {
  Serial.begin(115200);
  pinMode(TOOL_PIN, OUTPUT);
  digitalWrite(TOOL_PIN, LOW);
  Wire.begin();
#if USE_INA219
  ina219.begin();
#endif
#if USE_HX711
  scale.begin(HX711_DT, HX711_SCK);
  scale.tare();
#endif
}

void handleLine(String line) {
  line.trim();
  if (line == "PING") { Serial.println("PONG"); return; }
  if (line == "ZERO") {
#if USE_HX711
    scale.tare();
#endif
    Serial.println("OK");
    return;
  }
  if (line == "SENSE") { emitSense(); return; }
  if (line.startsWith("GPIO ")) {
    int sp = line.indexOf(' ', 5);
    if (sp > 0) {
      int pin = line.substring(5, sp).toInt();
      int val = line.substring(sp + 1).toInt();
      pinMode(pin, OUTPUT);
      digitalWrite(pin, val ? HIGH : LOW);
      Serial.println("OK");
      return;
    }
  }
  if (line.startsWith("STREAM ")) {
    streamHz = line.substring(7).toInt();
    Serial.println("OK");
    return;
  }
  if (line.startsWith("MATRIXPINS")) {
    matrixCount = 0;
    int idx = 10;                                  // past "MATRIXPINS"
    while (idx < (int)line.length() && matrixCount < MAX_MATRIX_PINS) {
      while (idx < (int)line.length() && line[idx] == ' ') idx++;
      if (idx >= (int)line.length()) break;
      int sp = line.indexOf(' ', idx);
      String tok = (sp < 0) ? line.substring(idx) : line.substring(idx, sp);
      tok.trim();
      if (tok.length() > 0) matrixPins[matrixCount++] = tok.toInt();
      idx = (sp < 0) ? line.length() : sp + 1;
    }
    for (int i = 0; i < matrixCount; i++) pinMode(matrixPins[i], INPUT_PULLUP);
    Serial.print("OK "); Serial.println(matrixCount);
    return;
  }
  if (line == "MATRIX") { runMatrix(); return; }
  Serial.println("ERR unknown");
}

void loop() {
  if (Serial.available()) {
    String line = Serial.readStringUntil('\n');
    handleLine(line);
  }
  if (streamHz > 0) {
    unsigned long now = millis();
    if (now - lastStreamMs >= (unsigned long)(1000 / streamHz)) {
      lastStreamMs = now;
      emitSense();
    }
  }
}
