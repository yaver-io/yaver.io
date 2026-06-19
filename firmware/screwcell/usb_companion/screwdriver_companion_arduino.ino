/*
  Yaver screwcell USB companion.

  Drives a CAT Power 36V brushed screwdriver motor at 24V through a BTS7960/IBT-2.
  The host Pi sends ASCII commands over USB serial. This keeps Raspberry Pi GPIO
  out of the motor loop.

  Board target: Arduino Nano/Uno compatible.
  Serial: 115200 8N1.

  Wiring:
    D5  -> RPWM
    D6  -> LPWM
    D7  -> R_EN
    D8  -> L_EN
    A0  -> optional forward/current sense
    GND -> BTS7960 GND
    5V  -> BTS7960 VCC

  Motor power:
    EDR +24V -> fuse / kill switch -> BTS7960 B+
    EDR 0V   -> BTS7960 B-
    1000-2200uF / 50V bulk capacitor across B+ / B-
*/

const int RPWM = 5;
const int LPWM = 6;
const int R_EN = 7;
const int L_EN = 8;
const int ISENSE = A0;

const unsigned long BAUD = 115200;
const int MAX_DUTY = 180;          // 0..255, conservative first ceiling.
const int START_DUTY = 14;         // ~5.5% of 255, inrush-friendly first step.
const int RAMP_STEP = 5;
const unsigned long RAMP_MS = 20;
const unsigned long DEAD_MS = 300;
const unsigned long ABS_MAX_MS = 3000;

bool enabled = false;
int currentDir = 0;
int currentDuty = 0;
float currentLimitA = 4.5;

String line;

void applyMotor(int dir, int duty) {
  duty = constrain(duty, 0, MAX_DUTY);
  if (!enabled || dir == 0 || duty == 0) {
    analogWrite(RPWM, 0);
    analogWrite(LPWM, 0);
    currentDir = 0;
    currentDuty = 0;
    return;
  }
  if (dir > 0) {
    analogWrite(LPWM, 0);
    analogWrite(RPWM, duty);
  } else {
    analogWrite(RPWM, 0);
    analogWrite(LPWM, duty);
  }
  currentDir = dir;
  currentDuty = duty;
}

void brakeMotor() {
  analogWrite(RPWM, 0);
  analogWrite(LPWM, 0);
  currentDir = 0;
  currentDuty = 0;
}

void setEnable(bool on) {
  enabled = on;
  digitalWrite(R_EN, on ? HIGH : LOW);
  digitalWrite(L_EN, on ? HIGH : LOW);
  if (!on) brakeMotor();
}

float readCurrentA() {
  // Placeholder scale: calibrate per BTS7960 board/sense divider.
  int raw = analogRead(ISENSE);
  float volts = raw * (5.0 / 1023.0);
  return volts / 0.059;
}

void rampTo(int dir, int targetDuty) {
  targetDuty = constrain(targetDuty, 0, MAX_DUTY);
  if (dir != currentDir && currentDuty > 0) {
    for (int d = currentDuty; d >= 0; d -= RAMP_STEP) {
      applyMotor(currentDir, d);
      delay(RAMP_MS);
    }
    brakeMotor();
    delay(DEAD_MS);
  }
  int d = currentDuty;
  if (d == 0 && targetDuty > 0) {
    d = min(START_DUTY, targetDuty);
    applyMotor(dir, d);
    delay(RAMP_MS);
  }
  while (d < targetDuty) {
    float amps = readCurrentA();
    if (amps > currentLimitA) {
      delay(RAMP_MS);
      continue;
    }
    d = min(targetDuty, d + RAMP_STEP);
    applyMotor(dir, d);
    delay(RAMP_MS);
  }
  while (d > targetDuty) {
    d = max(targetDuty, d - RAMP_STEP);
    applyMotor(dir, d);
    delay(RAMP_MS);
  }
}

void cmdDrive(String dirToken, int dutyPct, unsigned long maxMs) {
  if (!enabled) {
    Serial.println("ERR disabled call_ENABLE_first");
    return;
  }
  int dir = 0;
  dirToken.toUpperCase();
  if (dirToken == "FWD" || dirToken == "FORWARD" || dirToken == "1") dir = 1;
  if (dirToken == "REV" || dirToken == "REVERSE" || dirToken == "-1") dir = -1;
  if (dir == 0) {
    Serial.println("ERR bad_dir use_FWD_or_REV");
    return;
  }
  int targetDuty = map(constrain(dutyPct, 0, 100), 0, 100, 0, MAX_DUTY);
  maxMs = min(maxMs, ABS_MAX_MS);
  unsigned long start = millis();
  float peak = 0.0;

  rampTo(dir, targetDuty);
  while (millis() - start < maxMs) {
    float amps = readCurrentA();
    if (amps > peak) peak = amps;
    if (amps > currentLimitA * 1.5) break;
    delay(5);
  }
  brakeMotor();
  Serial.print("OK done=1 peak_a=");
  Serial.print(peak, 2);
  Serial.print(" ms=");
  Serial.println(millis() - start);
}

String nextToken(String &s) {
  s.trim();
  int p = s.indexOf(' ');
  if (p < 0) {
    String out = s;
    s = "";
    return out;
  }
  String out = s.substring(0, p);
  s = s.substring(p + 1);
  return out;
}

void handleLine(String s) {
  s.trim();
  if (s.length() == 0) return;
  String cmd = nextToken(s);
  cmd.toUpperCase();

  if (cmd == "PING") {
    Serial.println("OK pong=1");
  } else if (cmd == "STATUS") {
    Serial.print("OK enabled=");
    Serial.print(enabled ? 1 : 0);
    Serial.print(" dir=");
    Serial.print(currentDir);
    Serial.print(" duty=");
    Serial.print(currentDuty);
    Serial.print(" amps=");
    Serial.println(readCurrentA(), 2);
  } else if (cmd == "ENABLE") {
    setEnable(true);
    Serial.println("OK enabled=1");
  } else if (cmd == "DISABLE") {
    setEnable(false);
    Serial.println("OK enabled=0");
  } else if (cmd == "BRAKE") {
    brakeMotor();
    Serial.println("OK brake=1");
  } else if (cmd == "LIMIT") {
    currentLimitA = s.toFloat();
    if (currentLimitA < 0.5) currentLimitA = 0.5;
    Serial.print("OK limit_a=");
    Serial.println(currentLimitA, 2);
  } else if (cmd == "DRIVE") {
    String dir = nextToken(s);
    int duty = nextToken(s).toInt();
    unsigned long ms = nextToken(s).toInt();
    if (ms == 0) ms = 500;
    cmdDrive(dir, duty, ms);
  } else {
    Serial.println("ERR unknown_command");
  }
}

void setup() {
  pinMode(RPWM, OUTPUT);
  pinMode(LPWM, OUTPUT);
  pinMode(R_EN, OUTPUT);
  pinMode(L_EN, OUTPUT);
  setEnable(false);
  Serial.begin(BAUD);
  Serial.setTimeout(50);
}

void loop() {
  while (Serial.available()) {
    char c = (char)Serial.read();
    if (c == '\n' || c == '\r') {
      handleLine(line);
      line = "";
    } else if (line.length() < 96) {
      line += c;
    }
  }
}

