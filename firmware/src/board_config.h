// Per-board pins and panel settings, kept free of LovyanGFX so the host can check them
// (firmware/hostcheck/board_config_check.cpp). The 2.4" ESP32-2432S024R is the default;
// -DCV_BOARD_CYD28R selects the 2.8" ESP32-2432S028R.
#pragma once

namespace board {

constexpr int TFT_SCLK = 14, TFT_MOSI = 13, TFT_MISO = 12, TFT_DC = 2, TFT_CS = 15;
constexpr int TOUCH_CS = 33, TOUCH_IRQ = 36;

#if defined(CV_BOARD_CYD28R)
// ESP32-2432S028R: TFT on HSPI, XPT2046 touch on its own pins (VSPI), backlight on GPIO 21.
// The panel's memory is 240×320 (portrait). Rotation, colour order, inversion and read-back are
// the values found on the unit in Task 3 of docs/superpowers/plans/2026-11-23-cyd28r-board-support.md.
#ifndef CV_ROTATION
#define CV_ROTATION 0
#endif
#ifndef CV_PANEL_BGR
#define CV_PANEL_BGR 1
#endif
#ifndef CV_PANEL_INVERT
#define CV_PANEL_INVERT 0
#endif
#ifndef CV_READBACK_SHIFTED
#define CV_READBACK_SHIFTED 0
#endif
constexpr int BL = 21;
constexpr bool TOUCH_OWN_BUS = true;
constexpr int TOUCH_SCLK = 25, TOUCH_MOSI = 32, TOUCH_MISO = 39;
constexpr int PANEL_W = 240, PANEL_H = 320;
#else
// ESP32-2432S024R: TFT and XPT2046 touch share the HSPI bus.
// Verified on the unit: the controller's memory is 320×240 (landscape) with BGR colour order;
// rotation 5 shows it upright as 240×320 portrait with the USB port at the top. Rotation 7 (the
// same turned by 180°) garbles the picture on this panel, so "USB at the bottom" stays in rotation 5
// and turns pixels and touch in software (main.cpp).
#ifndef CV_ROTATION
#define CV_ROTATION 5
#endif
#ifndef CV_PANEL_BGR
#define CV_PANEL_BGR 1
#endif
#ifndef CV_PANEL_INVERT
#define CV_PANEL_INVERT 0
#endif
#ifndef CV_READBACK_SHIFTED
#define CV_READBACK_SHIFTED 1  // read-back arrives as (b, r, g) instead of (r, g, b)
#endif
constexpr int BL = 27;
constexpr bool TOUCH_OWN_BUS = false;
constexpr int TOUCH_SCLK = 14, TOUCH_MOSI = 13, TOUCH_MISO = 12;
constexpr int PANEL_W = 320, PANEL_H = 240;
#endif

#ifndef CV_PANEL_ST7789
#define CV_PANEL_ST7789 0  // ILI9341 unless the board branch above says otherwise
#endif

constexpr int ROTATION = CV_ROTATION;
constexpr bool PANEL_BGR = CV_PANEL_BGR, PANEL_INVERT = CV_PANEL_INVERT;
constexpr bool READBACK_SHIFTED = CV_READBACK_SHIFTED;

}  // namespace board
