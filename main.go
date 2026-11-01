package main

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	// app 是唯一的窗口实例。窗口过程是 C 回调，拿不到 Go 的闭包，
	// 所以必须通过包级变量访问。
	app *Clock

	// wndProcCb 由 syscall.NewCallback 生成，注册窗口类时要传这个地址。
	// 在 init 里创建一次即可（每次 NewCallback 都会占用一个回调槽位）。
	wndProcCb uintptr

	// taskbarCreatedMsg 是 explorer 重建任务栏后广播的注册消息。
	// 注册消息的数值由系统动态分配，所以只能在运行期取，不能写成常量。
	taskbarCreatedMsg uintptr
)

func init() {
	wndProcCb = syscall.NewCallback(wndProc)
}

func main() {
	// Win32 的窗口消息只投递给"创建该窗口的那个 OS 线程"。
	// Go 的 goroutine 会被调度到不同线程上，不加锁的典型症状是：
	// 窗口能显示，但鼠标点不动、托盘菜单弹不出来、定时器不触发——而且完全不报错。
	runtime.LockOSThread()

	// 注册 explorer 的"任务栏已重建"广播消息。explorer 重启后系统会清空
	// 所有托盘图标并广播这条消息，程序必须收到后重新登记，否则图标会一直
	// 消失到下次启动（这是托盘类程序必做的一步，漏了就会出现"图标自己没了"）。
	taskbarMsgName := utf16Ptr("TaskbarCreated")
	taskbarCreatedMsg, _, _ = pRegisterWindowMessageW.Call(
		uintptr(unsafe.Pointer(taskbarMsgName)))

	// 必须在建窗口之前判断：否则第二个实例会先画出自己的时钟和托盘图标，
	// 再退出，用户会看到一闪而过的残影。
	if !acquireSingleInstance() {
		// Win11 默认把新程序的托盘图标折叠起来，只说"去托盘找"会让人找不着，
		// 所以这里直接给出恢复路径。
		showInfo("桌面时钟",
			"时钟已经在运行了。\n\n"+
				"若任务栏上找不到它的图标，请打开：\n"+
				"设置 > 个性化 > 任务栏 > 其他系统托盘图标\n"+
				"把「桌面时钟」的开关打开。")
		return
	}

	if err := newClock(); err != nil {
		showError("桌面时钟启动失败", err.Error())
		os.Exit(1)
	}
	// 托盘图标的登记记录是按 exe 完整路径存的：换个目录运行一次，系统就多记一条
	// 同名「桌面时钟」，而删掉的副本留下的记录不会自己消失。
	// 这里清掉指向已删除文件的死记录；若还有别的副本仍在，提示用户只留一份。
	// 放在建窗口之后，是为了让时钟先出来，弹窗不会挡住启动。
	if a := auditTrayRecords(exePath()); len(a.Others) > 0 {
		showAlert("桌面时钟", duplicateCopyMessage(exePath(), a.Others))
	}

	messageLoop()
}

func messageLoop() {
	var msg MSG
	for {
		ret, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		// 返回 0 = WM_QUIT；返回 -1 = 出错
		if v := int32(ret); v == 0 || v == -1 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// wndProc 是窗口过程。所有分支都先判空：窗口创建过程中最早的消息
// 可能早于 app 赋值，不判空会 panic 在回调里（进程直接消失，无任何提示）。
func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	c := app

	// 注册消息的数值是运行期才确定的，无法写成 case 常量，只能先单独拦一下。
	// 放在 switch 之前，保证 explorer 重建任务栏后第一时间把图标挂回去。
	if taskbarCreatedMsg != 0 && msg == taskbarCreatedMsg {
		if c != nil {
			c.setupTray()
		}
		return 0
	}

	switch msg {
	case WM_DESTROY:
		pKillTimer.Call(hwnd, timerTick)
		pKillTimer.Call(hwnd, timerTrayRetry)
		pKillTimer.Call(hwnd, timerTrayPromote)
		if c != nil {
			c.removeTray()
		}
		pPostQuitMessage.Call(0)
		return 0

	case WM_PAINT:
		if c != nil {
			c.onPaint()
		}
		return 0

	case WM_WINDOWPOSCHANGING:
		if c != nil {
			return c.onWindowPosChanging(lparam)
		}
		return 0

	case WM_MOUSEACTIVATE:
		// 点时钟不激活窗口：桌面层语义下它是"壁纸的一部分"，不是应用
		return MA_NOACTIVATE

	case WM_TIMER:
		if c != nil {
			if wparam == timerTrayRetry {
				pKillTimer.Call(hwnd, timerTrayRetry)
				c.setupTray()
				return 0
			}
			if wparam == timerTrayPromote {
				pKillTimer.Call(hwnd, timerTrayPromote)
				c.ensureTrayVisible()
				return 0
			}
			if wparam == timerTrayRepair {
				pKillTimer.Call(hwnd, timerTrayRepair)
				c.readdIcon()
				return 0
			}
			c.keepOnDesktop()
			pInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0

	case WM_TRAY:
		// 托盘回调：lparam 低位是鼠标消息
		if c != nil && lparam == WM_RBUTTONUP {
			c.showTrayMenu()
		}
		return 0

	case WM_COMMAND:
		if c != nil {
			c.onCommand(wparam)
		}
		return 0

	case WM_LBUTTONDOWN:
		if c != nil {
			c.onLButtonDown()
		}
		return 0

	case WM_MOUSEMOVE:
		if c != nil {
			c.onMouseMove()
		}
		return 0

	case WM_LBUTTONUP:
		if c != nil {
			c.onLButtonUp()
		}
		return 0

	case WM_CLOSE:
		pDestroyWindow.Call(hwnd)
		return 0
	}

	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

// showError / showInfo 用 MessageBox 弹提示。
// 编译成 -H windowsgui 后没有控制台，fmt.Println 的内容用户看不到，
// 所以任何要告知用户的信息都必须走弹窗。
func showError(title, text string) {
	showMessageBox(title, text, MB_ICONERROR)
}

func showInfo(title, text string) {
	showMessageBox(title, text, MB_ICONINFORMATION)
}

// showAlert 用于启动期必须让用户看见的提示（例如检测到重复副本）。
// 加置顶 + 抢前台：开机自启拉起的进程没有前台权限，普通弹窗会被埋在窗口后面。
func showAlert(title, text string) {
	showMessageBox(title, text, MB_ICONINFORMATION|MB_SETFOREGROUND|MB_TOPMOST)
}

// showMessageBox 的 uType 传 MB_ICON*；hwnd 传 0 = 无父窗口的独立对话框。
func showMessageBox(title, text string, flags uintptr) {
	pMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(unsafe.Pointer(utf16Ptr(title))),
		flags)
}

// mutexHandle 保存命名互斥体的句柄。
// 进程退出时系统会自动释放，这里保留引用是为了让句柄在程序生命周期内始终有效。
var mutexHandle uintptr

// acquireSingleInstance 用命名互斥体保证同一登录会话内只运行一个时钟进程，
// 返回 false 表示已有实例在跑。
func acquireSingleInstance() bool {
	// 名字加 "Local\" 前缀，作用域限于当前登录会话：不同用户各自开一个互不干扰，
	// 与自启动写在 HKCU（本身按用户隔离）的行为保持一致。
	return acquireMutex(`Local\DesktopClockSingleInstance`)
}

// acquireMutex 尝试独占名为 name 的互斥体。抽出来单独一个函数是为了让测试
// 能用独立的名字验证，不至于和真实运行的时钟抢同一个互斥体。
func acquireMutex(name string) bool {
	h, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(utf16Ptr(name))))
	if h == 0 {
		// 极端情况下创建失败（句柄耗尽等）：放行让程序能启动，
		// 双开总好过完全打不开。
		return true
	}
	mutexHandle = h
	if errno, ok := err.(syscall.Errno); ok && errno == ERROR_ALREADY_EXISTS {
		return false
	}
	return true
}
