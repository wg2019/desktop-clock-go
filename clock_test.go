package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
	"unsafe"
)

// ---------- 配置：外部 json 兼容性 ----------

// TestConfigJSONCompatible 用一份手写的、字段齐全的 config.json 原文反序列化。
// 这条测试锁死配置字段名：以后重命名字段会立刻失败，防止用户升级后丢设置。
func TestConfigJSONCompatible(t *testing.T) {
	rawJSON := `{
  "hour_format": "12",
  "color": "#00FFFF",
  "font_family": "Microsoft YaHei",
  "font_size": 52,
  "font_weight": "bold",
  "italic": true,
  "x": 1634,
  "y": 40,
  "autostart": false,
  "click_through": true,
  "desktop_layer": true
}`
	var cfg Config
	if err := json.Unmarshal([]byte(rawJSON), &cfg); err != nil {
		t.Fatalf("解析配置文件失败: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"hour_format", cfg.HourFormat, "12"},
		{"color", cfg.Color, "#00FFFF"},
		{"font_family", cfg.FontFamily, "Microsoft YaHei"},
		{"font_size", cfg.FontSize, 52},
		{"font_weight", cfg.FontWeight, "bold"},
		{"italic", cfg.Italic, true},
		{"x", cfg.X, 1634},
		{"y", cfg.Y, 40},
		{"autostart", cfg.Autostart, false},
		{"click_through", cfg.ClickThrough, true},
		{"desktop_layer", cfg.DesktopLayer, true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, 期望 %v", c.name, c.got, c.want)
		}
	}
}

// TestConfigUnknownFieldsIgnored 未来版本新增字段时，旧版本不会因解析失败而重置全部设置。
func TestConfigUnknownFieldsIgnored(t *testing.T) {
	raw := `{"color": "#ABCDEF", "some_future_field": 123}`
	cfg := defaultConfig()
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		t.Fatalf("未知字段不应导致失败: %v", err)
	}
	if cfg.Color != "#ABCDEF" {
		t.Errorf("color = %s, 期望 #ABCDEF", cfg.Color)
	}
	if cfg.FontSize != 48 {
		t.Errorf("缺失字段应保留默认值, 实际 font_size=%d", cfg.FontSize)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	want := defaultConfig()
	want.Color = "#123456"
	want.FontSize = 36
	want.Italic = true
	want.X, want.Y = -20, 800

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var got Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if *want != got {
		t.Errorf("往返后不一致:\n want=%+v\n  got=%+v", *want, got)
	}
}

func TestDefaultConfigSane(t *testing.T) {
	c := defaultConfig()
	if c.HourFormat != "24" {
		t.Errorf("默认应为 24 小时制, 实际 %s", c.HourFormat)
	}
	if !c.DesktopLayer {
		t.Error("默认应在桌面层")
	}
	if !c.ClickThrough {
		t.Error("默认应锁定(鼠标穿透)")
	}
	if c.FontSize <= 0 {
		t.Errorf("字号必须为正, 实际 %d", c.FontSize)
	}
}

// ---------- 时间格式 ----------

func TestFormatTime(t *testing.T) {
	cases := []struct {
		name   string
		t      time.Time
		format string
		want   string
	}{
		{"24制 下午", time.Date(2026, 11, 1, 15, 4, 5, 0, time.Local), "24", "15:04:05"},
		{"24制 零点", time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local), "24", "00:00:00"},
		{"12制 下午", time.Date(2026, 11, 1, 15, 4, 5, 0, time.Local), "12", "下午 03:04:05"},
		{"12制 上午", time.Date(2026, 11, 1, 9, 4, 5, 0, time.Local), "12", "上午 09:04:05"},
		{"12制 正午边界", time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local), "12", "下午 12:00:00"},
		{"12制 零点边界", time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local), "12", "上午 12:00:00"},
	}
	for _, c := range cases {
		if got := formatTime(c.t, c.format); got != c.want {
			t.Errorf("%s: formatTime(%s) = %q, 期望 %q", c.name, c.format, got, c.want)
		}
	}
}

// ---------- 颜色 ----------

func TestColorRefRoundTrip(t *testing.T) {
	for _, hex := range []string{"#39FF14", "#000000", "#FFFFFF", "#00FFFF", "#123456"} {
		r, g, b := parseColor(hex)
		if got := colorRefToHex(toColorRef(r, g, b)); got != hex {
			t.Errorf("往返 %s -> %s", hex, got)
		}
	}
}

// TestColorRefByteOrder COLORREF 是 0x00BBGGRR，与直觉的 RGB 相反，这条钉死字节序。
func TestColorRefByteOrder(t *testing.T) {
	// 纯红 RGB(255,0,0) => COLORREF 0x000000FF
	if got := toColorRef(255, 0, 0); got != 0x0000FF {
		t.Errorf("纯红 COLORREF = 0x%06X, 期望 0x0000FF", got)
	}
	// 纯蓝 RGB(0,0,255) => COLORREF 0x00FF0000
	if got := toColorRef(0, 0, 255); got != 0xFF0000 {
		t.Errorf("纯蓝 COLORREF = 0x%06X, 期望 0xFF0000", got)
	}
}

func TestParseColorInvalidFallsBack(t *testing.T) {
	for _, bad := range []string{"", "not-a-color", "#12345", "#GGGGGG"} {
		r, g, b := parseColor(bad)
		if r != 0x39 || g != 0xFF || b != 0x14 {
			t.Errorf("非法输入 %q 应回退为默认绿, 实际 (%d,%d,%d)", bad, r, g, b)
		}
	}
}

// TestColorKeyNotBlack 色键若为纯黑，与黑背景混合会留边；必须非黑。
func TestColorKeyNotBlack(t *testing.T) {
	if keyR == 0 && keyG == 0 && keyB == 0 {
		t.Error("透明色键不能是纯黑")
	}
}

// ---------- Win32 结构体尺寸（运行期双保险，编译期已断言一遍） ----------

func TestStructSizes(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"WNDCLASSEXW", unsafe.Sizeof(WNDCLASSEXW{}), 80},
		{"LOGFONTW", unsafe.Sizeof(LOGFONTW{}), 92},
		{"CHOOSECOLORW", unsafe.Sizeof(CHOOSECOLORW{}), 72},
		{"CHOOSEFONTW", unsafe.Sizeof(CHOOSEFONTW{}), 104},
		{"NOTIFYICONDATAW", unsafe.Sizeof(NOTIFYICONDATAW{}), 976},
		{"PAINTSTRUCT", unsafe.Sizeof(PAINTSTRUCT{}), 72},
		{"WINDOWPOS", unsafe.Sizeof(WINDOWPOS{}), 40},
		{"MSG", unsafe.Sizeof(MSG{}), 48},
		{"RECT", unsafe.Sizeof(RECT{}), 16},
		{"POINT", unsafe.Sizeof(POINT{}), 8},
		{"SIZE", unsafe.Sizeof(SIZE{}), 8},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s 大小 = %d, 期望 %d（Win32 结构体错位会静默失败）", c.name, c.got, c.want)
		}
	}
}

// TestLOGFONTFaceNameOffset 字体名字段必须在偏移 28，否则设置的字体会被 Win32 读错。
func TestLOGFONTFaceNameOffset(t *testing.T) {
	var lf LOGFONTW
	off := uintptr(unsafe.Pointer(&lf.LfFaceName)) - uintptr(unsafe.Pointer(&lf))
	if off != 28 {
		t.Errorf("lfFaceName 偏移 = %d, 期望 28", off)
	}
}

// ---------- 句柄 / 符号扩展 ----------

// TestNegPtr 负常量转 uintptr 必须符号扩展满 64 位，否则 SetWindowLongPtr 会传入错误索引。
func TestNegPtr(t *testing.T) {
	if got := negPtr(-1); got != ^uintptr(0) {
		t.Errorf("negPtr(-1) = 0x%X, 期望 0xFFFFFFFFFFFFFFFF", got)
	}
	if got := negPtr(-20); got != 0xFFFFFFFFFFFFFFEC {
		t.Errorf("negPtr(-20) = 0x%X, 期望 0xFFFFFFFFFFFFFFEC", got)
	}
}

// TestHKeyCurrentUser HKEY_CURRENT_USER 是 32 位负数扩展，写错会 ERROR_INVALID_HANDLE。
func TestHKeyCurrentUser(t *testing.T) {
	if hkeyCurrentUser != 0xFFFFFFFF80000001 {
		t.Errorf("HKEY_CURRENT_USER = 0x%X, 期望 0xFFFFFFFF80000001", hkeyCurrentUser)
	}
}

// ---------- 字符串 / UTF-16 ----------

func TestCopyUTF16(t *testing.T) {
	var buf [32]uint16
	copyUTF16(buf[:], "Consolas")
	if got := utf16ToString(buf[:]); got != "Consolas" {
		t.Errorf("往返 = %q, 期望 Consolas", got)
	}

	// 超长必须截断到 len-1，且保证 NUL 结尾
	var small [4]uint16
	copyUTF16(small[:], "ABCDEFG")
	if got := utf16ToString(small[:]); got != "ABC" {
		t.Errorf("截断结果 = %q, 期望 ABC", got)
	}
	if small[3] != 0 {
		t.Error("末位必须是 NUL")
	}
}

func TestCopyUTF16Chinese(t *testing.T) {
	var buf [32]uint16
	copyUTF16(buf[:], "微软雅黑")
	if got := utf16ToString(buf[:]); got != "微软雅黑" {
		t.Errorf("中文往返 = %q", got)
	}
}

// ---------- 常量一致性 ----------

func TestMenuIDsUnique(t *testing.T) {
	ids := []int{idColor, idFont, idToggleFormat, idToggleLock,
		idToggleAutostart, idExit, idToggleLayer}
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			t.Errorf("菜单 ID %d 重复", id)
		}
		seen[id] = true
	}
}

func TestRegPath(t *testing.T) {
	if regPath != `Software\Microsoft\Windows\CurrentVersion\Run` {
		t.Errorf("自启注册表路径错误: %s", regPath)
	}
	if regName != "DesktopClock" {
		t.Errorf("注册表值名错误: %s", regName)
	}
}

// TestExePathAbs exe 路径必须是绝对路径，否则注册表自启会写成相对路径而失效。
func TestExePathAbs(t *testing.T) {
	p := exePath()
	if p == "" {
		t.Fatal("exePath 为空")
	}
	if !filepath.IsAbs(p) {
		t.Errorf("exePath 不是绝对路径: %s", p)
	}
}

func TestConfigPathBesideExe(t *testing.T) {
	cp := configPath()
	if filepath.Base(cp) != "config.json" {
		t.Errorf("配置文件名 = %s", filepath.Base(cp))
	}
	if filepath.Dir(cp) != filepath.Dir(exePath()) {
		t.Errorf("配置应与 exe 同目录: %s vs %s", filepath.Dir(cp), filepath.Dir(exePath()))
	}
}
