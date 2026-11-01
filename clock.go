package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"
)

const (
	className   = "DesktopClockWin32"
	windowTitle = "DesktopClock"

	// 透明色键：窗口背景用这个颜色填充，配合 LWA_COLORKEY 让该色变全透明。
	// 选 (1,1,1) 而不是纯黑，避免与纯黑背景混合时出现边缘黑边。
	keyR, keyG, keyB = 1, 1, 1
)

// Clock 持有全部运行期状态。单实例，放在包级变量 app 里供窗口过程访问
// （Win32 窗口过程是 C 回调，拿不到 Go 的闭包环境）。
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
	custColors [16]uint32
}

func newClock() (*Clock, error) {
	cfg := loadConfig()
	cfg.Autostart = getAutostart() // 以注册表实际状态为准

	c := &Clock{
		cfg:   cfg,
		hicon: loadAppIcon(),
		w:     200,
		h:     60,
	}
	c.hbrush, _, _ = pCreateSolidBrush.Call(uintptr(toColorRef(keyR, keyG, keyB)))

	// 必须在这里就把实例挂到全局：CreateWindowExW 一返回就可能收到
	// WM_WINDOWPOSCHANGING 等消息，那时 wndProc 里要靠 app 找到实例。
	app = c

	if err := c.registerClass(); err != nil {
		return nil, err
	}
	c.createFont()
	if err := c.createWindow(); err != nil {
		return nil, err
	}
	c.resizeToText()

	pShowWindow.Call(c.hwnd, SW_SHOW)
	pUpdateWindow.Call(c.hwnd)
	// 色键：该颜色的像素完全透明，且不接收鼠标（配合 WS_EX_TRANSPARENT 即穿透）
	pSetLayeredWindowAttrs.Call(c.hwnd, uintptr(toColorRef(keyR, keyG, keyB)), 0, LWA_COLORKEY)

	c.applyLayer()
	c.setupTray()
	pSetTimer.Call(c.hwnd, 1, 1000, 0) // 每秒一次：刷新时间 + 自检层级

	return c, nil
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
	lf.LfWeight = 400
	if c.cfg.FontWeight == "bold" {
		lf.LfWeight = 700
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
	hdc, _, _ := pGetDC.Call(c.hwnd)
	pSelectObject.Call(hdc, c.hfont)

	var sz SIZE
	pGetTextExtentPoint32W.Call(hdc,
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(len([]rune(text))),
		uintptr(unsafe.Pointer(&sz)))
	pReleaseDC.Call(c.hwnd, hdc)

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
	// 这里的 uintptr -> unsafe.Pointer 转换是 Win32 消息回调的固有写法，
	// 也是 go vet 唯一会提示的地方（unsafeptr 检查无法分辨来源）：
	//   · lparam 由系统在 DispatchMessage 时填入，指向系统栈/堆上的 WINDOWPOS，
	//     不是 Go 分配的堆对象，因此不受 Go 栈增长与 GC 搬移影响；
	//   · 指针不逃逸，只在本次回调内读写，返回后即失效。
	// 换成任何等价写法（unsafe.Add、unsafe.Slice）都仍需要同一次转换，
	// vet 照样会报，所以保留原样并在此说明。
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
