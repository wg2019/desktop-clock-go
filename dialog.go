package main

import "unsafe"

// chooseColor 调用系统取色器。确认后立即生效并落盘。
func (c *Clock) chooseColor() {
	c.ensureCustColors()

	var cc CHOOSECOLORW
	cc.LStructSize = uint32(unsafe.Sizeof(cc))
	cc.HwndOwner = c.hwnd
	cc.RgbResult = parseColorRef(c.cfg.Color)
	cc.LpCustColors = &c.custColors[0]
	cc.Flags = CC_RGBINIT | CC_FULLOPEN // 初始色 = 当前色；直接展开自定义颜色面板

	if r, _, _ := pChooseColorW.Call(uintptr(unsafe.Pointer(&cc))); r != 0 {
		c.cfg.Color = colorRefToHex(cc.RgbResult)
		_ = saveConfig(c.cfg)
		pInvalidateRect.Call(c.hwnd, 0, 1)
	}
}

// ensureCustColors 自定义颜色槽位初始化（首次打开对话框时的默认色板）。
func (c *Clock) ensureCustColors() {
	for _, v := range c.custColors {
		if v != 0 {
			return
		}
	}
	for i := range c.custColors {
		c.custColors[i] = 0x00FF00FF // 洋红，作为自定义颜色区的初始占位值
	}
}

// chooseFont 调用系统字体对话框。
//
// 关键点：lfItalic 必须双向同步 —— 进去时把当前状态填进 LOGFONTW，
// 回来时再把 lfItalic 读回配置，否则会出现"勾了斜体却不生效"。
func (c *Clock) chooseFont() {
	var lf LOGFONTW
	lf.LfHeight = -int32(c.cfg.FontSize)
	lf.LfWeight = 400
	if c.cfg.FontWeight == "bold" {
		lf.LfWeight = 700
	}
	if c.cfg.Italic {
		lf.LfItalic = 1
	}
	copyUTF16(lf.LfFaceName[:], c.cfg.FontFamily)

	var cf CHOOSEFONTW
	cf.LStructSize = uint32(unsafe.Sizeof(cf))
	cf.HwndOwner = c.hwnd
	cf.LpLogFont = &lf
	cf.Flags = CF_INITTOLOGFONTSTRUCT | CF_NOSCRIPTSEL

	if r, _, _ := pChooseFontW.Call(uintptr(unsafe.Pointer(&cf))); r == 0 {
		return // 用户取消
	}

	c.cfg.FontFamily = utf16ToString(lf.LfFaceName[:])

	size := int(lf.LfHeight)
	if size < 0 {
		size = -size
	}
	if size == 0 {
		size = 48
	}
	c.cfg.FontSize = size

	if lf.LfWeight >= 700 {
		c.cfg.FontWeight = "bold"
	} else {
		c.cfg.FontWeight = "normal"
	}
	c.cfg.Italic = lf.LfItalic != 0

	c.createFont()
	c.resizeToText()
	_ = saveConfig(c.cfg)
	pInvalidateRect.Call(c.hwnd, 0, 1)
}
