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
)

func init() {
	wndProcCb = syscall.NewCallback(wndProc)
}

func main() {
	// ── 关键 ──────────────────────────────────────────────────────────
	// Win32 的窗口消息只会投递到"创建该窗口的那个 OS 线程"。
	// Go 的 goroutine 默认会被调度到不同线程上，若不加锁，典型症状是：
	// 窗口能显示出来，但鼠标点不动、托盘菜单弹不出来、定时器不触发 ——
	// 而且完全不报错，极难排查。
	runtime.LockOSThread()
	// ─────────────────────────────────────────────────────────────────

	c, err := newClock()
	if err != nil {
		showError("桌面时钟启动失败", err.Error())
		os.Exit(1)
	}
	app = c

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

	switch msg {
	case WM_DESTROY:
		pKillTimer.Call(hwnd, 1)
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

// showError 用 MessageBox 弹错误。
// 编译成 -H windowsgui 后没有控制台，fmt.Println 的内容用户看不到，
// 所以启动期致命错误必须走弹窗。
func showError(title, text string) {
	pMessageBoxW := user32.NewProc("MessageBoxW")
	pMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(unsafe.Pointer(utf16Ptr(title))),
		0x00000010) // MB_ICONERROR
}
