package main

import (
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

// 定时器 ID。区分开是为了在 WM_TIMER 里辨认是哪个定时器到点了。
const (
	timerTick        = 1 // 每秒一次：刷新时间 + 层级自检
	timerTrayRetry   = 2 // 托盘登记失败后的重试
	timerTrayPromote = 3 // 补登记「常显」：首次运行时记录还没落盘
	timerTrayRepair  = 4 // 启动后补登记一次：任务栏可能把首次请求静默丢了
)

// trayRepairDelay 是启动后补登记的延迟。
//
// 实测会遇到这种情况：Shell_NotifyIcon 返回成功、也没有任何错误，但任务栏既没画出
// 图标、也没往 NotifyIconSettings 里写记录 —— 请求被静默丢弃了（任务栏刚重建、
// 或托盘处于异常状态时）。返回值靠不住，只能过一会儿再登记一次兜底。
const trayRepairDelay = 2500

// maxTrayRetry 是托盘登记的最大重试次数。
// explorer 重启后任务栏要过几百毫秒才准备好，期间 Shell_NotifyIcon 会失败，
// 等一会儿再试基本都能成功；连续失败到这个次数才认定是真的出问题。
const maxTrayRetry = 3

// maxPromoteWait 是「等任务栏把登记记录写进注册表」的最大重试次数。
//
// 首次运行时任务栏要过几百毫秒才落盘那条记录；正常一两秒内就能等到。
// 一直等不到说明这台机器不会写（例如被组策略挡住），再加定时器也是白枚举注册表，
// 所以设上限，超了就放弃——图标此时多半仍在溢出区里，用户还能手动放行。
const maxPromoteWait = 5

// setupTray 把图标登记到任务栏，返回是否成功。
//
// 先 NIM_DELETE 再 NIM_ADD：explorer 重启后旧图标已随任务栏消失（DELETE 失败无害），
// 但如果是自己重复调用，先删能避免"图标已存在"导致 ADD 失败。
func (c *Clock) setupTray() bool {
	c.nid = NOTIFYICONDATAW{}
	c.nid.CbSize = uint32(unsafe.Sizeof(c.nid)) // 必须是 976（已由编译期断言保证）
	c.nid.Hwnd = c.hwnd
	c.nid.UID = 1
	c.nid.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	c.nid.UCallbackMessage = WM_TRAY
	c.nid.HIcon = c.hicon
	copyUTF16(c.nid.SzTip[:], "桌面时钟")

	pShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&c.nid)))
	if r, _, _ := pShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&c.nid))); r != 0 {
		c.trayRetry = 0
		pKillTimer.Call(c.hwnd, timerTrayRetry)
		c.ensureTrayVisible()
		return true
	}

	if c.trayRetry < maxTrayRetry {
		c.trayRetry++
		pSetTimer.Call(c.hwnd, timerTrayRetry, 1500, 0)
		return false
	}
	// 编译成 -H windowsgui 后没有控制台，只能靠弹窗告知；
	// 托盘没了等于菜单没了，用户需要知道还能改 config.json。
	showError("桌面时钟", "托盘图标创建失败。\n设置仍可通过编辑 config.json 调整。")
	return false
}

func (c *Clock) removeTray() {
	if c.nid.Hwnd != 0 {
		pShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&c.nid)))
	}
}

// ensureTrayVisible 保证图标是「常显」的。
//
// Win11 默认把新程序的图标折叠进溢出区，而折叠的方法在这台机器上根本找不到入口
// （任务栏连 "^" 都不出现），等于图标彻底消失、右键菜单也进不去。所以登记成功后
// 主动把本程序的记录改成常显，再重新挂一次让任务栏重新读取。
//
// 两种情况需要等一等：首次运行那一刻任务栏还没把记录写进注册表（found=false）。
func (c *Clock) ensureTrayVisible() {
	changed, found := promoteTrayIcon(exePath())
	switch {
	case changed:
		c.promoteTries = 0
		c.readdIcon()
	case found:
		c.promoteTries = 0 // 已经是常显，什么都不用做
	default:
		if c.promoteTries < maxPromoteWait {
			c.promoteTries++
			pSetTimer.Call(c.hwnd, timerTrayPromote, 800, 0)
		}
	}
}

// readdIcon 先删再添，让任务栏重新读一遍登记记录。
//
// 也用于启动后的补登记：任务栏有时会把第一次请求静默丢掉（返回成功、图标不出现、
// 记录也不写），过几秒无条件重挂一次就能救回来。
func (c *Clock) readdIcon() {
	pShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&c.nid)))
	pShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&c.nid)))
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

// onCommand 分发菜单命令。
// wparam 低 16 位是命令 ID，高 16 位是通知码（菜单项这里恒为 0，忽略）。
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
			showError("桌面时钟", "写入开机自启失败，设置未生效。")
		}
		_ = saveConfig(c.cfg)
	case idExit:
		pDestroyWindow.Call(c.hwnd)
	}
}
