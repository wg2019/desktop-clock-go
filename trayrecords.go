package main

// 托盘图标登记记录的清理。
//
// 背景：Windows 11 把「任务栏托盘里有哪些图标、是否常显」记在
// HKCU\Control Panel\NotifyIconSettings 下，**每条记录对应一个 exe 的完整路径**。
// 于是同一个程序换个目录运行一次，设置界面的「其他系统托盘图标」里就会多出一条
// 同名的「桌面时钟」；原目录被删掉之后，那条记录仍然留着，永远不会自己消失。
//
// 这段代码在启动时做两件事：
//  1. 删掉「本程序、但 exe 已经不存在」的死记录 —— 僵尸条目自愈；
//  2. 找出「本程序、文件仍在、但不是当前这份」的其他副本，交给调用方提示用户。
//
// 只认 exe 文件名相同的记录，别的程序的记录一律不碰。

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	notifyIconSettingsPath = `Control Panel\NotifyIconSettings`
	execPathValue          = "ExecutablePath"
)

// recordKind 是一条托盘登记记录相对于「当前运行的程序」的分类。
type recordKind int

const (
	recordForeign   recordKind = iota // 别的程序，与本程序无关
	recordSelf                        // 当前这份程序自己的记录
	recordStale                       // 本程序，但那个 exe 已经被删掉了
	recordOtherCopy                   // 本程序，文件还在，但不是当前这份
)

// classifyTrayRecord 判断一条记录属于哪一类。
//
// exists 由调用方注入：生产代码传 win32FileExists，测试传假的实现，
// 这样判定逻辑可以脱离注册表和真实文件系统单独验证。
func classifyTrayRecord(recordPath, selfExe string, exists func(string) bool) recordKind {
	if recordPath == "" || selfExe == "" {
		return recordForeign
	}
	// 只处理同名 exe；不同程序即使同名也不算（下面还会再比对路径）
	if !strings.EqualFold(filepath.Base(recordPath), filepath.Base(selfExe)) {
		return recordForeign
	}
	if strings.EqualFold(filepath.Clean(recordPath), filepath.Clean(selfExe)) {
		return recordSelf
	}
	if exists(recordPath) {
		return recordOtherCopy
	}
	return recordStale
}

// trayAudit 是一次启动时清理的结果。
type trayAudit struct {
	Removed []string // 已删除的死记录（对应文件已不存在）
	Others  []string // 仍存在的其他副本路径，会导致托盘里出现第二个同名图标
}

// auditTrayRecords 扫描托盘登记表，删死记录并汇报仍存在的其他副本。
//
// Win10 及更早版本没有这个注册表键，打开失败时直接返回空结果 —— 那套系统本来
// 也不会因为这个机制产生重复条目。
func auditTrayRecords(selfExe string) trayAudit {
	var out trayAudit

	var hKey uintptr
	ret, _, _ := pRegOpenKeyExW.Call(
		hkeyCurrentUser,
		uintptr(unsafe.Pointer(utf16Ptr(notifyIconSettingsPath))),
		0,
		KEY_ALL_ACCESS, // 需要 DELETE 权限才能删子键，KEY_ALL_ACCESS 已包含
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != ERROR_SUCCESS {
		return out
	}
	defer pRegCloseKey.Call(hKey)

	// 先收集全部子键名再动手删：边枚举边删会让后续索引整体前移，漏掉条目。
	subKeys := listSubKeys(hKey)
	for _, sub := range subKeys {
		path := queryRegString(hKey, sub, execPathValue)
		switch classifyTrayRecord(path, selfExe, win32FileExists) {
		case recordStale:
			if deleteRegKey(hKey, sub) {
				out.Removed = append(out.Removed, path)
			}
		case recordOtherCopy:
			out.Others = append(out.Others, path)
		}
	}
	return out
}

// listSubKeys 枚举一个注册表键下的所有子键名。
func listSubKeys(hKey uintptr) []string {
	var out []string
	for i := uint32(0); ; i++ {
		var buf [512]uint16
		size := uint32(len(buf))
		ret, _, _ := pRegEnumKeyExW.Call(
			hKey,
			uintptr(i),
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
			0, 0, 0, 0,
		)
		if ret == ERROR_NO_MORE_ITEMS || ret != ERROR_SUCCESS {
			break
		}
		out = append(out, utf16ToString(buf[:size]))
	}
	return out
}

// queryRegString 读取子键下的一个 REG_SZ 值；不存在或类型不符时返回空串。
func queryRegString(parent uintptr, subKey, value string) string {
	var hKey uintptr
	ret, _, _ := pRegOpenKeyExW.Call(
		parent,
		uintptr(unsafe.Pointer(utf16Ptr(subKey))),
		0,
		KEY_QUERY_VALUE,
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != ERROR_SUCCESS {
		return ""
	}
	defer pRegCloseKey.Call(hKey)

	// 第一次只问长度（lpData 传 NULL），避免猜缓冲区大小
	var typ, size uint32
	ret, _, _ = pRegQueryValueExW.Call(
		hKey,
		uintptr(unsafe.Pointer(utf16Ptr(value))),
		0,
		uintptr(unsafe.Pointer(&typ)),
		0,
		uintptr(unsafe.Pointer(&size)),
	)
	if ret != ERROR_SUCCESS || typ != REG_SZ || size == 0 || size > 64*1024 {
		return ""
	}

	buf := make([]uint16, size/2+1)
	ret, _, _ = pRegQueryValueExW.Call(
		hKey,
		uintptr(unsafe.Pointer(utf16Ptr(value))),
		0,
		uintptr(unsafe.Pointer(&typ)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret != ERROR_SUCCESS {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// deleteRegKey 删除一个子键（只用于叶子键）。
func deleteRegKey(parent uintptr, subKey string) bool {
	ret, _, _ := pRegDeleteKeyW.Call(
		parent,
		uintptr(unsafe.Pointer(utf16Ptr(subKey))),
	)
	return ret == ERROR_SUCCESS
}

// win32FileExists 判断路径是否存在。
//
// 用 GetFileAttributesW 而不是 os.Stat：这个方法不打开文件、不受占用影响，
// 而且对没有权限的路径也会明确返回 INVALID_FILE_ATTRIBUTES。
func win32FileExists(path string) bool {
	if path == "" {
		return false
	}
	r, _, _ := pGetFileAttributesW.Call(uintptr(unsafe.Pointer(utf16Ptr(path))))
	return uint32(r) != INVALID_FILE_ATTRIBUTES
}

// duplicateCopyMessage 组装「发现另一个副本」的提示文案。
func duplicateCopyMessage(selfExe string, others []string) string {
	var b strings.Builder
	b.WriteString("这台电脑上还有另一个「桌面时钟」副本：\n\n")
	for _, p := range others {
		b.WriteString("  " + p + "\n")
	}
	b.WriteString("\n它会在「设置 > 个性化 > 任务栏 > 其他系统托盘图标」里\n")
	b.WriteString("显示成第二个同名条目。建议只保留一份：\n")
	b.WriteString("删掉上面那个目录（或当前这个），之后只在保留的那份里启动。\n\n")
	b.WriteString("当前运行的是：\n  " + selfExe)
	return b.String()
}
