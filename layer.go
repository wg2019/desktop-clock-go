package main

import "unsafe"

// 逐像素 alpha 图层。
//
// 为什么不用色键（旧做法：背景填 (1,1,1) + SetLayeredWindowAttributes(LWA_COLORKEY)）：
// 色键只有"这个像素算透明 / 不算透明"两档，文字边缘的覆盖率无处安放，只能硬切一刀 ——
// 在亮壁纸上就是一圈毛刺，对比度越高越扎眼。
//
// 逐像素 alpha（UpdateLayeredWindow + 32 位 DIB）把每个像素的覆盖率变成真正的不透明度，
// 边缘自然过渡，同时能顺手乘上一个整体不透明度。
//
// 难点在于 GDI 只画颜色、从不写 alpha 通道。所以这里用"覆盖率遮罩"的老办法：
//  1. 在位图上用**白色**画字、底色留**纯黑** —— GDI 的灰度抗锯齿把覆盖率写进 RGB，
//     白色被黑色按覆盖率稀释后仍是灰色，灰度值就是覆盖率本身；
//  2. 逐像素把灰度乘上文字色和不透明度，写成预乘 alpha 的 BGRA；
//  3. 整块交给 UpdateLayeredWindow 合成。

// dibSurface 是一张与窗口同尺寸的 32 位 DIB，外加一个内存 DC。
//
// 尺寸随字体/12 小时制变化，所以按需重建；像素缓冲由系统分配（不在 Go 堆上）。
type dibSurface struct {
	hdc  uintptr
	hbmp uintptr
	old  uintptr // 建 DC 时选入的默认位图：删除前必须换回去，否则 DeleteObject 失败
	bits uintptr // 像素首地址。用 uintptr 而非 unsafe.Pointer 是为了不让 GC 去扫这块外部内存
	w, h int
}

// ensure 保证缓冲区与 (w,h) 匹配，返回是否可用。
// 尺寸没变就什么都不做 —— 每秒重画一次，重建 DIB 没必要。
func (s *dibSurface) ensure(w, h int) bool {
	if s.hdc != 0 && s.w == w && s.h == h {
		return true
	}
	s.release()

	hdc, _, _ := pCreateCompatibleDC.Call(0)
	if hdc == 0 {
		return false
	}

	var bi BITMAPINFOHEADER
	bi.BiSize = uint32(unsafe.Sizeof(bi))
	bi.BiWidth = int32(w)
	// 高度取负 = 自上而下存行，和 []byte 的行序一致；取正会得到上下颠倒的图
	bi.BiHeight = -int32(h)
	bi.BiPlanes = 1
	bi.BiBitCount = 32
	bi.BiCompression = BI_RGB

	var bits uintptr
	hbmp, _, _ := pCreateDIBSection.Call(hdc,
		uintptr(unsafe.Pointer(&bi)),
		DIB_RGB_COLORS,
		uintptr(unsafe.Pointer(&bits)),
		0, 0)
	if hbmp == 0 || bits == 0 {
		pDeleteDC.Call(hdc)
		return false
	}
	old, _, _ := pSelectObject.Call(hdc, hbmp)

	s.hdc, s.hbmp, s.old, s.bits, s.w, s.h = hdc, hbmp, old, bits, w, h
	return true
}

func (s *dibSurface) release() {
	if s.hdc == 0 {
		return
	}
	if s.old != 0 {
		pSelectObject.Call(s.hdc, s.old)
	}
	pDeleteObject.Call(s.hbmp)
	pDeleteDC.Call(s.hdc)
	*s = dibSurface{}
}

// pixels 返回整块像素缓冲，按 BGRA 排列、长度为 w*h*4。
//
// bits 是 CreateDIBSection 交出来的系统内存地址，不是 Go 堆对象：GC 不管理它，
// 生命周期由 hbmp 决定（release 之前一直有效）。从 uintptr 转成指针这一步
// go vet 的 unsafeptr 检查会报一条 —— 和 onWindowPosChanging 里那条同理，
// 来源是系统而不是 Go 的算术运算，属于必要转换。
func (s *dibSurface) pixels() []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(s.bits)), s.w*s.h*4)
}

// render 重画整幅并提交给窗口。
//
// 分层窗口的内容不经过 WM_PAINT：谁改了配置谁调它一次，比 InvalidateRect 更直接。
func (c *Clock) render() {
	if c.hwnd == 0 || c.w <= 0 || c.h <= 0 {
		return
	}
	if !c.surface.ensure(c.w, c.h) {
		return
	}
	px := c.surface.pixels()
	// 全 0 = 全透明：alpha 为 0，且预乘后的 RGB 也必须是 0
	clear(px)

	// 白字画在黑底上，灰度即覆盖率。抗锯齿走灰度（字体质量见 logFont），
	// 不能用 ClearType —— 它靠 RGB 三个通道错位做次像素渲染，转成 alpha 会留下彩边。
	oldFont, _, _ := pSelectObject.Call(c.surface.hdc, c.hfont)
	pSetBkMode.Call(c.surface.hdc, BKMODE_TRANSPARENT)
	pSetTextColor.Call(c.surface.hdc, 0xFFFFFF)

	var rc RECT
	rc.Right = int32(c.surface.w)
	rc.Bottom = int32(c.surface.h)
	text := c.timeStr()
	pDrawTextW.Call(c.surface.hdc,
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		^uintptr(0), // cchText = -1：以 NUL 结尾自动计长
		uintptr(unsafe.Pointer(&rc)),
		DT_CENTER|DT_VCENTER|DT_SINGLELINE)

	pSelectObject.Call(c.surface.hdc, oldFont) // 还原 DC 默认字体，避免 GDI 状态残留

	// 覆盖率 → 预乘 alpha。中文/数字边缘的灰阶都在这一步变成透明度，
	// 所以这里逐像素处理、不能省：GDI 写进 DIB 的 alpha 字节始终是 0。
	r, g, b := parseColor(c.cfg.Color)
	op := clampOpacity(c.cfg.Opacity)
	for i := 0; i < len(px); i += 4 {
		// 白字三通道相等，取最大值即覆盖率（GDI 可能因色彩管理留下微小差异）
		cov := px[i]
		if px[i+1] > cov {
			cov = px[i+1]
		}
		if px[i+2] > cov {
			cov = px[i+2]
		}
		if cov == 0 {
			continue // 已经全 0，不会更透明
		}
		R, G, B, A := premultipliedPixel(cov, r, g, b, op)
		px[i], px[i+1], px[i+2], px[i+3] = B, G, R, A // DIB 内存序是 BGRA
	}

	var blend BLENDFUNCTION
	blend.BlendOp = AC_SRC_OVER
	blend.SourceConstantAlpha = 255 // 不透明度已经烘进每像素的 alpha，这里不再打折
	blend.AlphaFormat = AC_SRC_ALPHA

	var size SIZE
	size.Cx = int32(c.surface.w)
	size.Cy = int32(c.surface.h)
	var src POINT // 0,0：源位图从左上角开始取

	// pptDst 传 0 = 不改窗口位置：位置由 resizeToText / 拖动时的 SetWindowPos 负责。
	// 每次重画都顺手摆一次位置的话，系统会在内部再走一遍 SetWindowPos，
	// 等于每秒平白多一次 z 序重排（桌面层对 z 序很敏感）。
	pUpdateLayeredWindow.Call(c.hwnd,
		0, // hdcDst：0 = 屏幕 DC
		0, // pptDst：位置不变
		uintptr(unsafe.Pointer(&size)),
		c.surface.hdc,
		uintptr(unsafe.Pointer(&src)),
		0, // crKey：不用色键，这个参数无意义
		uintptr(unsafe.Pointer(&blend)),
		ULW_ALPHA)
}

// premultipliedPixel 把「覆盖率」算成一个预乘 alpha 的像素。
//
// cov 是白色文字盖在黑色底上留下的灰度（0 = 这个像素没被笔画碰到，255 = 笔画正中），
// 也就是字形在这个像素上的覆盖率。分层窗口要求 RGB 已按 alpha 预乘：直接把
// 文字原色写进去，边缘会因为颜色比应有的更饱和而出现一圈亮边。
func premultipliedPixel(cov byte, r, g, b, opacity int) (R, G, B, A byte) {
	// +50 是四舍五入：否则低不透明度下整幅会系统性偏暗一档
	a := (int(cov)*opacity + 50) / 100
	return byte(r * a / 255), byte(g * a / 255), byte(b * a / 255), byte(a)
}
