package main

import (
	"strings"
	"testing"
)

// 托盘登记记录的判定逻辑是这段改造里唯一有分支的地方，
// 用假的 exists 把它从注册表和真实文件系统里剥出来单独验证。
func TestClassifyTrayRecord(t *testing.T) {
	const self = `C:\Users\me\Documents\DesktopClock\DesktopClockGo.exe`

	cases := []struct {
		name       string
		recordPath string
		exists     map[string]bool
		want       recordKind
	}{
		{
			name:       "空路径：不是我们的记录",
			recordPath: "",
			want:       recordForeign,
		},
		{
			name:       "别的程序：文件名不同，一律不碰",
			recordPath: `C:\Windows\System32\notepad.exe`,
			exists:     map[string]bool{`C:\Windows\System32\notepad.exe`: true},
			want:       recordForeign,
		},
		{
			name:       "当前这份程序自己的记录：不动",
			recordPath: self,
			exists:     map[string]bool{self: true},
			want:       recordSelf,
		},
		{
			name:       "同一路径但大小写不同：仍算自己",
			recordPath: `c:\users\me\documents\desktopclock\DESKTOPCLOCKGO.EXE`,
			exists:     map[string]bool{self: true},
			want:       recordSelf,
		},
		{
			name:       "同名的其他副本，文件已被删除：死记录",
			recordPath: `C:\Users\me\Desktop\桌面时钟-v1.0.0-win64\DesktopClockGo.exe`,
			exists:     map[string]bool{},
			want:       recordStale,
		},
		{
			name:       "同名的其他副本，文件还在：会造成第二个托盘图标",
			recordPath: `C:\Users\me\Documents\桌面时钟-v1.0.0-win64\DesktopClockGo.exe`,
			exists: map[string]bool{
				`C:\Users\me\Documents\桌面时钟-v1.0.0-win64\DesktopClockGo.exe`: true,
			},
			want: recordOtherCopy,
		},
		{
			name:       "文件名相同但路径含多余分隔符：清洗后仍判定为副本",
			recordPath: `C:\Users\me\Documents\\DesktopClockGo.exe`,
			exists:     map[string]bool{`C:\Users\me\Documents\\DesktopClockGo.exe`: false},
			want:       recordStale,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exists := func(p string) bool { return c.exists[p] }
			if got := classifyTrayRecord(c.recordPath, self, exists); got != c.want {
				t.Errorf("classifyTrayRecord(%q) = %v, want %v", c.recordPath, got, c.want)
			}
		})
	}
}

// 判定为「别的程序」或「自己」时不应该去查文件系统 ——
// 否则每启动一次就要对表里所有程序的路径做一次磁盘访问。
func TestClassifyTrayRecordSkipsFileCheck(t *testing.T) {
	const self = `C:\Apps\DesktopClock\DesktopClockGo.exe`
	calls := 0
	exists := func(string) bool { calls++; return true }

	classifyTrayRecord(`C:\Windows\System32\notepad.exe`, self, exists)
	classifyTrayRecord(self, self, exists)
	if calls != 0 {
		t.Errorf("无关记录不应触发文件检查，实际调用了 %d 次", calls)
	}

	classifyTrayRecord(`C:\Other\DesktopClockGo.exe`, self, exists)
	if calls != 1 {
		t.Errorf("同名副本应检查一次文件是否存在，实际 %d 次", calls)
	}
}

// 提示文案必须给出具体路径，否则用户不知道删哪个。
func TestDuplicateCopyMessageListsPaths(t *testing.T) {
	const self = `C:\A\DesktopClockGo.exe`
	others := []string{`C:\B\DesktopClockGo.exe`, `C:\C\DesktopClockGo.exe`}
	msg := duplicateCopyMessage(self, others)

	for _, want := range append([]string{self}, others...) {
		if !strings.Contains(msg, want) {
			t.Errorf("提示文案缺少路径 %q", want)
		}
	}
	if !strings.Contains(msg, "其他系统托盘图标") {
		t.Error("提示文案应指出设置里的具体位置")
	}
}
