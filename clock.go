package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"
)

const (
	// className 是窗口类的注册名，仅在本进程内标识窗口，不对外暴露。
	// 刻意不带 "32"/"64" 字样：本程序调用的是 Win32 API —— 那是 Windows
	// 原生接口家族的名字（user32/gdi32 里的 "32" 是 16→32 位年代的遗留），
	// 64 位程序用的是同一套 API，与编译目标位数无关。
	className   = "DesktopClockWindow"
	windowTitle = "DesktopClock"

	// 透明色键：窗口背景用这个颜色填充，配合 LWA_COLORKEY 让该色变全透明。
	// 选 (1,1,1) 而不是纯黑，避免与纯黑背景混合时出现边缘黑边。
	keyR, keyG, keyB = 1, 1, 1
)

// Clock 持有全部运行期状态。单实例，由包级变量 app 引用
// （原因见 main.go：窗口过程拿不到 Go 的闭包）。
type Clock struct {
	cfg    *Config
	hwnd   uintptr
	hfont  uintptr
	hbrush uintptr
	hicon  uintptr

	w, h int

	dragging           bool
	dragOffX, dragOffY int

	nid        NOTIFYICONDATAW
	trayRetry  int // 托盘登记失败次数，成功即归零
	custColors [16]uint32
}

// newClock 构造并启动时钟窗口，只返回成败。
//
// 实例在内部挂到包级 app 上，不通过返回值暴露：窗口过程是 C 回调、拿不到闭包，
// 本来就必须走 app；而且这一步要在 CreateWindowExW 之前完成，否则首条窗口消息
// 进来时 wndProc 里 app 还是 nil。
func newClock() error {
	cfg := loadConfig()
	cfg.Autostart = getAutostart() // 以注册表实际状态为准

	c := &Clock{
		cfg:   cfg,
		hicon: loadAppIcon(),
		w:     200,
		h:     60,
	}
	c.hbrush, _, _ = pCreateSolidBrush.Call(uintptr(toColorRef(keyR, keyG, keyB)))
	app = c

	if err := c.registerClass(); err != nil {
		return err
	}
	c.createFont()
	if err := c.createWindow(); err != nil {
		return err
	}
	c.resizeToText()

	pShowWindow.Call(c.hwnd, SW_SHOW)
	pUpdateWindow.Call(c.hwnd)
	// 色键：该颜色的像素完全透明，且不接收鼠标（配合 WS_EX_TRANSPARENT 即穿透）
	pSetLayeredWindowAttrs.Call(c.hwnd, uintptr(toColorRef(keyR, keyG, keyB)), 0, LWA_COLORKEY)

	c.applyLayer()
	c.setupTray()
	pSetTimer.Call(c.hwnd, timerTick, 1000, 0) // 每秒一次：刷新时间 + 自检层级

	return nil
}

// ---------- 图标 ----------

// loadAppIcon 三级回退：exe 内嵌资源 → 同目录 icon.ico → 系统默认图标。
func loadAppIcon() uintptr {
	exe := exePath()

	// exe 已用 -ldflags -H=windowsgui + rsrc 嵌入图标时，ExtractIconW 能直接取到
	if h, _, _ := pExtractIconW.Call(0, uintptr(unsafe.Pointer(utf16Ptr(exe))), 0); h > 1 {
		return h
	}
	// 开发态：读同目录 icon.ico
	ico := filepath.Join(filepath.Dir(exe), "icon.ico")
	if _, err := os.Stat(ico); err == nil {
		if h, _, _ := pLoadImageW.Call(0, uintptr(unsafe.Pointer(utf16Ptr(ico))),
			IMAGE_ICON, 0, 0, LR_LOADFROMFILE); h != 0 {
			return h
		}
	}
	// 兜底
	h, _, _ := pLoadIconW.Call(0, uintptr(IDI_APPLICATION))
	return h
}

// ---------- 窗口类 / 创建 ----------

func (c *Clock) registerClass() error {
	hInst, _, _ := pGetModuleHandleW.Call(0)
	cursor, _, _ := pLoadCursorW.Call(0, uintptr(IDC_ARROW))

	var wc WNDCLASSEXW
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.LpfnWndProc = wndProcCb
	wc.HInstance = hInst
	wc.HCursor = cursor
	wc.HbrBackground = 6 // (HBRUSH)(COLOR_WINDOW+1)
	wc.LpszClassName = utf16Ptr(className)
	wc.HIcon = c.hicon
	wc.HIconSm = c.hicon

	atom, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return fmt.Errorf("RegisterClassExW 失败: %v", err)
	}
	return nil
}

// exStyle 组装扩展样式。
//
//	DesktopLayer=true  → NOACTIVATE(点击不抢焦点) + TOOLWINDOW(不进 Alt+Tab)
//	DesktopLayer=false → TOPMOST(传统置顶悬浮)
//	ClickThrough=true  → 追加 TRANSPARENT，点击直接穿透到下层
func (c *Clock) exStyle() uintptr {
	var ex uintptr
	if c.cfg.DesktopLayer {
		ex = WS_EX_LAYERED | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE
	} else {
		ex = WS_EX_LAYERED | WS_EX_TOPMOST | WS_EX_TOOLWINDOW
	}
	if c.cfg.ClickThrough {
		ex |= WS_EX_TRANSPARENT
	}
	return ex
}

func (c *Clock) createWindow() error {
	hInst, _, _ := pGetModuleHandleW.Call(0)
	hwnd, _, err := pCreateWindowExW.Call(
		c.exStyle(),
		uintptr(unsafe.Pointer(utf16Ptr(className))),
		uintptr(unsafe.Pointer(utf16Ptr(windowTitle))),
		WS_POPUP,
		uintptr(c.cfg.X), uintptr(c.cfg.Y),
		uintptr(c.w), uintptr(c.h),
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW 失败: %v", err)
	}
	c.hwnd = hwnd
	return nil
}

// ---------- 字体 ----------

func (c *Clock) createFont() {
	if c.hfont != 0 {
		pDeleteObject.Call(c.hfont)
	}
	var lf LOGFONTW
	lf.LfHeight = -int32(c.cfg.FontSize) // 负值 = 字符高度（不含内部行距）
	lf.LfWeight = 400                    // 400 = FW_NORMAL
	if c.cfg.FontWeight == "bold" {
		lf.LfWeight = 700 // 700 = FW_BOLD
	}
	if c.cfg.Italic {
		lf.LfItalic = 1
	}
	copyUTF16(lf.LfFaceName[:], c.cfg.FontFamily)

	h, _, _ := pCreateFontIndirectW.Call(uintptr(unsafe.Pointer(&lf)))
	c.hfont = h
}

// ---------- 尺寸自适应 ----------

// resizeToText 按当前字体下时间文本的实际像素宽度重设窗口大小，
// 这样换字体 / 切 12 小时制（多了"上午"两字）都不会截断。
func (c *Clock) resizeToText() {
	text := c.timeStr()
	// 借窗口 DC 选入当前字体，才能量到"这个字体下"的文字尺寸
	hdc, _, _ := pGetDC.Call(c.hwnd)
	pSelectObject.Call(hdc, c.hfont)

	var sz SIZE
	pGetTextExtentPoint32W.Call(hdc,
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(len([]rune(text))),
		uintptr(unsafe.Pointer(&sz)))
	pReleaseDC.Call(c.hwnd, hdc)

	// 留白：GetTextExtent 不含字形外沿的抗锯齿像素，不加会把数字边缘裁掉
	c.w = int(sz.Cx) + 16
	c.h = int(sz.Cy) + 10
	pSetWindowPos.Call(c.hwnd, 0,
		uintptr(c.cfg.X), uintptr(c.cfg.Y),
		uintptr(c.w), uintptr(c.h),
		SWP_NOZORDER)
}

// ---------- 时间 ----------

// formatTime 是纯函数，便于单测覆盖 12/24 制的边界（0 点、12 点）。
func formatTime(t time.Time, hourFormat string) string {
	if hourFormat == "12" {
		ap := "上午"
		if t.Hour() >= 12 {
			ap = "下午"
		}
		return ap + " " + t.Format("03:04:05")
	}
	return t.Format("15:04:05")
}

func (c *Clock) timeStr() string {
	return formatTime(time.Now(), c.cfg.HourFormat)
}

// ---------- 绘制 ----------

func (c *Clock) onPaint() {
	var ps PAINTSTRUCT
	hdc, _, _ := pBeginPaint.Call(c.hwnd, uintptr(unsafe.Pointer(&ps)))

	oldFont, _, _ := pSelectObject.Call(hdc, c.hfont)
	pSetBkMode.Call(hdc, BKMODE_TRANSPARENT) // 文字背景透明，露出色键底色
	pSetTextColor.Call(hdc, uintptr(parseColorRef(c.cfg.Color)))

	var rc RECT
	pGetClientRect.Call(c.hwnd, uintptr(unsafe.Pointer(&rc)))
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), c.hbrush)

	text := c.timeStr()
	pDrawTextW.Call(hdc,
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		^uintptr(0), // cchText = -1：以 NUL 结尾自动计长
		uintptr(unsafe.Pointer(&rc)),
		DT_CENTER|DT_VCENTER|DT_SINGLELINE)

	pSelectObject.Call(hdc, oldFont) // 还原 DC 默认字体，避免 GDI 状态残留
	pEndPaint.Call(c.hwnd, uintptr(unsafe.Pointer(&ps)))
}

// ---------- 图层 ----------

// desktopWindow 返回桌面窗口 Progman（壁纸和桌面图标都在它里面）。
// 每次实时查询，防止 explorer 重启后句柄失效。
func desktopWindow() uintptr {
	if h, _, _ := pFindWindowW.Call(
		uintptr(unsafe.Pointer(utf16Ptr("Progman"))), 0); h != 0 {
		return h
	}
	h, _, _ := pGetDesktopWindow.Call()
	return h
}

// desktopAnchor 返回桌面层的 z 序锚点 = 桌面窗口"正上方那一个"窗口。
//
// SetWindowPos 的语义是把窗口插到 HwndInsertAfter 的**下面**，所以：
//   - 插到 Progman 之后  → 沉到壁纸下面，画面上完全看不见（这是最容易踩的坑）
//   - 插到 Progman 上面那个窗口之后 → 紧贴桌面之上、所有应用窗口之下 ✅
func desktopAnchor(self uintptr) uintptr {
	prog := desktopWindow()
	if prog == 0 {
		return 0
	}
	anchor, _, _ := pGetWindow.Call(prog, GW_HWNDPREV)
	if self != 0 && anchor == self {
		// 自己已经在目标位置：改成插到"自己上面那个"之后，等于原地不动
		anchor, _, _ = pGetWindow.Call(self, GW_HWNDPREV)
	}
	return anchor
}

func (c *Clock) anchorZ() {
	if anchor := desktopAnchor(c.hwnd); anchor != 0 {
		pSetWindowPos.Call(c.hwnd, anchor, 0, 0, 0, 0,
			SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
	}
}

// applyLayer 按当前配置重设扩展样式并重新落位。
//
// 改完扩展样式必须再调一次 SetWindowPos：SetWindowLongPtrW 只改属性值，
// 系统不会因此重排 z 序，样式要等下一次位置变更才真正生效。
func (c *Clock) applyLayer() {
	pSetWindowLongPtrW.Call(c.hwnd, negPtr(GWL_EXSTYLE), c.exStyle())
	if c.cfg.DesktopLayer {
		c.anchorZ()
		return
	}
	// HWND_TOPMOST = -1
	pSetWindowPos.Call(c.hwnd, negPtr(-1), 0, 0, 0, 0,
		SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
}

// isAnchored 判断自己是不是"桌面之上第一个**可见**窗口"。
// z 序里夹着的隐藏辅助窗（Chrome/IME 之类）不影响画面，直接跳过，
// 否则每秒都会误判并白调一次 SetWindowPos。
func (c *Clock) isAnchored() bool {
	h, _, _ := pGetWindow.Call(desktopWindow(), GW_HWNDPREV)
	for h != 0 {
		if vis, _, _ := pIsWindowVisible.Call(h); vis != 0 {
			break
		}
		h, _, _ = pGetWindow.Call(h, GW_HWNDPREV)
	}
	return h == c.hwnd
}

// keepOnDesktop 每秒自检：始终"贴在桌面之上、所有应用窗口之下"。
//
// 两个会被系统打断的时机：
//  1. Win+D / 右键"显示桌面" —— 系统把 Progman 抬到前台，时钟被压到壁纸后面，
//     退出桌面状态才回来；这里重新落位，让它 1 秒内复位。
//  2. explorer 重排 / 换主题 —— 同理。
//
// 代价只有一次 GetWindow（+ 必要时一次 SetWindowPos），可以忽略。
func (c *Clock) keepOnDesktop() {
	if !c.cfg.DesktopLayer {
		return
	}
	vis, _, _ := pIsWindowVisible.Call(c.hwnd)
	iconic, _, _ := pIsIconic.Call(c.hwnd)
	if vis == 0 || iconic != 0 {
		pShowWindow.Call(c.hwnd, SW_RESTORE) // 有 NOACTIVATE 样式，不会被激活抢焦点
		c.applyLayer()
		return
	}
	if !c.isAnchored() {
		c.anchorZ()
	}
}

// onWindowPosChanging 拦截并改写 z 序，做到"任何时候都被压在应用窗口之下"。
//
// 只靠 applyLayer() 调一次 SetWindowPos 是不够的：点击、窗口调整、
// explorer 重排都会让系统重设插入位置，必须在 WM_WINDOWPOSCHANGING 里改。
func (c *Clock) onWindowPosChanging(lparam uintptr) uintptr {
	if !c.cfg.DesktopLayer {
		return 0
	}
	anchor := desktopAnchor(c.hwnd)
	if anchor == 0 {
		return 0
	}
	// lparam 由系统在 DispatchMessage 时填入，指向系统内存里的 WINDOWPOS，
	// 不是 Go 堆对象、不逃逸、只在本次回调内有效，因此这个转换是安全的。
	// 但 go vet 的 unsafeptr 检查无法分辨来源，会恒定报一条——见 README 说明。
	wp := (*WINDOWPOS)(unsafe.Pointer(lparam))
	wp.HwndInsertAfter = anchor
	wp.Flags &^= SWP_NOZORDER // 清掉 NOZORDER，否则改写不生效
	return 0
}

// ---------- 拖动 ----------

func (c *Clock) onLButtonDown() {
	if c.cfg.ClickThrough {
		return // 穿透模式下点击根本到不了这里，双保险
	}
	c.dragging = true

	var pt POINT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	var rc RECT
	pGetWindowRect.Call(c.hwnd, uintptr(unsafe.Pointer(&rc)))
	// 记下光标相对窗口左上角的偏移。拖动时保持这个偏移不变，
	// 否则窗口会瞬间跳到光标位置、跟手感全无。
	c.dragOffX = int(pt.X) - int(rc.Left)
	c.dragOffY = int(pt.Y) - int(rc.Top)

	pSetCapture.Call(c.hwnd) // 保证鼠标移出窗口也能继续收到 WM_MOUSEMOVE
}

func (c *Clock) onMouseMove() {
	if !c.dragging {
		return
	}
	var pt POINT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	x := int(pt.X) - c.dragOffX
	y := int(pt.Y) - c.dragOffY

	// 最后一个参数 1 = 立即重绘，否则拖动过程中会留下拖影
	pMoveWindow.Call(c.hwnd, uintptr(x), uintptr(y), uintptr(c.w), uintptr(c.h), 1)
	c.cfg.X = x
	c.cfg.Y = y
}

func (c *Clock) onLButtonUp() {
	if !c.dragging {
		return
	}
	c.dragging = false
	pReleaseCapture.Call()
	_ = saveConfig(c.cfg) // 松手即落盘
}
