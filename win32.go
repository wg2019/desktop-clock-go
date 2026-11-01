// Package main — Win32 API 绑定层（纯 syscall，零第三方依赖）
//
// 设计要点：
//  1. 所有 DLL 过程用 LazyProc 懒加载，首次调用时才 GetProcAddress；
//  2. 结构体字段顺序与大小必须与 C 定义逐字节一致 —— 错一个字段，Win32 不会报错，
//     只会静默失败（典型表现：窗口根本不出来，很难查）。因此文件末尾用一组
//     编译期断言把尺寸钉死：字段一改错就直接编译不过；
//  3. 句柄统一用 uintptr；负值常量必须经 intptr 转换，直接写 uintptr(-1)
//     会在 64 位下被截断成 0xFFFFFFFF，丢失高位。
package main

import (
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// ---------- DLL ----------
var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
)

// ---------- 过程 ----------
var (
	// kernel32
	pGetModuleHandleW   = kernel32.NewProc("GetModuleHandleW")
	pGetModuleFileNameW = kernel32.NewProc("GetModuleFileNameW")

	// user32 — 窗口
	pRegisterClassExW      = user32.NewProc("RegisterClassExW")
	pCreateWindowExW       = user32.NewProc("CreateWindowExW")
	pDefWindowProcW        = user32.NewProc("DefWindowProcW")
	pDestroyWindow         = user32.NewProc("DestroyWindow")
	pShowWindow            = user32.NewProc("ShowWindow")
	pUpdateWindow          = user32.NewProc("UpdateWindow")
	pSetWindowPos          = user32.NewProc("SetWindowPos")
	pSetWindowLongPtrW     = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtrW     = user32.NewProc("GetWindowLongPtrW")
	pMoveWindow            = user32.NewProc("MoveWindow")
	pGetClientRect         = user32.NewProc("GetClientRect")
	pGetWindowRect         = user32.NewProc("GetWindowRect")
	pInvalidateRect        = user32.NewProc("InvalidateRect")
	pIsWindowVisible       = user32.NewProc("IsWindowVisible")
	pIsIconic              = user32.NewProc("IsIconic")
	pFindWindowW           = user32.NewProc("FindWindowW")
	pGetDesktopWindow      = user32.NewProc("GetDesktopWindow")
	pGetWindow             = user32.NewProc("GetWindow")
	pSetLayeredWindowAttrs = user32.NewProc("SetLayeredWindowAttributes")

	// user32 — 消息循环
	pGetMessageW      = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessageW = user32.NewProc("DispatchMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")

	// user32 — 绘制 / 定时器 / 光标
	pBeginPaint     = user32.NewProc("BeginPaint")
	pEndPaint       = user32.NewProc("EndPaint")
	pGetDC          = user32.NewProc("GetDC")
	pReleaseDC      = user32.NewProc("ReleaseDC")
	pFillRect       = user32.NewProc("FillRect")
	pDrawTextW      = user32.NewProc("DrawTextW")
	pSetTimer       = user32.NewProc("SetTimer")
	pKillTimer      = user32.NewProc("KillTimer")
	pGetCursorPos   = user32.NewProc("GetCursorPos")
	pSetCapture     = user32.NewProc("SetCapture")
	pReleaseCapture = user32.NewProc("ReleaseCapture")
	pLoadCursorW    = user32.NewProc("LoadCursorW")
	pLoadIconW      = user32.NewProc("LoadIconW")
	pLoadImageW     = user32.NewProc("LoadImageW")

	// user32 — 菜单
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenuW         = user32.NewProc("AppendMenuW")
	pTrackPopupMenuEx    = user32.NewProc("TrackPopupMenuEx")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")

	// gdi32
	pCreateFontIndirectW   = gdi32.NewProc("CreateFontIndirectW")
	pCreateSolidBrush      = gdi32.NewProc("CreateSolidBrush")
	pSelectObject          = gdi32.NewProc("SelectObject")
	pDeleteObject          = gdi32.NewProc("DeleteObject")
	pSetTextColor          = gdi32.NewProc("SetTextColor")
	pSetBkMode             = gdi32.NewProc("SetBkMode")
	pGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")

	// shell32
	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	pExtractIconW     = shell32.NewProc("ExtractIconW")

	// comdlg32
	pChooseColorW = comdlg32.NewProc("ChooseColorW")
	pChooseFontW  = comdlg32.NewProc("ChooseFontW")

	// advapi32 — 注册表（开机自启）
	pRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	pRegSetValueExW   = advapi32.NewProc("RegSetValueExW")
	pRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	pRegDeleteValueW  = advapi32.NewProc("RegDeleteValueW")
	pRegCloseKey      = advapi32.NewProc("RegCloseKey")
)

// ---------- 常量 ----------
const (
	// 窗口基础样式
	WS_POPUP = 0x80000000

	// 扩展样式
	WS_EX_TOPMOST     = 0x00000008
	WS_EX_TRANSPARENT = 0x00000020 // 鼠标穿透：点击直接落到下层窗口
	WS_EX_TOOLWINDOW  = 0x00000080 // 不进 Alt+Tab、不在任务栏显示
	WS_EX_LAYERED     = 0x00080000
	// NOACTIVATE：点击不激活窗口。桌面层必须，否则点一下就会把时钟抬到所有窗口之前
	WS_EX_NOACTIVATE = 0x08000000

	// Get/SetWindowLongPtr 索引
	GWL_STYLE   = -16
	GWL_EXSTYLE = -20

	// ShowWindow
	SW_SHOW    = 5
	SW_RESTORE = 9

	// SetWindowPos
	SWP_NOSIZE     = 0x0001
	SWP_NOMOVE     = 0x0002
	SWP_NOZORDER   = 0x0004
	SWP_NOACTIVATE = 0x0010
	SWP_SHOWWINDOW = 0x0040

	// 层叠窗口
	LWA_COLORKEY = 0x0001

	// GetWindow 的 uCmd
	GW_HWNDNEXT = 2 // z 序里"下面那一个"
	GW_HWNDPREV = 3 // z 序里"上面那一个"

	// 窗口消息
	WM_DESTROY           = 0x0002
	WM_PAINT             = 0x000F
	WM_CLOSE             = 0x0010
	WM_TIMER             = 0x0113
	WM_COMMAND           = 0x0111
	WM_MOUSEACTIVATE     = 0x0021
	WM_WINDOWPOSCHANGING = 0x0046
	WM_LBUTTONDOWN       = 0x0201
	WM_LBUTTONUP         = 0x0202
	WM_MOUSEMOVE         = 0x0200
	WM_RBUTTONUP         = 0x0205
	WM_USER              = 0x0400
	WM_TRAY              = WM_USER + 1 // 自定义：托盘回调消息
	MA_NOACTIVATE        = 3

	// 菜单
	MF_STRING       = 0x0000
	MF_CHECKED      = 0x0008
	MF_UNCHECKED    = 0x0000
	MF_SEPARATOR    = 0x0800
	TPM_RIGHTBUTTON = 0x0002

	// 托盘
	NIF_MESSAGE = 0x0001
	NIF_ICON    = 0x0002
	NIF_TIP     = 0x0004
	NIM_ADD     = 0x0000
	NIM_DELETE  = 0x0001

	// 绘制
	DT_CENTER          = 0x0001
	DT_VCENTER         = 0x0004
	DT_SINGLELINE      = 0x0020
	BKMODE_TRANSPARENT = 1

	// 资源加载
	IMAGE_ICON      = 1
	LR_LOADFROMFILE = 0x0010
	IDC_ARROW       = 32512
	IDI_APPLICATION = 32512

	// 对话框标志
	CC_RGBINIT             = 0x00000001
	CC_FULLOPEN            = 0x00000002
	CF_SCREENFONTS         = 0x00000001
	CF_INITTOLOGFONTSTRUCT = 0x00000040
	CF_NOSCRIPTSEL         = 0x00800000

	// 注册表
	HKEY_CURRENT_USER    = 0x80000001
	KEY_QUERY_VALUE      = 0x0001
	KEY_SET_VALUE        = 0x0002
	REG_SZ               = 1
	ERROR_SUCCESS        = 0
	ERROR_FILE_NOT_FOUND = 2
)

// ---------- 结构体（顺序与 C 定义严格一致） ----------
type RECT struct {
	Left, Top, Right, Bottom int32
}

type POINT struct {
	X, Y int32
}

type SIZE struct {
	Cx, Cy int32
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type LOGFONTW struct {
	LfHeight         int32
	LfWidth          int32
	LfEscapement     int32
	LfOrientation    int32
	LfWeight         int32
	LfItalic         byte
	LfUnderline      byte
	LfStrikeOut      byte
	LfCharSet        byte
	LfOutPrecision   byte
	LfClipPrecision  byte
	LfQuality        byte
	LfPitchAndFamily byte
	LfFaceName       [32]uint16
}

type CHOOSECOLORW struct {
	LStructSize    uint32
	_              uint32
	HwndOwner      uintptr
	HInstance      uintptr
	RgbResult      uint32
	_              uint32
	LpCustColors   *uint32
	Flags          uint32
	_              uint32
	LcustData      uintptr
	LpfnHook       uintptr
	LpTemplateName *uint16
}

type CHOOSEFONTW struct {
	LStructSize    uint32
	_              uint32
	HwndOwner      uintptr
	HDC            uintptr
	LpLogFont      *LOGFONTW
	IPointSize     int32
	Flags          uint32
	RgbColors      uint32
	_              uint32
	LcustData      uintptr
	LpfnHook       uintptr
	LpTemplateName *uint16
	HInstance      uintptr
	LpszStyle      *uint16
	NFontType      uint16
	NAlign         uint16
	NSizeMin       int32
	NSizeMax       int32
}

type NOTIFYICONDATAW struct {
	CbSize           uint32
	_                uint32
	Hwnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	_                uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

type PAINTSTRUCT struct {
	Hdc         uintptr
	FErase      int32
	_           int32
	RcPaint     RECT
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

// WINDOWPOS 是 WM_WINDOWPOSCHANGING 携带的结构；改写 HwndInsertAfter 即可强控 z 序。
type WINDOWPOS struct {
	Hwnd            uintptr
	HwndInsertAfter uintptr
	X               int32
	Y               int32
	Cx              int32
	Cy              int32
	Flags           uint32
	_               uint32
}

type MSG struct {
	Hwnd     uintptr
	Message  uint32
	_        uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       POINT
	LPrivate uint32
}

// ---------- 编译期尺寸断言 ----------
//
// 每个结构体断言两遍：多一字节或少一字节都会因为"负常量转 uint"而编译失败。
// 这是防止结构体定义漂移的唯一可靠手段（Win32 不校验，错了只会静默失败）。
const (
	_ = uint(unsafe.Sizeof(WNDCLASSEXW{}) - 80)
	_ = uint(80 - unsafe.Sizeof(WNDCLASSEXW{}))

	_ = uint(unsafe.Sizeof(LOGFONTW{}) - 92)
	_ = uint(92 - unsafe.Sizeof(LOGFONTW{}))

	_ = uint(unsafe.Sizeof(CHOOSECOLORW{}) - 72)
	_ = uint(72 - unsafe.Sizeof(CHOOSECOLORW{}))

	_ = uint(unsafe.Sizeof(CHOOSEFONTW{}) - 104)
	_ = uint(104 - unsafe.Sizeof(CHOOSEFONTW{}))

	_ = uint(unsafe.Sizeof(NOTIFYICONDATAW{}) - 976)
	_ = uint(976 - unsafe.Sizeof(NOTIFYICONDATAW{}))

	_ = uint(unsafe.Sizeof(PAINTSTRUCT{}) - 72)
	_ = uint(72 - unsafe.Sizeof(PAINTSTRUCT{}))

	_ = uint(unsafe.Sizeof(WINDOWPOS{}) - 40)
	_ = uint(40 - unsafe.Sizeof(WINDOWPOS{}))

	_ = uint(unsafe.Sizeof(MSG{}) - 48)
	_ = uint(48 - unsafe.Sizeof(MSG{}))
)

// ---------- 辅助函数 ----------

// utf16Ptr 把 Go 字符串转为以 NUL 结尾的 UTF-16 指针（Win32 的 *W 系列入参）。
func utf16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}

// utf16ToString 从定长 UTF-16 缓冲中取出字符串（读到 NUL 为止）。
func utf16ToString(buf []uint16) string {
	return syscall.UTF16ToString(buf)
}

// copyUTF16 把 Go 字符串写入定长 UTF-16 数组，末尾补 0。
func copyUTF16(dst []uint16, s string) {
	u := utf16.Encode([]rune(s))
	n := len(u)
	if n > len(dst)-1 {
		n = len(dst) - 1
	}
	for i := 0; i < n; i++ {
		dst[i] = u[i]
	}
	for i := n; i < len(dst); i++ {
		dst[i] = 0
	}
}

// negPtr 把负的 int 常量安全转换为 uintptr（保留 64 位符号扩展）。
func negPtr(v int) uintptr {
	return uintptr(int64(v))
}
