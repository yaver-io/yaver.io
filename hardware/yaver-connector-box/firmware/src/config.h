// Yaver Connector Box — pin map & constants.
// Pins match hardware/yaver-connector-box/schematic.md (Block E, ESP32-S3).
#pragma once
#include <Arduino.h>

// ── RS485 (UART1, isolated ADM2587E via A/B-swap mux) ──
static const int PIN_RS485_TX = 17;  // U1_TX
static const int PIN_RS485_RX = 18;  // U1_RX
static const int PIN_RS485_DE = 16;  // U1_DE (driver enable; tied DE=/RE, half-duplex)

// ── RS232 (UART2, isolated ADM3251E) ──
static const int PIN_RS232_TX = 43;  // U2_TX
static const int PIN_RS232_RX = 44;  // U2_RX

// ── I2C (TPS65987D PD telemetry, INA219 input monitor, sensors) ──
static const int PIN_SDA = 8;
static const int PIN_SCL = 9;

// ── bus options ──
static const int PIN_AB_SEL  = 10;   // RS485 A/B polarity swap (drives TS3A24159)
static const int PIN_TERM_EN = 11;   // 120R termination enable
static const int PIN_BIAS_EN = 12;   // fail-safe bias enable

// ── CAN (optional, SN65HVD230) ──
static const int PIN_CAN_TX = 13;
static const int PIN_CAN_RX = 14;

// ── companion force/torque (optional HX711) ──
static const int PIN_HX711_DT  = 5;
static const int PIN_HX711_SCK = 6;

// ── status / controls ──
static const int PIN_LED_RGB = 48;   // WS2812 (neopixelWrite); built-in on many S3 devkits
static const int PIN_BOOT    = 0;    // BOOT button / mode hint

// ── I2C addresses ──
static const uint8_t INA219_ADDR = 0x40;

// ── network (wireless mode SoftAP) ──
static const char*    AP_PREFIX     = "Yaver-Box-";
static const char*    AP_PASSWORD   = "yaver-connect"; // overridden by the QR-label cred at provisioning
static const uint16_t PORT_MODBUSTCP = 502;
static const uint16_t PORT_RAWTCP    = 9000;
static const uint16_t PORT_CONTROL   = 8347;  // text control (mirrors the phone HTTP port convention)

// ── timing ──
static const uint32_t RS485_RX_TIMEOUT_MS = 300;  // gateway wait for a slave reply
static const uint32_t LINK_WATCHDOG_MS    = 4000; // no link traffic → release the bus (safe state)
static const uint32_t DEFAULT_BAUD        = 9600;

// ── firmware identity ──
static const char* FW_VERSION = "yaver-box-fw 0.1.0";
