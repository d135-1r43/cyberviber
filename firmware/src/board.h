// The LovyanGFX device for whichever board board_config.h selects.
#pragma once

#define LGFX_USE_V1
#include <LovyanGFX.hpp>

#include "board_config.h"

static constexpr int BOARD_ROTATION = board::ROTATION;

// Rough raw XPT2046 range; replaced by the calibration stored in NVS (see main.cpp).
#define CV_TOUCH_X_MIN 300
#define CV_TOUCH_X_MAX 3800
#define CV_TOUCH_Y_MIN 250
#define CV_TOUCH_Y_MAX 3800

class Board : public lgfx::LGFX_Device {
#if CV_PANEL_ST7789
  lgfx::Panel_ST7789 panel_;
#else
  lgfx::Panel_ILI9341 panel_;
#endif
  lgfx::Bus_SPI bus_;
  lgfx::Light_PWM light_;
  lgfx::Touch_XPT2046 touch_;

 public:
  Board() {
    {
      auto c = bus_.config();
      c.spi_host = HSPI_HOST;
      c.spi_mode = 0;
      c.freq_write = 40000000;
      c.freq_read = 16000000;
      c.spi_3wire = false;
      c.use_lock = true;
      c.dma_channel = SPI_DMA_CH_AUTO;
      c.pin_sclk = board::TFT_SCLK;
      c.pin_mosi = board::TFT_MOSI;
      c.pin_miso = board::TFT_MISO;
      c.pin_dc = board::TFT_DC;
      bus_.config(c);
      panel_.setBus(&bus_);
    }
    {
      auto c = panel_.config();
      c.pin_cs = board::TFT_CS;
      c.pin_rst = -1;  // tied to EN
      c.pin_busy = -1;
      c.panel_width = board::PANEL_W;
      c.panel_height = board::PANEL_H;
      c.offset_rotation = 0;
      c.readable = true;
      c.invert = board::PANEL_INVERT;
      c.rgb_order = board::PANEL_BGR;
      c.dlen_16bit = false;
      c.bus_shared = !board::TOUCH_OWN_BUS;
      panel_.config(c);
    }
    {
      auto c = light_.config();
      c.pin_bl = board::BL;
      c.invert = false;
      c.freq = 12000;
      c.pwm_channel = 7;
      light_.config(c);
      panel_.setLight(&light_);
    }
    {
      auto c = touch_.config();
      c.x_min = CV_TOUCH_X_MIN;
      c.x_max = CV_TOUCH_X_MAX;
      c.y_min = CV_TOUCH_Y_MIN;
      c.y_max = CV_TOUCH_Y_MAX;
      c.pin_int = board::TOUCH_IRQ;
      c.bus_shared = !board::TOUCH_OWN_BUS;
      c.offset_rotation = 0;
#ifdef CV_TOUCH_SOFT_SPI
      c.spi_host = -1;  // bit-banged, for boards where the VSPI peripheral does not reach the touch pins
#else
      c.spi_host = board::TOUCH_OWN_BUS ? VSPI_HOST : HSPI_HOST;
#endif
      c.freq = 1000000;
      c.pin_sclk = board::TOUCH_SCLK;
      c.pin_mosi = board::TOUCH_MOSI;
      c.pin_miso = board::TOUCH_MISO;
      c.pin_cs = board::TOUCH_CS;
      touch_.config(c);
      panel_.setTouch(&touch_);
    }
    setPanel(&panel_);
  }
};
