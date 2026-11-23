// Compile-time check of the per-board values. Compiling is the test:
//   c++ -std=c++17 -fsyntax-only -I firmware/src firmware/hostcheck/board_config_check.cpp
//   c++ -std=c++17 -fsyntax-only -DCV_BOARD_CYD28R -I firmware/src firmware/hostcheck/board_config_check.cpp
#include "board_config.h"

static_assert(board::TFT_SCLK == 14 && board::TFT_MOSI == 13 && board::TFT_MISO == 12, "TFT bus");
static_assert(board::TFT_DC == 2 && board::TFT_CS == 15, "TFT control pins");
static_assert(board::TOUCH_CS == 33 && board::TOUCH_IRQ == 36, "touch CS and IRQ");

#if defined(CV_BOARD_CYD28R)
static_assert(board::BL == 21, "2.8\": backlight on GPIO 21");
static_assert(board::TOUCH_OWN_BUS, "2.8\": XPT2046 has its own pins");
static_assert(board::TOUCH_SCLK == 25 && board::TOUCH_MOSI == 32 && board::TOUCH_MISO == 39, "2.8\": touch pins");
static_assert(board::PANEL_W == 240 && board::PANEL_H == 320, "2.8\": portrait panel memory");
#else
static_assert(board::BL == 27, "2.4\": backlight on GPIO 27");
static_assert(!board::TOUCH_OWN_BUS, "2.4\": touch shares HSPI");
static_assert(board::TOUCH_SCLK == 14 && board::TOUCH_MOSI == 13 && board::TOUCH_MISO == 12, "2.4\": touch pins");
static_assert(board::PANEL_W == 320 && board::PANEL_H == 240, "2.4\": landscape panel memory");
static_assert(board::ROTATION == 5, "2.4\": rotation 5");
static_assert(board::PANEL_BGR && !board::PANEL_INVERT && board::READBACK_SHIFTED, "2.4\": panel settings");
static_assert(CV_PANEL_ST7789 == 0, "2.4\": ILI9341");
#endif

int main() { return 0; }
