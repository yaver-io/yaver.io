#include <Arduino.h>
#include <ArduinoJson.h>
#include <Preferences.h>
#include <Update.h>
#include <USB.h>
#include <USBHIDKeyboard.h>
#include <USBHIDMouse.h>
#include <WebServer.h>
#include <WiFi.h>
#include <WiFiUdp.h>
#include <esp_system.h>
#include <mbedtls/md.h>
#include <mbedtls/sha256.h>

#ifndef YAVER_KVM_VERSION
#define YAVER_KVM_VERSION "dev"
#endif

// M5Stack AtomS3U: physical button=GPIO41, RGB data=GPIO35. The released
// firmware intentionally targets AtomS3U rather than the nonexistent
// "M5StickS3" named in the original design note. AtomS3U has native USB OTG,
// so it can enumerate as a real keyboard+mouse without a second MCU.
static constexpr uint8_t kArmButton = 41;
static constexpr uint16_t kPort = 8348;
static constexpr uint16_t kBeaconPort = 19838;
static constexpr uint32_t kArmMs = 60000;
static constexpr uint32_t kLeaseMaxMs = 10000;
static constexpr uint32_t kHeartbeatGraceMs = 1500;
static constexpr size_t kMaxTextBytes = 256;

USBHIDKeyboard Keyboard;
USBHIDMouse Mouse;

// USBHIDMouse is relative-only. Remote video controls produce absolute pixel
// coordinates, so expose a second standards-compliant absolute pointer report
// rather than guessing from the host's current cursor position or acceleration.
class AbsoluteMouse : public USBHIDDevice {
 public:
  AbsoluteMouse() {
    static bool registered = false;
    if (!registered) {
      registered = true;
      USBHID::addDevice(this, sizeof(reportDescriptor));
    }
  }
  void begin() { hid.begin(); }
  void release() { send(0, x, y, 0); }
  void moveTo(uint16_t nextX, uint16_t nextY) {
    x = nextX;
    y = nextY;
    send(0, x, y, 0);
  }
  void click(uint8_t buttons) {
    send(buttons, x, y, 0);
    send(0, x, y, 0);
  }
  void dragTo(uint16_t startX, uint16_t startY, uint16_t endX, uint16_t endY, uint16_t durationMs) {
    moveTo(startX, startY);
    send(MOUSE_LEFT, startX, startY, 0);
    const uint8_t steps = constrain(durationMs / 16, 2, 60);
    for (uint8_t i = 1; i <= steps; ++i) {
      x = startX + (static_cast<int32_t>(endX) - startX) * i / steps;
      y = startY + (static_cast<int32_t>(endY) - startY) * i / steps;
      send(MOUSE_LEFT, x, y, 0);
      delay(16);
    }
    send(0, x, y, 0);
  }
  uint16_t _onGetDescriptor(uint8_t *buffer) override {
    memcpy(buffer, reportDescriptor, sizeof(reportDescriptor));
    return sizeof(reportDescriptor);
  }

 private:
  struct __attribute__((packed)) Report {
    uint8_t buttons;
    uint16_t x;
    uint16_t y;
    int8_t wheel;
  };
  static constexpr uint8_t kReportID = 8;
  static const uint8_t reportDescriptor[65];
  USBHID hid;
  uint16_t x = 0;
  uint16_t y = 0;
  void send(uint8_t buttons, uint16_t px, uint16_t py, int8_t wheel) {
    const Report report{buttons, px, py, wheel};
    hid.SendReport(kReportID, &report, sizeof(report));
  }
};

const uint8_t AbsoluteMouse::reportDescriptor[65] = {
    0x05, 0x01,        // Usage Page (Generic Desktop)
    0x09, 0x02,        // Usage (Mouse)
    0xA1, 0x01,        // Collection (Application)
    0x85, 0x08,        // Report ID (8)
    0x09, 0x01,        // Usage (Pointer)
    0xA1, 0x00,        // Collection (Physical)
    0x05, 0x09,        // Usage Page (Buttons)
    0x19, 0x01, 0x29, 0x05,
    0x15, 0x00, 0x25, 0x01,
    0x95, 0x05, 0x75, 0x01,
    0x81, 0x02,        // 5 button bits
    0x95, 0x01, 0x75, 0x03,
    0x81, 0x03,        // padding
    0x05, 0x01,
    0x09, 0x30, 0x09, 0x31,  // X, Y
    0x15, 0x00,
    0x26, 0xFF, 0x7F,  // logical max 32767
    0x75, 0x10, 0x95, 0x02,
    0x81, 0x02,        // absolute data
    0x09, 0x38,        // Wheel
    0x15, 0x81, 0x25, 0x7F,
    0x75, 0x08, 0x95, 0x01,
    0x81, 0x06,        // relative data
    0xC0, 0xC0};

AbsoluteMouse Absolute;
WebServer server(kPort);
WiFiUDP beacon;
Preferences prefs;

String deviceId;
String bearerToken;
String wifiSSID;
String wifiPassword;
String leaseId;
uint32_t armedUntil = 0;
uint32_t leaseUntil = 0;
uint32_t lastSequence = 0;
bool usbMounted = false;
bool buttonWasDown = false;
bool controllerPaired = false;
String pairedControllerId;
String authNonce;
uint32_t authNonceUntil = 0;
bool otaOK = false;
String otaError;
mbedtls_sha256_context otaSHA;

static bool beforeDeadline(uint32_t deadline) {
  return deadline != 0 && static_cast<int32_t>(deadline - millis()) > 0;
}

static String randomHex(size_t bytes) {
  static const char *hex = "0123456789abcdef";
  String out;
  out.reserve(bytes * 2);
  for (size_t i = 0; i < bytes; ++i) {
    uint8_t value = static_cast<uint8_t>(esp_random());
    out += hex[value >> 4];
    out += hex[value & 0x0f];
  }
  return out;
}

static String bytesHex(const uint8_t *bytes, size_t count) {
  static const char *hex = "0123456789abcdef";
  String out;
  out.reserve(count * 2);
  for (size_t i = 0; i < count; ++i) {
    out += hex[bytes[i] >> 4];
    out += hex[bytes[i] & 0x0f];
  }
  return out;
}

static String tokenHMAC(const String &message) {
  uint8_t digest[32];
  const mbedtls_md_info_t *info = mbedtls_md_info_from_type(MBEDTLS_MD_SHA256);
  mbedtls_md_hmac(info,
                  reinterpret_cast<const uint8_t *>(bearerToken.c_str()), bearerToken.length(),
                  reinterpret_cast<const uint8_t *>(message.c_str()), message.length(), digest);
  return bytesHex(digest, sizeof(digest));
}

static String sha256Hex(const uint8_t *data, size_t length) {
  uint8_t digest[32];
  mbedtls_sha256_ret(data, length, digest, 0);
  return bytesHex(digest, sizeof(digest));
}

static String requestMethod() {
  switch (server.method()) {
    case HTTP_GET: return "GET";
    case HTTP_POST: return "POST";
    case HTTP_DELETE: return "DELETE";
    default: return "OTHER";
  }
}

static bool constantTimeEqual(const String &a, const String &b) {
  if (a.length() != b.length()) return false;
  uint8_t diff = 0;
  for (size_t i = 0; i < a.length(); ++i) diff |= static_cast<uint8_t>(a[i] ^ b[i]);
  return diff == 0;
}

static void releaseAll() {
  Keyboard.releaseAll();
  Mouse.release(MOUSE_ALL);
  Absolute.release();
}

static void closeLease() {
  releaseAll();
  leaseId = "";
  leaseUntil = 0;
  lastSequence = 0;
}

static void reply(int status, JsonDocument &doc) {
  String body;
  serializeJson(doc, body);
  server.send(status, "application/json", body);
}

static void errorReply(int status, const char *code, const char *message, const char *remedy) {
  JsonDocument doc;
  doc["ok"] = false;
  doc["code"] = code;
  doc["error"] = message;
  if (remedy && remedy[0]) doc["remedy"] = remedy;
  reply(status, doc);
}

static bool authenticatedForHash(const String &bodyHash) {
  if (bearerToken.isEmpty()) {
    errorReply(409, "KVM_NOT_COMMISSIONED", "The input bridge is not commissioned.", "Connect to its setup Wi-Fi and finish setup.");
    return false;
  }
  const String nonce = server.header("X-Yaver-Nonce");
  const String controllerId = server.header("X-Yaver-Controller-ID");
  const String signature = server.header("X-Yaver-Signature");
  const bool nonceOK = !authNonce.isEmpty() && beforeDeadline(authNonceUntil) && constantTimeEqual(nonce, authNonce);
  const String canonical = nonce + "\n" + controllerId + "\n" + requestMethod() + "\n" + server.uri() + "\n" + bodyHash;
  const bool signatureOK = !controllerId.isEmpty() && constantTimeEqual(signature, tokenHMAC(canonical));
  // Consume before verdict so a valid or invalid attempt can never replay it.
  authNonce = "";
  authNonceUntil = 0;
  if (!nonceOK || !signatureOK || (controllerPaired && !constantTimeEqual(controllerId, pairedControllerId))) {
    errorReply(401, "KVM_AUTH_REQUIRED", "The input bridge rejected this credential.", "Pair the bridge again from Yaver.");
    return false;
  }
  if (!controllerPaired && server.uri() != "/v1/pair") {
    errorReply(409, "KVM_PAIR_REQUIRED", "The input bridge is not paired to a Pi controller.", "Press the AtomS3U button and pair it from Yaver.");
    return false;
  }
  return true;
}

static bool authenticated() {
  const String body = server.hasArg("plain") ? server.arg("plain") : "";
  return authenticatedForHash(sha256Hex(reinterpret_cast<const uint8_t *>(body.c_str()), body.length()));
}

static bool readJSON(JsonDocument &doc) {
  if (!server.hasArg("plain") || deserializeJson(doc, server.arg("plain"))) {
    errorReply(400, "KVM_BAD_JSON", "The request body is not valid JSON.", "Retry the action.");
    return false;
  }
  return true;
}

static bool requireReadyLease(JsonDocument &doc) {
  if (!authenticated() || !readJSON(doc)) return false;
  if (!usbMounted) {
    errorReply(409, "KVM_USB_NOT_READY", "The M5Stack is not enumerated as USB HID.", "Connect AtomS3U directly to the target PC with its USB plug.");
    return false;
  }
  if (!beforeDeadline(armedUntil)) {
    closeLease();
    errorReply(423, "KVM_LOCAL_ARM_REQUIRED", "Remote input is locally locked.", "Press the AtomS3U button, then retry within 60 seconds.");
    return false;
  }
  if (leaseId.isEmpty() || !beforeDeadline(leaseUntil) || String(doc["leaseId"] | "") != leaseId) {
    closeLease();
    errorReply(409, "KVM_LEASE_REQUIRED", "The input lease is missing or expired.", "Acquire a new control lease.");
    return false;
  }
  uint32_t seq = doc["sequence"] | 0;
  if (seq == 0 || seq <= lastSequence) {
    errorReply(409, "KVM_SEQUENCE_REJECTED", "This input action is stale or duplicated.", "Send the next sequence number from the active lease.");
    return false;
  }
  lastSequence = seq;
  leaseUntil = millis() + kLeaseMaxMs;
  return true;
}

static uint8_t namedKey(const String &name) {
  if (name == "ctrl" || name == "control") return KEY_LEFT_CTRL;
  if (name == "shift") return KEY_LEFT_SHIFT;
  if (name == "alt" || name == "option") return KEY_LEFT_ALT;
  if (name == "gui" || name == "meta" || name == "cmd" || name == "command" || name == "win" || name == "windows") return KEY_LEFT_GUI;
  if (name == "enter" || name == "return") return KEY_RETURN;
  if (name == "escape" || name == "esc") return KEY_ESC;
  if (name == "tab") return KEY_TAB;
  if (name == "backspace") return KEY_BACKSPACE;
  if (name == "delete") return KEY_DELETE;
  if (name == "up") return KEY_UP_ARROW;
  if (name == "down") return KEY_DOWN_ARROW;
  if (name == "left") return KEY_LEFT_ARROW;
  if (name == "right") return KEY_RIGHT_ARROW;
  if (name == "home") return KEY_HOME;
  if (name == "end") return KEY_END;
  if (name == "pageup") return KEY_PAGE_UP;
  if (name == "pagedown") return KEY_PAGE_DOWN;
  if (name.length() == 1) return static_cast<uint8_t>(name[0]);
  return 0;
}

static bool sendKeyChord(String chord) {
  chord.trim();
  chord.toLowerCase();
  if (chord.isEmpty() || chord.length() > 64) return false;
  if (chord.indexOf('+') < 0) {
    const uint8_t key = namedKey(chord);
    if (!key) return false;
    Keyboard.write(key);
    return true;
  }
  uint8_t pressed = 0;
  int start = 0;
  while (start < chord.length() && pressed < 6) {
    int end = chord.indexOf('+', start);
    if (end < 0) end = chord.length();
    String token = chord.substring(start, end);
    token.trim();
    const uint8_t key = namedKey(token);
    if (!key) {
      Keyboard.releaseAll();
      return false;
    }
    Keyboard.press(key);
    pressed++;
    start = end + 1;
  }
  if (start < chord.length() || pressed < 2) {
    Keyboard.releaseAll();
    return false;
  }
  delay(12);
  Keyboard.releaseAll();
  return true;
}

static bool supportedUSBText(const String &value) {
  for (size_t i = 0; i < value.length(); ++i) {
    const uint8_t byte = static_cast<uint8_t>(value[i]);
    if (byte > 0x7e || (byte < 0x20 && byte != '\n' && byte != '\t')) return false;
  }
  return true;
}

static void writeStatus() {
  JsonDocument doc;
  doc["ok"] = true;
  doc["protocol"] = "yaver-physical-kvm-v1";
  doc["version"] = YAVER_KVM_VERSION;
  doc["deviceId"] = deviceId;
	doc["mode"] = "keyboard";
	doc["pairingState"] = controllerPaired ? "paired" : "unpaired";
	doc["keyboardLayout"] = "us";
  doc["ip"] = WiFi.localIP().toString();
  doc["usbReady"] = usbMounted;
  doc["armed"] = beforeDeadline(armedUntil);
  doc["leaseActive"] = !leaseId.isEmpty() && beforeDeadline(leaseUntil);
  doc["leaseRemainingMs"] = beforeDeadline(leaseUntil) ? leaseUntil - millis() : 0;
  reply(200, doc);
}

static void handleStatus() {
	if (!authenticated()) return;
	writeStatus();
}

static void handlePair() {
	if (!authenticated()) return;
	if (!beforeDeadline(armedUntil)) {
		errorReply(423, "KVM_LOCAL_ARM_REQUIRED", "Pairing is locally locked.", "Press the AtomS3U button, then retry within 60 seconds.");
		return;
	}
	JsonDocument in;
	if (!readJSON(in)) return;
	const String controllerId = String(in["controllerId"] | "");
	if (controllerId.isEmpty() || controllerId.length() > 128 || controllerId != server.header("X-Yaver-Controller-ID")) {
		errorReply(400, "KVM_CONTROLLER_ID_INVALID", "The Pi controller identity is missing or invalid.", "Enroll the Pi, then retry pairing.");
		return;
	}
	prefs.begin("yaver-kvm", false);
	prefs.putBool("paired", true);
	prefs.putString("controller", controllerId);
	prefs.end();
	controllerPaired = true;
	pairedControllerId = controllerId;
	writeStatus();
}

static void handleUnpair() {
	if (!authenticated()) return;
	if (!beforeDeadline(armedUntil)) {
		errorReply(423, "KVM_LOCAL_ARM_REQUIRED", "Unpairing is locally locked.", "Press the AtomS3U button, then retry within 60 seconds.");
		return;
	}
	closeLease();
	prefs.begin("yaver-kvm", false);
	prefs.remove("paired");
	prefs.remove("controller");
	prefs.end();
	controllerPaired = false;
	pairedControllerId = "";
	writeStatus();
}

static void handleChallenge() {
  authNonce = randomHex(24);
  authNonceUntil = millis() + 5000;
  JsonDocument out;
  out["ok"] = true;
  out["nonce"] = authNonce;
  out["expiresInMs"] = 5000;
  reply(200, out);
}

static void handleOpen() {
  if (!authenticated()) return;
  JsonDocument in;
  if (!readJSON(in)) return;
  if (!usbMounted) {
    errorReply(409, "KVM_USB_NOT_READY", "The M5Stack is not enumerated as USB HID.", "Connect AtomS3U directly to the target PC.");
    return;
  }
  if (!beforeDeadline(armedUntil)) {
    errorReply(423, "KVM_LOCAL_ARM_REQUIRED", "Remote input is locally locked.", "Press the AtomS3U button, then retry within 60 seconds.");
    return;
  }
  closeLease();
  leaseId = randomHex(16);
  leaseUntil = millis() + kLeaseMaxMs;
  JsonDocument out;
  out["ok"] = true;
  out["leaseId"] = leaseId;
  out["nonce"] = String(in["nonce"] | "");
  out["expiresInMs"] = kLeaseMaxMs;
  reply(200, out);
}

static void handleHeartbeat() {
  if (!authenticated()) return;
  JsonDocument in;
  if (!readJSON(in)) return;
  if (leaseId.isEmpty() || String(in["leaseId"] | "") != leaseId || !beforeDeadline(armedUntil)) {
    closeLease();
    errorReply(409, "KVM_LEASE_REQUIRED", "The input lease is missing or expired.", "Acquire a new control lease.");
    return;
  }
  leaseUntil = millis() + kLeaseMaxMs;
  JsonDocument out;
  out["ok"] = true;
  out["expiresInMs"] = kLeaseMaxMs;
  reply(200, out);
}

static void handleClose() {
  if (!authenticated()) return;
  closeLease();
  JsonDocument out;
  out["ok"] = true;
  reply(200, out);
}

static void handleReleaseAll() {
  if (!authenticated()) return;
  closeLease();
  JsonDocument out;
  out["ok"] = true;
  reply(200, out);
}

static void handleAction() {
  JsonDocument in;
  if (!requireReadyLease(in)) return;
  const String kind = String(in["kind"] | "");
  if (kind == "text") {
    const String value = String(in["text"] | "");
    if (value.length() == 0 || value.length() > kMaxTextBytes) {
      errorReply(400, "KVM_TEXT_BOUNDS", "Text input must be 1-256 bytes.", "Send shorter text chunks.");
      return;
    }
    if (!supportedUSBText(value)) {
      errorReply(400, "KVM_TEXT_LAYOUT_UNSUPPORTED", "USB text supports US-layout printable ASCII, tab, and newline in v0.", "Use the target PC's paste/input method for Unicode text.");
      return;
    }
    Keyboard.print(value);
  } else if (kind == "key") {
    if (!sendKeyChord(String(in["key"] | ""))) {
      errorReply(400, "KVM_KEY_UNSUPPORTED", "That key name is not supported.", "Use a printable character or a named navigation key.");
      return;
    }
  } else if (kind == "move") {
    int dx = constrain(in["dx"] | 0, -127, 127);
    int dy = constrain(in["dy"] | 0, -127, 127);
    Mouse.move(dx, dy, 0);
  } else if (kind == "click") {
    const String button = String(in["button"] | "left");
    uint8_t value = button == "right" ? MOUSE_RIGHT : button == "middle" ? MOUSE_MIDDLE : MOUSE_LEFT;
    Mouse.click(value);
  } else if (kind == "tap") {
    const int width = in["width"] | 0;
    const int height = in["height"] | 0;
    const int px = in["x"] | -1;
    const int py = in["y"] | -1;
    if (width <= 0 || height <= 0 || px < 0 || py < 0 || px >= width || py >= height) {
      errorReply(400, "KVM_COORDINATES_INVALID", "Tap coordinates are outside the captured frame.", "Refresh the frame geometry and retry.");
      return;
    }
    const uint16_t ax = static_cast<uint32_t>(px) * 32767U / static_cast<uint32_t>(width - 1 > 0 ? width - 1 : 1);
    const uint16_t ay = static_cast<uint32_t>(py) * 32767U / static_cast<uint32_t>(height - 1 > 0 ? height - 1 : 1);
    Absolute.moveTo(ax, ay);
    Absolute.click(MOUSE_LEFT);
  } else if (kind == "drag") {
    const int width = in["width"] | 0;
    const int height = in["height"] | 0;
    const int x1 = in["x1"] | -1;
    const int y1 = in["y1"] | -1;
    const int x2 = in["x2"] | -1;
    const int y2 = in["y2"] | -1;
    if (width <= 0 || height <= 0 || x1 < 0 || y1 < 0 || x2 < 0 || y2 < 0 ||
        x1 >= width || x2 >= width || y1 >= height || y2 >= height) {
      errorReply(400, "KVM_COORDINATES_INVALID", "Drag coordinates are outside the captured frame.", "Refresh the frame geometry and retry.");
      return;
    }
    auto scaleX = [width](int value) -> uint16_t { return static_cast<uint32_t>(value) * 32767U / static_cast<uint32_t>(width - 1 > 0 ? width - 1 : 1); };
    auto scaleY = [height](int value) -> uint16_t { return static_cast<uint32_t>(value) * 32767U / static_cast<uint32_t>(height - 1 > 0 ? height - 1 : 1); };
    Absolute.dragTo(scaleX(x1), scaleY(y1), scaleX(x2), scaleY(y2), constrain(in["durationMs"] | 250, 32, 2000));
  } else if (kind == "wheel") {
    Mouse.move(0, 0, constrain(in["delta"] | 0, -127, 127));
  } else {
    errorReply(400, "KVM_ACTION_UNSUPPORTED", "That input action is not supported.", "Use text, key, move, click, or wheel.");
    return;
  }
  JsonDocument out;
  out["ok"] = true;
  out["sequence"] = lastSequence;
  reply(200, out);
}

static void saveCommissioning(const String &ssid, const String &password, const String &token) {
  prefs.begin("yaver-kvm", false);
  prefs.putString("ssid", ssid);
  prefs.putString("password", password);
  prefs.putString("token", token);
  prefs.remove("paired");
  prefs.remove("controller");
  prefs.end();
}

static void handleCommission() {
  // Commissioning is reachable only before a credential exists. Recommission
  // requires a physical 8-second button hold, which erases Wi-Fi and token.
  if (!bearerToken.isEmpty()) {
    errorReply(403, "KVM_ALREADY_COMMISSIONED", "This bridge is already paired.", "Hold its button for 8 seconds to reset pairing.");
    return;
  }
	if (!beforeDeadline(armedUntil)) {
	  errorReply(423, "KVM_LOCAL_ARM_REQUIRED", "Commissioning is locally locked.", "Press the AtomS3U button, then retry within 60 seconds.");
	  return;
	}
  JsonDocument in;
  if (!readJSON(in)) return;
  const String ssid = String(in["ssid"] | "");
  const String password = String(in["password"] | "");
  if (ssid.isEmpty() || ssid.length() > 32 || password.length() < 8 || password.length() > 63) {
    errorReply(400, "KVM_WIFI_INVALID", "A Wi-Fi name and an 8-63 byte WPA2 password are required.", "Enter a secured 2.4 GHz Wi-Fi network.");
    return;
  }
  const String token = randomHex(32);
  saveCommissioning(ssid, password, token);
  JsonDocument out;
  out["ok"] = true;
  out["deviceId"] = deviceId;
  out["token"] = token; // shown once on the physically-local setup AP
  out["next"] = "Reconnect to your normal Wi-Fi, then pair this device in Yaver.";
  reply(200, out);
  delay(500);
  ESP.restart();
}

static void handleFirmwareUpload() {
  HTTPUpload &upload = server.upload();
  if (upload.status == UPLOAD_FILE_START) {
    otaOK = false;
    otaError = "";
    const String expectedSHA = server.header("X-Yaver-SHA256");
    const String expectedMAC = server.header("X-Yaver-HMAC");
    if (!authenticatedForHash(expectedSHA)) {
      otaError = "KVM_AUTH_REQUIRED: signed firmware request rejected";
    } else if (!beforeDeadline(armedUntil)) {
      otaError = "KVM_LOCAL_ARM_REQUIRED: press the AtomS3U button before firmware update";
    } else if (expectedSHA.length() != 64 || !constantTimeEqual(tokenHMAC(expectedSHA), expectedMAC)) {
      otaError = "KVM_FIRMWARE_AUTH_INVALID: checksum authentication failed";
    } else if (!Update.begin(UPDATE_SIZE_UNKNOWN, U_FLASH)) {
      otaError = "KVM_FIRMWARE_BEGIN_FAILED";
    } else {
      closeLease();
      mbedtls_sha256_init(&otaSHA);
      mbedtls_sha256_starts_ret(&otaSHA, 0);
    }
  } else if (upload.status == UPLOAD_FILE_WRITE && otaError.isEmpty()) {
    if (Update.write(upload.buf, upload.currentSize) != upload.currentSize) {
      otaError = "KVM_FIRMWARE_WRITE_FAILED";
      Update.abort();
    } else {
      mbedtls_sha256_update_ret(&otaSHA, upload.buf, upload.currentSize);
    }
  } else if (upload.status == UPLOAD_FILE_END && otaError.isEmpty()) {
    uint8_t digest[32];
    mbedtls_sha256_finish_ret(&otaSHA, digest);
    mbedtls_sha256_free(&otaSHA);
    if (!constantTimeEqual(bytesHex(digest, sizeof(digest)), server.header("X-Yaver-SHA256"))) {
      otaError = "KVM_FIRMWARE_CHECKSUM_MISMATCH";
      Update.abort();
    } else if (!Update.end(true)) {
      otaError = "KVM_FIRMWARE_FINALIZE_FAILED";
    } else {
      otaOK = true;
    }
  } else if (upload.status == UPLOAD_FILE_ABORTED) {
    otaError = "KVM_FIRMWARE_UPLOAD_ABORTED";
    Update.abort();
  }
}

static void handleFirmwareComplete() {
  if (!otaOK) {
    errorReply(400, "KVM_FIRMWARE_UPDATE_FAILED", otaError.c_str(), "Keep the bridge powered, arm it locally, and retry the verified Yaver OTA image.");
    return;
  }
  JsonDocument out;
  out["ok"] = true;
  out["restarting"] = true;
  reply(200, out);
  delay(400);
  ESP.restart();
}

static void startSetupAP() {
  const String suffix = deviceId.substring(deviceId.length() - 6);
  const String apName = "Yaver-KVM-" + suffix;
  const String apPassword = "yv" + suffix + "!";
  WiFi.mode(WIFI_AP);
  WiFi.softAP(apName.c_str(), apPassword.c_str());
  Serial.printf("Yaver KVM setup Wi-Fi: %s\n", apName.c_str());
  Serial.printf("Setup password: %s\n", apPassword.c_str());
  Serial.printf("POST http://192.168.4.1:%u/v1/commission with {ssid,password}\n", kPort);
}

static void loadConfig() {
  prefs.begin("yaver-kvm", true);
  wifiSSID = prefs.getString("ssid", "");
  wifiPassword = prefs.getString("password", "");
	bearerToken = prefs.getString("token", "");
	controllerPaired = prefs.getBool("paired", false);
	pairedControllerId = prefs.getString("controller", "");
	if (controllerPaired && pairedControllerId.isEmpty()) controllerPaired = false;
  prefs.end();
}

static void usbEvent(void *, esp_event_base_t, int32_t event, void *) {
  if (event == ARDUINO_USB_STARTED_EVENT || event == ARDUINO_USB_RESUME_EVENT) usbMounted = true;
  if (event == ARDUINO_USB_STOPPED_EVENT || event == ARDUINO_USB_SUSPEND_EVENT) {
    usbMounted = false;
    closeLease();
  }
}

static void sendBeacon() {
  if (WiFi.status() != WL_CONNECTED || bearerToken.isEmpty()) return;
  JsonDocument doc;
  doc["protocol"] = "yaver-physical-kvm-v1";
  doc["deviceId"] = deviceId;
  doc["address"] = WiFi.localIP().toString();
  doc["port"] = kPort;
	doc["mode"] = "keyboard";
	doc["pairingState"] = controllerPaired ? "paired" : "unpaired";
	doc["keyboardLayout"] = "us";
  doc["usbReady"] = usbMounted;
  doc["armed"] = beforeDeadline(armedUntil);
  String body;
  serializeJson(doc, body);
  beacon.beginPacket(IPAddress(255, 255, 255, 255), kBeaconPort);
  beacon.write(reinterpret_cast<const uint8_t *>(body.c_str()), body.length());
  beacon.endPacket();
}

void setup() {
  Serial.begin(115200);
  char deviceBuffer[20];
  snprintf(deviceBuffer, sizeof(deviceBuffer), "m5-%012llx", static_cast<unsigned long long>(ESP.getEfuseMac() & 0xffffffffffffULL));
  deviceId = deviceBuffer;
  pinMode(kArmButton, INPUT_PULLUP);
  USB.onEvent(usbEvent);
  Mouse.begin();
  Keyboard.begin();
  Absolute.begin();
  USB.productName("Yaver Physical KVM");
  USB.manufacturerName("Yaver");
  USB.begin();
  loadConfig();

  const char *headers[] = {"X-Yaver-Nonce", "X-Yaver-Controller-ID", "X-Yaver-Signature", "X-Yaver-SHA256", "X-Yaver-HMAC"};
  server.collectHeaders(headers, 5);
  server.on("/v1/commission", HTTP_POST, handleCommission);
  server.on("/v1/challenge", HTTP_GET, handleChallenge);
  server.on("/v1/status", HTTP_GET, handleStatus);
  server.on("/v1/pair", HTTP_POST, handlePair);
	server.on("/v1/unpair", HTTP_POST, handleUnpair);
  server.on("/v1/session/open", HTTP_POST, handleOpen);
  server.on("/v1/session/heartbeat", HTTP_POST, handleHeartbeat);
  server.on("/v1/session/close", HTTP_POST, handleClose);
  server.on("/v1/release-all", HTTP_POST, handleReleaseAll);
  server.on("/v1/action", HTTP_POST, handleAction);
  server.on("/v1/firmware", HTTP_POST, handleFirmwareComplete, handleFirmwareUpload);
  server.onNotFound([]() { errorReply(404, "KVM_ROUTE_NOT_FOUND", "Unknown bridge route.", "Update Yaver and the M5Stack firmware together."); });

  if (bearerToken.isEmpty()) {
    startSetupAP();
  } else {
    WiFi.mode(WIFI_STA);
    WiFi.begin(wifiSSID.c_str(), wifiPassword.c_str());
    const uint32_t deadline = millis() + 15000;
    while (WiFi.status() != WL_CONNECTED && beforeDeadline(deadline)) delay(100);
  }
  server.begin();
}

void loop() {
  server.handleClient();
  const bool down = digitalRead(kArmButton) == LOW;
  static uint32_t buttonDownAt = 0;
  if (down && !buttonWasDown) {
    buttonDownAt = millis();
    armedUntil = millis() + kArmMs;
  }
  if (!down && buttonWasDown) buttonDownAt = 0;
  if (down && buttonDownAt && millis() - buttonDownAt > 8000) {
    closeLease();
    prefs.begin("yaver-kvm", false);
    prefs.clear();
    prefs.end();
    delay(250);
    ESP.restart();
  }
  buttonWasDown = down;
  if (!leaseId.isEmpty() && (!beforeDeadline(leaseUntil + kHeartbeatGraceMs) || !beforeDeadline(armedUntil))) closeLease();
  static uint32_t lastBeacon = 0;
  if (millis() - lastBeacon > 2000) {
    lastBeacon = millis();
    sendBeacon();
  }
  delay(2);
}
