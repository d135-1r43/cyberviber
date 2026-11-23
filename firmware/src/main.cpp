// cyberviber firmware: renders the daemon's session list in one of five layouts and reports
// taps. Protocol: newline-delimited JSON over UART0 at 115200 baud (spec §3.3).
#include <ArduinoJson.h>
#include <Preferences.h>
#include <mbedtls/base64.h>

#include "app.h"
#include "board.h"

static Board tft;

// ---------------------------------------------------------------- state

Session sessions[MAX_SESSIONS];
int nSessions = 0;
char focusId[64] = "";

static int64_t macNow = 0;    // daemon clock at the last message (unix seconds)
static uint32_t macNowAt = 0;  // millis() when it arrived
int32_t macTZ = 0;
uint32_t lastHeard = 0;
bool heard = false;

const Theme* T = &THEMES[0];
int curLayout = 0, curTheme = 0;
bool usbBottom = false;

int64_t nowMac() { return macNow + (int64_t)((millis() - macNowAt) / 1000); }
bool macTimeKnown() { return macNow > 0; }

// ---------------------------------------------------------------- persistence (NVS)

static void loadConfig() {
  Preferences p;
  p.begin("cv", true);
  char buf[32];
  if (p.getString("layout", buf, sizeof buf) && findLayout(buf) >= 0) curLayout = findLayout(buf);
  if (p.getString("theme", buf, sizeof buf) && findTheme(buf) >= 0) curTheme = findTheme(buf);
  brightPct = LV_CLAMP(1, p.getInt("bright", brightPct), 100);
  dimAfterMin = LV_CLAMP(0, p.getInt("dim", dimAfterMin), 1440);
  usbBottom = p.getBool("usbbot", usbBottom);
  p.end();
  T = &THEMES[curTheme];
}

void saveConfig() {
  Preferences p;
  p.begin("cv", false);
  p.putString("layout", LAYOUTS[curLayout].id);
  p.putString("theme", THEMES[curTheme].id);
  p.putInt("bright", brightPct);
  p.putInt("dim", dimAfterMin);
  p.putBool("usbbot", usbBottom);
  p.end();
}

// ---------------------------------------------------------------- serial out

void sendHello() {
  JsonDocument doc;
  doc["type"] = "hello";
  doc["device"] = "cyberviber";
  doc["fw"] = FW_VERSION;
  doc["layout"] = LAYOUTS[curLayout].id;
  doc["theme"] = THEMES[curTheme].id;
  doc["brightness"] = brightPct;
  doc["dim_after"] = dimAfterMin;
  doc["usb"] = usbBottom ? "bottom" : "top";
  serializeJson(doc, Serial);
  Serial.print('\n');
}

void sendFocus(const char* id) {
  JsonDocument doc;
  doc["type"] = "focus";
  doc["id"] = id;
  serializeJson(doc, Serial);
  Serial.print('\n');
}

// ---------------------------------------------------------------- protocol in

// copies at most n-1 bytes without cutting a UTF-8 sequence in half
static void copyUtf8(char* dst, const char* src, size_t n) {
  if (!src) src = "";
  size_t len = strlen(src);
  if (len >= n) {
    len = n - 1;
    while (len > 0 && (src[len] & 0xC0) == 0x80) len--;
  }
  memcpy(dst, src, len);
  dst[len] = 0;
}

static State parseState(const char* s) {
  if (!s) return S_IDLE;
  if (!strcmp(s, "input")) return S_INPUT;
  if (!strcmp(s, "ready")) return S_READY;
  if (!strcmp(s, "working")) return S_WORKING;
  return S_IDLE;
}

static void handleSessions(JsonDocument& doc) {
  macNow = doc["now"] | (int64_t)0;
  macNowAt = millis();
  macTZ = doc["tz"] | 0;

  static Session next[MAX_SESSIONS];
  bool newInput = false;
  int n = 0;
  for (JsonObject s : doc["sessions"].as<JsonArray>()) {
    if (n == MAX_SESSIONS) break;
    Session& d = next[n++];
    copyUtf8(d.id, s["id"], sizeof d.id);
    copyUtf8(d.name, s["name"], sizeof d.name);
    copyUtf8(d.branch, s["branch"], sizeof d.branch);
    copyUtf8(d.activity, s["activity"], sizeof d.activity);
    copyUtf8(d.terminal, s["terminal"], sizeof d.terminal);
    copyUtf8(d.mux, s["mux"], sizeof d.mux);
    copyUtf8(d.muxShort, s["mux_short"], sizeof d.muxShort);
    copyUtf8(d.muxSession, s["mux_session"], sizeof d.muxSession);
    d.state = parseState(s["state"]);
    d.since = s["since"] | (int64_t)0;
    d.tab = s["tab"] | 0;
    d.ctx = s["ctx"] | 0;
    if (d.state == S_INPUT) {
      bool was = false;
      for (int k = 0; k < nSessions; k++)
        if (sessions[k].state == S_INPUT && !strcmp(sessions[k].id, d.id)) was = true;
      newInput |= !was;
    }
  }
  if (newInput) backlightWake();
  // input → ready → working → idle, then most recent state change first
  std::stable_sort(next, next + n, [](const Session& a, const Session& b) {
    if (a.state != b.state) return a.state < b.state;
    return a.since > b.since;
  });
  memcpy(sessions, next, sizeof(Session) * n);
  nSessions = n;
  onSessions();
}

// Reads the panel back and sends it as base64 RGB565 strips (cyberviber screenshot).
static void sendScreenshot(int from, int rows) {
  constexpr int N = 4;
  static lgfx::rgb888_t rgb[W * N];
  static uint16_t px[W * N];
  static unsigned char b64[sizeof px * 4 / 3 + 8];
  lv_refr_now(nullptr);
  from = LV_CLAMP(0, from - from % N, H);
  int to = LV_MIN(H, from + rows);
  for (int y = from; y < to; y += N) {
    if (usbBottom) {  // the panel holds the picture turned by 180°
      tft.readRect(0, H - N - y, W, N, rgb);
      std::reverse(rgb, rgb + W * N);
    } else {
      tft.readRect(0, y, W, N, rgb);
    }
    for (int i = 0; i < W * N; i++)
      px[i] = board::READBACK_SHIFTED  // see board_config.h
                  ? (rgb[i].g >> 3) << 11 | (rgb[i].b >> 2) << 5 | rgb[i].r >> 3
                  : (rgb[i].r >> 3) << 11 | (rgb[i].g >> 2) << 5 | rgb[i].b >> 3;
    size_t len = 0;
    mbedtls_base64_encode(b64, sizeof b64, &len, (const unsigned char*)px, sizeof px);
    Serial.printf("{\"type\":\"shot\",\"w\":%d,\"h\":%d,\"y\":%d,\"n\":%d,\"data\":\"", W, H, y, N);
    Serial.write(b64, len);
    Serial.print("\"}\n");
  }
  Serial.print("{\"type\":\"shot_end\"}\n");
}

static void handleLine(char* line) {
  if (line[0] != '{') return;  // boot ROM output and other noise
  JsonDocument doc;
  if (deserializeJson(doc, line)) return;
  const char* type = doc["type"] | "";
  if (!strcmp(type, "sessions")) {
    heard = true;
    lastHeard = millis();
    handleSessions(doc);
  } else if (!strcmp(type, "hello")) {
    lastHeard = millis();
    sendHello();
  } else if (!strcmp(type, "config")) {
    applyConfig(findLayout(doc["layout"] | ""), findTheme(doc["theme"] | ""), false);
    applyBacklight(doc["brightness"] | -1, doc["dim_after"] | -1, false);
    const char* usb = doc["usb"] | "";
    applyUsbBottom(!strcmp(usb, "bottom") ? 1 : !strcmp(usb, "top") ? 0 : -1, false);
    sendHello();  // confirms the applied config
  } else if (!strcmp(type, "screenshot")) {
    sendScreenshot(doc["y"] | 0, doc["rows"] | H);
  } else if (!strcmp(type, "focused")) {
    if (!(doc["ok"] | false)) showFocusError(doc["id"] | "", doc["reason"] | "failed");
  }
}

static void pollSerial() {
  static char buf[8 * 1024];
  static size_t len = 0;
  static bool overflow = false;
  while (Serial.available()) {
    char c = (char)Serial.read();
    if (c == '\n') {
      if (!overflow) {
        buf[len] = 0;
        handleLine(buf);
      }
      len = 0;
      overflow = false;
    } else if (len < sizeof buf - 1) {
      buf[len++] = c;
    } else {
      overflow = true;
    }
  }
}

// ---------------------------------------------------------------- LVGL glue

// USB at the bottom: a rectangle turned by 180° is its pixel buffer reversed, placed mirrored.
static void flushCb(lv_display_t* disp, const lv_area_t* area, uint8_t* px) {
  uint32_t w = lv_area_get_width(area), h = lv_area_get_height(area);
  int32_t x = area->x1, y = area->y1;
  if (usbBottom) {
    std::reverse((uint16_t*)px, (uint16_t*)px + w * h);
    x = W - 1 - area->x2;
    y = H - 1 - area->y2;
  }
  tft.startWrite();
  tft.setAddrWindow(x, y, w, h);
  tft.writePixels((lgfx::rgb565_t*)px, w * h);
  tft.endWrite();
  lv_display_flush_ready(disp);
}

void setBacklight(uint8_t level) { tft.setBrightness(level); }

static void touchCb(lv_indev_t*, lv_indev_data_t* data) {
  static bool wasPressed = false, swallow = false;
  int32_t x, y;
  bool pressed = tft.getTouch(&x, &y);
  if (pressed && !wasPressed) {
    gestured = false;               // a new touch starts as a possible tap
    swallow = backlightWake();      // a touch on the dimmed screen only wakes it
  }
  if (pressed) backlightWake();     // holding or scrolling keeps it awake
  wasPressed = pressed;
  if (pressed && !swallow) {
    data->state = LV_INDEV_STATE_PRESSED;
    data->point.x = usbBottom ? W - 1 - x : x;
    data->point.y = usbBottom ? H - 1 - y : y;
#ifdef CV_TOUCH_LOG
    static uint32_t last;
    if (millis() - last > 150) {
      last = millis();
      lgfx::touch_point_t raw;
      tft.getTouchRaw(&raw, 1);
      Serial.printf("{\"type\":\"log\",\"raw\":[%d,%d],\"pt\":[%ld,%ld]}\n", raw.x, raw.y, (long)x, (long)y);
    }
#endif
  } else {
    data->state = LV_INDEV_STATE_RELEASED;
  }
}

static uint32_t tickCb() { return millis(); }

// BOOT button (GPIO0, active low): next layout. Only selects the boot mode during a reset,
// afterwards it is a plain input. A press on the dimmed screen only wakes it.
static constexpr int BOOT_PIN = 0;
static constexpr uint32_t DEBOUNCE_MS = 30;

static void pollButton() {
  static bool down = false;
  static uint32_t changedAt = 0;
  bool now = digitalRead(BOOT_PIN) == LOW;
  if (now == down || millis() - changedAt < DEBOUNCE_MS) return;
  down = now;
  changedAt = millis();
  if (down && !backlightWake()) cycleLayout(1);
}

// Touch calibration: LovyanGFX's four-corner routine, run once and kept in NVS.
// Holding a finger on the screen while the board starts runs it again.
static void setupTouch() {
  Preferences prefs;
  prefs.begin("cv", false);
  uint16_t cal[8];
  bool have = prefs.getBytes("tcal", cal, sizeof cal) == sizeof cal;
  int32_t x, y;
  bool held = false;
  for (int i = 0; i < 10 && !held; i++, delay(30)) held = tft.getTouch(&x, &y);
  if (!have || held) {
    uint32_t bg = tft.color888(0x1a, 0x1b, 0x26), fg = tft.color888(0xc0, 0xca, 0xf5);
    tft.fillScreen(bg);
    tft.setTextColor(fg, bg);
    tft.setTextDatum(lgfx::middle_center);
    tft.setTextSize(1);
    tft.drawString("touch calibration", W / 2, H / 2 - 10);
    tft.drawString("tap each arrow tip", W / 2, H / 2 + 6);
    while (tft.getTouch(&x, &y)) delay(20);  // wait for release
    tft.calibrateTouch(cal, tft.color888(0x7a, 0xa2, 0xf7), bg, 16);
    prefs.putBytes("tcal", cal, sizeof cal);
  }
  tft.setTouchCalibrate(cal);
  prefs.end();
}

static constexpr size_t DRAW_BUF_BYTES = W * 40 * 2;

void setup() {
  Serial.setRxBufferSize(8192);
  Serial.begin(115200);

  tft.init();
  tft.setRotation(BOARD_ROTATION);
  tft.setBrightness(255);
  tft.fillScreen(TFT_BLACK);
  setupTouch();
  loadConfig();
  backlightInit();
  pinMode(BOOT_PIN, INPUT_PULLUP);

  lv_init();
  lv_tick_set_cb(tickCb);
  lv_display_t* disp = lv_display_create(W, H);
  lv_display_set_flush_cb(disp, flushCb);
  void* drawBuf = heap_caps_malloc(DRAW_BUF_BYTES, MALLOC_CAP_DMA | MALLOC_CAP_8BIT);
  lv_display_set_buffers(disp, drawBuf, nullptr, DRAW_BUF_BYTES, LV_DISPLAY_RENDER_MODE_PARTIAL);
  lv_indev_t* indev = lv_indev_create();
  lv_indev_set_type(indev, LV_INDEV_TYPE_POINTER);
  lv_indev_set_read_cb(indev, touchCb);
  lv_indev_set_long_press_time(indev, 800);

  uiInit();
  sendHello();
}

void loop() {
  pollSerial();
  pollButton();
  lv_timer_handler();
  delay(5);
}
