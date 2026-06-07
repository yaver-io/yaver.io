// Yaver Connector Box — ESP32-S3 facade firmware.
//
// The box is a FACADE, not a PC: this firmware only bridges the phone link to the
// machine bus and reports the box's own health. No OS, no Yaver-on-box — the phone
// is the brain (AI runner, vision, netcapture). See ../README.md & firmware/README.md.
//
//   WIRED  : phone is USB host. Raw machine data goes through the FT232 (plain
//            USB-serial, no firmware). This ESP exposes the line-based CONTROL /
//            companion protocol over USB-CDC, and (when BOX_HAS_FT232) RELEASES
//            the RS485 driver so it never fights the FT232 for the bus.
//   WIRELESS: phone joins the box SoftAP. The ESP OWNS the bus and serves
//            Modbus-TCP(:502) + raw-TCP(:9000) gateways ⇄ RS485/RS232, plus the
//            CONTROL protocol on :8347. Lets the phone+camera sit anywhere.
//
// Control protocol (text, newline-terminated) is identical on USB-CDC and TCP:8347
// and is a superset of desktop/agent/robot/companion.go so the existing companion
// driver works against the box unchanged.

#include <Arduino.h>
#include <WiFi.h>
#include <Wire.h>
#include "config.h"

// ── globals ────────────────────────────────────────────────────────────────
HardwareSerial RS485(1);
HardwareSerial RS232(2);

WiFiServer modbusServer(PORT_MODBUSTCP);
WiFiServer rawServer(PORT_RAWTCP);
WiFiServer ctrlServer(PORT_CONTROL);

WiFiClient rawClient;
WiFiClient ctrlClient;

String   activeBus   = "rs485";   // rs485|rs232|can
uint32_t busBaud     = DEFAULT_BAUD;
bool     abSwap      = false;
bool     termOn      = false;
bool     biasOn      = false;
uint16_t streamHz    = 0;
bool     wiredHost   = false;     // a USB-CDC host (phone) is connected
bool     ownBus      = true;      // ESP currently drives the RS485 transmitter
uint32_t lastStream  = 0;
uint32_t lastLink    = 0;         // last time we saw any link traffic
char     chipId[13];

// ── forward declarations ─────────────────────────────────────────────────────
static bool abAutoDetect();
static String senseLine();
static void applyBusOptions();

// ── CRC-16 (Modbus RTU, poly 0xA001) ────────────────────────────────────────
static uint16_t crc16(const uint8_t* d, size_t n) {
  uint16_t crc = 0xFFFF;
  for (size_t i = 0; i < n; i++) {
    crc ^= d[i];
    for (int b = 0; b < 8; b++) crc = (crc & 1) ? (crc >> 1) ^ 0xA001 : crc >> 1;
  }
  return crc;
}

// ── bus ownership / RS485 driver-enable ──────────────────────────────────────
static void releaseBus() { ownBus = false; digitalWrite(PIN_RS485_DE, LOW); }   // receive-only / hi-Z to FT232
static void takeBus()    { ownBus = true;  digitalWrite(PIN_RS485_DE, LOW); }   // ready to drive on TX

static void applyBusOptions() {
  digitalWrite(PIN_AB_SEL,  abSwap ? HIGH : LOW);
  digitalWrite(PIN_TERM_EN, termOn ? HIGH : LOW);
  digitalWrite(PIN_BIAS_EN, biasOn ? HIGH : LOW);
}

static HardwareSerial& busUart() { return (activeBus == "rs232") ? RS232 : RS485; }

// Half-duplex RS485 transaction: drive DE, send req, read reply until inter-byte
// gap or timeout. Returns reply length (incl. CRC for RTU) into out.
static size_t busTxn(const uint8_t* req, size_t reqLen, uint8_t* out, size_t outMax,
                     uint32_t timeoutMs) {
  HardwareSerial& u = busUart();
  while (u.available()) u.read();                 // flush stale
  bool is485 = (activeBus == "rs485");
  if (is485) { if (!ownBus) takeBus(); digitalWrite(PIN_RS485_DE, HIGH); delayMicroseconds(50); }
  u.write(req, reqLen);
  u.flush();                                      // wait TX complete
  if (is485) { digitalWrite(PIN_RS485_DE, LOW); }
  size_t n = 0;
  uint32_t start = millis(), lastByte = millis();
  while (millis() - start < timeoutMs) {
    if (u.available()) {
      while (u.available() && n < outMax) { out[n++] = u.read(); lastByte = millis(); }
    } else if (n > 0 && millis() - lastByte > 5) {
      break;                                      // RTU inter-frame gap
    }
  }
  return n;
}

// ── status LED ───────────────────────────────────────────────────────────────
static void led(uint8_t r, uint8_t g, uint8_t b) {
#if defined(RGB_BUILTIN) || defined(PIN_LED_RGB)
  neopixelWrite(PIN_LED_RGB, r, g, b);
#endif
}

// ── INA219 input-power telemetry (raw register read, no library) ──────────────
static bool ina219Read(int32_t& vin_mV, int32_t& ibus_mA) {
  // bus voltage reg 0x02: bits [15:3] * 4 mV. shunt reg 0x01: LSB 10 µV.
  auto rd = [](uint8_t reg, uint16_t& v) -> bool {
    Wire.beginTransmission(INA219_ADDR); Wire.write(reg);
    if (Wire.endTransmission(false) != 0) return false;
    if (Wire.requestFrom((int)INA219_ADDR, 2) != 2) return false;
    v = (Wire.read() << 8) | Wire.read();
    return true;
  };
  uint16_t bus = 0, sh = 0;
  if (!rd(0x02, bus) || !rd(0x01, sh)) return false;
  vin_mV  = ((int16_t)bus >> 3) * 4;
  int32_t shunt_uV = (int16_t)sh * 10;            // signed
  ibus_mA = shunt_uV / 10;                         // I = Vsh / Rshunt(0.01Ω) → uV/10 = mA
  return true;
}

// ── HX711 force/torque (optional) ────────────────────────────────────────────
static long hx711Read() {
  if (digitalRead(PIN_HX711_DT) == HIGH) return 0; // not ready
  long v = 0;
  for (int i = 0; i < 24; i++) {
    digitalWrite(PIN_HX711_SCK, HIGH); delayMicroseconds(1);
    v = (v << 1) | digitalRead(PIN_HX711_DT);
    digitalWrite(PIN_HX711_SCK, LOW);  delayMicroseconds(1);
  }
  digitalWrite(PIN_HX711_SCK, HIGH); delayMicroseconds(1);  // 25th pulse: gain 128
  digitalWrite(PIN_HX711_SCK, LOW);
  if (v & 0x800000) v |= ~0xFFFFFF;                // sign-extend 24→32
  return v;
}

static String senseLine() {
  int32_t vin = 0, ibus = 0; ina219Read(vin, ibus);
  long f = hx711Read();
  // cur/force/tq are placeholders until calibrated; vin/ibus are the live power
  // telemetry the phone uses for charging/brown-out analytics.
  char buf[128];
  snprintf(buf, sizeof(buf), "S cur=%ld force=%ld tq=%ld vin=%ld ibus=%ld",
           (long)ibus, f, 0L, (long)vin, (long)ibus);
  return String(buf);
}

// ── control protocol ─────────────────────────────────────────────────────────
static void reply(Stream& s, const String& line) { s.print(line); s.print("\n"); }

static void handleControl(const String& raw, Stream& s) {
  String line = raw; line.trim();
  if (line.length() == 0) return;
  lastLink = millis();
  int sp = line.indexOf(' ');
  String cmd = (sp < 0 ? line : line.substring(0, sp)); cmd.toUpperCase();
  String arg = (sp < 0 ? String("") : line.substring(sp + 1)); arg.trim();

  if (cmd == "PING")      { reply(s, "PONG"); }
  else if (cmd == "INFO") {
    reply(s, String("INFO fw=") + FW_VERSION + " id=" + chipId +
              " link=" + (wiredHost ? "usb" : "wifi") + " bus=" + activeBus +
              " baud=" + busBaud + " ab=" + (abSwap?1:0) +
              " term=" + (termOn?1:0) + " bias=" + (biasOn?1:0));
  }
  else if (cmd == "SENSE") { reply(s, senseLine()); }
  else if (cmd == "ZERO")  { reply(s, "OK"); /* tare TODO: store offset */ }
  else if (cmd == "STREAM"){ streamHz = (uint16_t)arg.toInt(); reply(s, "OK"); }
  else if (cmd == "BUS")   { if (arg=="rs485"||arg=="rs232"||arg=="can"){activeBus=arg; reply(s,"OK");} else reply(s,"ERR bus"); }
  else if (cmd == "BAUD")  { busBaud=(uint32_t)arg.toInt(); RS485.updateBaudRate(busBaud); RS232.updateBaudRate(busBaud); reply(s,"OK"); }
  else if (cmd == "ABSWAP"){
    if (arg=="AUTO") { reply(s, abAutoDetect() ? "OK ab=auto-resolved" : "ERR no-reply-either-polarity"); }
    else { abSwap = arg.toInt()!=0; applyBusOptions(); reply(s,"OK"); }
  }
  else if (cmd == "TERM")  { termOn = arg.toInt()!=0; applyBusOptions(); reply(s,"OK"); }
  else if (cmd == "BIAS")  { biasOn = arg.toInt()!=0; applyBusOptions(); reply(s,"OK"); }
  else if (cmd == "GPIO")  { reply(s, "OK"); /* advisory only — never a safety chain */ }
  else if (cmd == "LED")   {
    int r=0,g=0,b=0; sscanf(arg.c_str(), "%d %d %d", &r,&g,&b); led(r,g,b); reply(s,"OK");
  }
  else reply(s, "ERR unknown");
}

// Try a benign Modbus read on each A/B polarity; keep the one that yields a
// CRC-valid reply. Helps past the RS485 "A/B not standardized" gotcha.
static bool abAutoDetect() {
  if (activeBus != "rs485") return false;
  uint8_t probe[8] = {0x01,0x03,0x00,0x00,0x00,0x01,0,0};   // unit1 read 1 holding @0
  uint16_t c = crc16(probe, 6); probe[6]=c&0xFF; probe[7]=c>>8;
  uint8_t resp[64];
  for (int s = 0; s <= 1; s++) {
    abSwap = (s == 1); applyBusOptions(); delay(20);
    size_t n = busTxn(probe, 8, resp, sizeof(resp), 200);
    if (n >= 5 && crc16(resp, n) == 0) return true;          // full-frame CRC incl. trailer == 0
  }
  return false;
}

// ── Modbus-TCP(:502) ⇄ RTU gateway (wireless mode) ───────────────────────────
static void serviceModbusTCP() {
  static WiFiClient mc;
  if (!mc || !mc.connected()) { mc = modbusServer.available(); if (!mc) return; }
  if (mc.available() < 7) return;
  uint8_t mbap[6];
  for (int i=0;i<6;i++) mbap[i]=mc.read();
  uint16_t len = (mbap[4]<<8)|mbap[5];             // unit + PDU length
  if (len < 2 || len > 253) { mc.stop(); return; }
  uint8_t pdu[256]; size_t got=0; uint32_t t0=millis();
  while (got<len && millis()-t0<200) { if (mc.available()) pdu[got++]=mc.read(); }
  if (got<len) return;
  lastLink = millis();
  // build RTU: [unit][PDU...] + CRC
  uint8_t rtu[260]; memcpy(rtu, pdu, len);
  uint16_t c = crc16(rtu, len); rtu[len]=c&0xFF; rtu[len+1]=c>>8;
  uint8_t resp[260];
  size_t rn = busTxn(rtu, len+2, resp, sizeof(resp), RS485_RX_TIMEOUT_MS);
  if (rn < 4 || crc16(resp, rn) != 0) {            // timeout / bad reply → Modbus exception 0x0B (gateway target failed to respond)
    uint8_t ex[3] = { pdu[0], (uint8_t)(pdu[1]|0x80), 0x0B };
    uint8_t hdr[6] = { mbap[0],mbap[1],0,0,0,3 };
    mc.write(hdr,6); mc.write(ex,3); return;
  }
  uint16_t plen = rn - 2;                          // strip CRC
  uint8_t hdr[6] = { mbap[0],mbap[1],0,0,(uint8_t)(plen>>8),(uint8_t)(plen&0xFF) };
  mc.write(hdr,6); mc.write(resp, plen);
}

// ── raw-TCP(:9000) transparent bridge ⇄ active UART (wireless mode) ──────────
static void serviceRawTCP() {
  if (!rawClient || !rawClient.connected()) { rawClient = rawServer.available(); if (!rawClient) return; }
  HardwareSerial& u = busUart();
  bool is485 = (activeBus=="rs485");
  // TCP → bus
  if (rawClient.available()) {
    lastLink = millis();
    if (is485 && ownBus) { digitalWrite(PIN_RS485_DE,HIGH); delayMicroseconds(50); }
    while (rawClient.available()) u.write(rawClient.read());
    u.flush();
    if (is485) digitalWrite(PIN_RS485_DE,LOW);
  }
  // bus → TCP
  while (u.available()) { rawClient.write(u.read()); lastLink = millis(); }
}

// ── control transport: USB-CDC + TCP:8347 ────────────────────────────────────
static void serviceControl() {
  // USB-CDC (wired)
  static String cdcBuf;
  while (Serial.available()) {
    char ch = Serial.read();
    if (ch=='\n'||ch=='\r') { if(cdcBuf.length()) { handleControl(cdcBuf, Serial); cdcBuf=""; } }
    else if (cdcBuf.length()<200) cdcBuf += ch;
  }
  // TCP control (wireless)
  if (!ctrlClient || !ctrlClient.connected()) ctrlClient = ctrlServer.available();
  if (ctrlClient && ctrlClient.connected()) {
    static String tcpBuf;
    while (ctrlClient.available()) {
      char ch = ctrlClient.read();
      if (ch=='\n'||ch=='\r') { if(tcpBuf.length()){ handleControl(tcpBuf, ctrlClient); tcpBuf=""; } }
      else if (tcpBuf.length()<200) tcpBuf += ch;
    }
  }
}

// ── setup / loop ─────────────────────────────────────────────────────────────
void setup() {
  pinMode(PIN_RS485_DE, OUTPUT); digitalWrite(PIN_RS485_DE, LOW);
  pinMode(PIN_AB_SEL, OUTPUT); pinMode(PIN_TERM_EN, OUTPUT); pinMode(PIN_BIAS_EN, OUTPUT);
  pinMode(PIN_HX711_SCK, OUTPUT); pinMode(PIN_HX711_DT, INPUT);
  pinMode(PIN_BOOT, INPUT_PULLUP);
  applyBusOptions();

  Serial.begin(115200);                            // USB-CDC control (wired)
  RS485.begin(busBaud, SERIAL_8N1, PIN_RS485_RX, PIN_RS485_TX);
  RS232.begin(busBaud, SERIAL_8N1, PIN_RS232_RX, PIN_RS232_TX);
  Wire.begin(PIN_SDA, PIN_SCL);

  uint64_t mac = ESP.getEfuseMac();
  snprintf(chipId, sizeof(chipId), "%04X%08X", (uint16_t)(mac>>32), (uint32_t)mac);

  String ssid = String(AP_PREFIX) + chipId;
  WiFi.mode(WIFI_AP);
  WiFi.softAP(ssid.c_str(), AP_PASSWORD);          // 192.168.4.1, no infra Wi-Fi needed
  modbusServer.begin(); rawServer.begin(); ctrlServer.begin();
  modbusServer.setNoDelay(true); rawServer.setNoDelay(true);

  takeBus();
  led(0, 0, 8);                                    // dim blue: up, no link yet
  lastLink = millis();
}

void loop() {
  // Wired-host detection: a USB-CDC host (phone) is connected. With an FT232 raw
  // path present, release RS485 so we never fight it for the bus.
  bool host = (bool)Serial;
  if (host != wiredHost) {
    wiredHost = host;
#ifdef BOX_HAS_FT232
    if (wiredHost) releaseBus(); else takeBus();
#endif
  }

  serviceControl();
  if (ownBus || activeBus=="rs232") { serviceModbusTCP(); serviceRawTCP(); }

  // periodic SENSE stream
  if (streamHz > 0) {
    uint32_t period = 1000 / streamHz;
    if (millis() - lastStream >= period) {
      lastStream = millis();
      if (ctrlClient && ctrlClient.connected()) reply(ctrlClient, senseLine());
      if ((bool)Serial) reply(Serial, senseLine());
    }
  }

  // link watchdog → release the bus to a safe state if the link goes quiet.
  if (millis() - lastLink > LINK_WATCHDOG_MS) {
    digitalWrite(PIN_RS485_DE, LOW);               // never leave the driver asserted
  }

  // status LED: green = link active recently, blue = idle
  led(0, (millis()-lastLink < 1500) ? 12 : 0, (millis()-lastLink < 1500) ? 0 : 6);
}
