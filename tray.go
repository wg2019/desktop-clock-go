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

// idOpacityBase 是"不透明度"档位的命令 ID 基址：第 i 档 = idOpacityBase + i。
//
// 偏移取索引而不是百分比，档位增删只需要改 opacitySteps，ID 不会和旧值错位。
// 注意别在 [idOpacityBase, idOpacityBase+len(opacitySteps)) 区间里再定义别的命令。
const idOpacityBase = 200

// opacitySteps 是右键菜单里可选的不透明度档位（百分比，100 = 完全不透明）。
//
// 只给几个整数档而不是滑块：这是"文字往壁纸里沉多少"的观感参数，差 5% 肉眼看不出来，
// 档位给多了反而让人反复试。85 是出厂值，列在里面方便一键回退。
var opacitySteps = []int{60, 75, 85, 95, 100}

// ---------- 托盘图标 ----------

// 定时器 ID。区分开是为了在 WM_TIMER 里辨认是哪个定时器到点了。
const (
	timerTick       = 1 // 每秒一次：刷新时间 + 层级自检
	timerTrayRetry  = 2 // 托盘登记失败后的重试
	timerTrayRepair = 3 // 启动后补登记一次：任务栏可能把首次请求静默丢了
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

// readdIcon 先删再添，让任务栏重新读一遍登记记录。
//
// 用于启动后的补登记：任务栏有时会把第一次请求静默丢掉（返回成功、图标不出现、
// 记录也不写），过几秒无条件重挂一次就能救回来。
//
// 注意它只重新登记图标，不碰 NotifyIconSettings 里的任何值 —— 图标常显与否
// 完全是用户在系统设置里的权利，程序不代劳也不撤销（详见 README 的托盘一节）。
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

// buildTrayMenu 构建整个右键菜单，返回主菜单句柄。
//
// 单独拆出来是为了可测。菜单结构的错（少一项、子菜单挂错位置、勾选状态不对）
// 运行时只表现成"点了没反应"或"勾错了"，编译期一点提示都没有，只能靠断言兜。
// 调用方负责 DestroyMenu —— 它是递归的，挂在上面的子菜单会一并销毁。
func (c *Clock) buildTrayMenu() uintptr {
	hmenu, _, _ := pCreatePopupMenu.Call()

	appendMenu(hmenu, MF_STRING, idColor, "选择颜色...")
	appendMenu(hmenu, MF_STRING, idFont, "选择字体...")

	// 不透明度子菜单。标题带上当前实际值：即便这个值不在预设档位里（比如手改
	// config.json 写成 73），用户也能一眼看到现在是多少，而不是"一个都没勾"。
	hsub, _, _ := pCreatePopupMenu.Call()
	for i, v := range opacitySteps {
		appendMenu(hsub, menuFlags(c.cfg.Opacity == v),
			idOpacityBase+i, fmt.Sprintf("%d%%", v))
	}
	appendMenu(hmenu, MF_POPUP, int(hsub), fmt.Sprintf("不透明度（当前 %d%%）", c.cfg.Opacity))

	appendMenu(hmenu, MF_SEPARATOR, 0, "")

	appendMenu(hmenu, menuFlags(c.cfg.HourFormat == "12"), idToggleFormat, "12 小时制")
	appendMenu(hmenu, menuFlags(c.cfg.ClickThrough), idToggleLock, "锁定(鼠标穿透)")
	appendMenu(hmenu, menuFlags(c.cfg.DesktopLayer), idToggleLayer, "只在桌面显示(不遮挡窗口)")
	appendMenu(hmenu, menuFlags(c.cfg.Autostart), idToggleAutostart, "开机自启动")
	appendMenu(hmenu, MF_SEPARATOR, 0, "")
	appendMenu(hmenu, MF_STRING, idExit, "退出")

	return hmenu
}

func (c *Clock) showTrayMenu() {
	hmenu := c.buildTrayMenu()

	var pt POINT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// 必须先把本窗口设为前台，否则菜单弹出后点空白处不会消失（经典 Win32 坑）
	pSetForegroundWindow.Call(c.hwnd)
	pTrackPopupMenuEx.Call(hmenu,
		TPM_RIGHTBUTTON,
		uintptr(pt.X), uintptr(pt.Y),
		c.hwnd, 0)

	// DestroyMenu 是递归的：销毁父菜单会一并销毁上面挂的子菜单，不用单独 Destroy
	pDestroyMenu.Call(hmenu)
}

// ---------- 命令分发 ----------

// setOpacity 应用新的不透明度：改内存 → 落盘 → 重画一帧。
//
// 不需要 resizeToText：不透明度不改变文字尺寸。分层窗口的内容由 render 直接提交，
// 也不走 WM_PAINT，所以点完菜单立刻就能看到新观感。
func (c *Clock) setOpacity(v int) {
	c.cfg.Opacity = clampOpacity(v)
	_ = saveConfig(c.cfg)
	c.render()
}

// onCommand 分发菜单命令。
// wparam 低 16 位是命令 ID，高 16 位是通知码（菜单项这里恒为 0，忽略）。
func (c *Clock) onCommand(wparam uintptr) {
	id := int(wparam & 0xFFFF)

	// 不透明度档位按 ID 区间分发：档位由 opacitySteps 决定，在 switch 里逐档写 case
	// 的话，每次调档位都要改两处，容易漏。
	if i := id - idOpacityBase; i >= 0 && i < len(opacitySteps) {
		c.setOpacity(opacitySteps[i])
		return
	}

	switch id {
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
		c.resizeToText() // 12 小时制多了"上午/下午"，窗口要变宽；内部会重画
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
