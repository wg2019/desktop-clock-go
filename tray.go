package main

import (
	"fmt"
	"unsafe"
)

// 菜单命令 ID（与 WM_COMMAND 的 LOWORD 对应）
const (
	idColor           = 101
	idFont            = 102
	idToggleFormat    = 103
	idToggleLock      = 104
	idToggleAutostart = 105
	idExit            = 106
	idToggleLayer     = 107
)

// ---------- 托盘图标 ----------

func (c *Clock) setupTray() {
	c.nid = NOTIFYICONDATAW{}
	c.nid.CbSize = uint32(unsafe.Sizeof(c.nid)) // 必须是 976（已由编译期断言保证）
	c.nid.Hwnd = c.hwnd
	c.nid.UID = 1
	c.nid.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	c.nid.UCallbackMessage = WM_TRAY
	c.nid.HIcon = c.hicon
	copyUTF16(c.nid.SzTip[:], "桌面时钟")

	if r, _, _ := pShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&c.nid))); r == 0 {
		fmt.Println("[提示] 托盘图标创建失败；设置仍可通过编辑 config.json 调整")
	}
}

func (c *Clock) removeTray() {
	if c.nid.Hwnd != 0 {
		pShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&c.nid)))
	}
}

// ---------- 右键菜单 ----------

func appendMenu(hmenu, flags uintptr, id int, text string) {
	var tp uintptr
	if text != "" {
		tp = uintptr(unsafe.Pointer(utf16Ptr(text)))
	}
	pAppendMenuW.Call(hmenu, flags, uintptr(id), tp)
}

// menuFlags 拼出带勾选状态的菜单标志。
func menuFlags(checked bool) uintptr {
	if checked {
		return MF_STRING | MF_CHECKED
	}
	return MF_STRING | MF_UNCHECKED
}

func (c *Clock) showTrayMenu() {
	hmenu, _, _ := pCreatePopupMenu.Call()

	appendMenu(hmenu, MF_STRING, idColor, "选择颜色...")
	appendMenu(hmenu, MF_STRING, idFont, "选择字体...")
	appendMenu(hmenu, MF_SEPARATOR, 0, "")

	appendMenu(hmenu, menuFlags(c.cfg.HourFormat == "12"), idToggleFormat, "12 小时制")
	appendMenu(hmenu, menuFlags(c.cfg.ClickThrough), idToggleLock, "锁定(鼠标穿透)")
	appendMenu(hmenu, menuFlags(c.cfg.DesktopLayer), idToggleLayer, "只在桌面显示(不遮挡窗口)")
	appendMenu(hmenu, menuFlags(c.cfg.Autostart), idToggleAutostart, "开机自启动")
	appendMenu(hmenu, MF_SEPARATOR, 0, "")
	appendMenu(hmenu, MF_STRING, idExit, "退出")

	var pt POINT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// 必须先把本窗口设为前台，否则菜单弹出后点空白处不会消失（经典 Win32 坑）
	pSetForegroundWindow.Call(c.hwnd)
	pTrackPopupMenuEx.Call(hmenu,
		TPM_RIGHTBUTTON,
		uintptr(pt.X), uintptr(pt.Y),
		c.hwnd, 0)

	pDestroyMenu.Call(hmenu)
}

// ---------- 命令分发 ----------

func (c *Clock) onCommand(wparam uintptr) {
	switch int(wparam & 0xFFFF) {
	case idColor:
		c.chooseColor()
	case idFont:
		c.chooseFont()
	case idToggleFormat:
		if c.cfg.HourFormat == "24" {
			c.cfg.HourFormat = "12"
		} else {
			c.cfg.HourFormat = "24"
		}
		_ = saveConfig(c.cfg)
		c.resizeToText() // 12 小时制多了"上午/下午"，窗口要变宽
		pInvalidateRect.Call(c.hwnd, 0, 1)
	case idToggleLock:
		c.cfg.ClickThrough = !c.cfg.ClickThrough
		c.applyLayer() // 重设扩展样式；桌面层下同时重新压回底部
		_ = saveConfig(c.cfg)
	case idToggleLayer:
		c.cfg.DesktopLayer = !c.cfg.DesktopLayer
		c.applyLayer()
		_ = saveConfig(c.cfg)
	case idToggleAutostart:
		c.cfg.Autostart = !c.cfg.Autostart
		if !setAutostart(c.cfg.Autostart) {
			c.cfg.Autostart = !c.cfg.Autostart // 写注册表失败则回滚 UI 状态
			fmt.Println("[警告] 读写注册表失败，自启设置未生效")
		}
		_ = saveConfig(c.cfg)
	case idExit:
		pDestroyWindow.Call(c.hwnd)
	}
}
