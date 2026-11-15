package main

import (
	"encoding/json"
	"os"
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
	if c.Opacity != defaultOpacity {
		t.Errorf("默认不透明度应为 %d, 实际 %d", defaultOpacity, c.Opacity)
	}
	// 出厂值不能是 100：那等于没有半透明效果；也不能太低，深色壁纸上会看不清
	if c.Opacity < 60 || c.Opacity >= 100 {
		t.Errorf("默认不透明度 %d 超出合理区间 [60,100)", c.Opacity)
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

// ---------- 逐像素 alpha ----------

// TestPremultipliedPixel 锁定「覆盖率 → 预乘 alpha 像素」的算法。
//
// 两条不能破的底线：
//  1. 没被笔画碰到的像素必须全 0（alpha=0 且 RGB=0）。分层窗口要求 RGB 预乘，
//     留个非零 RGB 会在边缘透出一圈彩色；
//  2. 完全不透明时必须还原成原色，否则用户选的颜色会被画错。
func TestPremultipliedPixel(t *testing.T) {
	// 覆盖率 0：四个字节都得是 0
	if R, G, B, A := premultipliedPixel(0, 0x39, 0xFF, 0x14, 85); R != 0 || G != 0 || B != 0 || A != 0 {
		t.Errorf("零覆盖率应为全透明, 实际 R=%d G=%d B=%d A=%d", R, G, B, A)
	}

	// 满覆盖率 + 100%：原色原样返回
	if R, G, B, A := premultipliedPixel(255, 0x39, 0xFF, 0x14, 100); R != 0x39 || G != 0xFF || B != 0x14 || A != 255 {
		t.Errorf("满覆盖率+全不透明应为 (57,255,20,255), 实际 (%d,%d,%d,%d)", R, G, B, A)
	}

	// 满覆盖率 + 50%：alpha 减半，RGB 同比预乘（不是只改 alpha）
	R, G, B, A := premultipliedPixel(255, 0x39, 0xFF, 0x14, 50)
	if A != 128 { // (255*50+50)/100 = 128，四舍五入而非截断成 127
		t.Errorf("50%% 不透明度 alpha = %d, 期望 128", A)
	}
	if R != byte(0x39*128/255) || G != byte(0xFF*128/255) || B != byte(0x14*128/255) {
		t.Errorf("RGB 必须按 alpha 预乘, 实际 (%d,%d,%d)", R, G, B)
	}

	// 单调性：覆盖率越大，alpha 不能反而变小（边缘渐变不能出现断层）
	prev := -1
	for cov := 0; cov <= 255; cov++ {
		_, _, _, a := premultipliedPixel(byte(cov), 0x39, 0xFF, 0x14, 85)
		if int(a) < prev {
			t.Fatalf("覆盖率 %d 的 alpha=%d 小于前一档 %d", cov, a, prev)
		}
		prev = int(a)
	}
}

// TestClampOpacity 不透明度写错时退回默认值，而不是画出看不见的文字。
func TestClampOpacity(t *testing.T) {
	cases := []struct{ in, want int }{
		{85, 85},
		{1, 1},
		{100, 100},
		{0, defaultOpacity},   // 0 会让文字彻底消失
		{-20, defaultOpacity}, // 负数同理
		{101, defaultOpacity},
		{1000, defaultOpacity},
	}
	for _, c := range cases {
		if got := clampOpacity(c.in); got != c.want {
			t.Errorf("clampOpacity(%d) = %d, 期望 %d", c.in, got, c.want)
		}
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
		{"BITMAPINFOHEADER", unsafe.Sizeof(BITMAPINFOHEADER{}), 40},
		{"BLENDFUNCTION", unsafe.Sizeof(BLENDFUNCTION{}), 4},
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
	if HKEY_CURRENT_USER != 0xFFFFFFFF80000001 {
		t.Errorf("HKEY_CURRENT_USER = 0x%X, 期望 0xFFFFFFFF80000001", HKEY_CURRENT_USER)
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

// TestAcquireMutexSingleInstance 验证命名互斥体的互斥语义：
// 第一次拿得到，第二次（模拟另一个进程）拿不到。
//
// 用测试专属的名字，避免和正在运行的时钟抢同一个互斥体 ——
// 否则用户开着时钟时跑测试会假失败。
func TestAcquireMutexSingleInstance(t *testing.T) {
	name := `Local\DesktopClockTestMutex`
	if !acquireMutex(name) {
		t.Fatal("首次获取互斥体应当成功")
	}
	if acquireMutex(name) {
		t.Error("同名互斥体第二次获取应当返回 false（表示已有实例）")
	}
}

// TestSameAutostartPath 锁定自启项路径比较的容错语义。
// 回归背景：程序目录改名后，注册表里的旧路径让开机自启静默失灵 ——
// 菜单仍显示已勾选，指向的文件却已不存在。自我修正依赖这个比较函数，
// 引号、大小写、首尾空格都不能让它误判成"还是同一路径"。
func TestSameAutostartPath(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		same bool
	}{
		{name: "完全一致", a: `C:/A/X.exe`, b: `C:/A/X.exe`, same: true},
		{name: "大小写不同也算同一处", a: `C:/A/X.EXE`, b: `c:/a/x.exe`, same: true},
		{name: "带引号 vs 不带", a: `"C:/A/X.exe"`, b: `C:/A/X.exe`, same: true},
		{name: "首尾空格", a: `  C:/A/X.exe `, b: `C:/A/X.exe`, same: true},
		{name: "目录改名后是不同路径", a: `C:/Old/X.exe`, b: `C:/New/X.exe`, same: false},
		{name: "不同文件名", a: `C:/A/X.exe`, b: `C:/A/Y.exe`, same: false},
	}
	for _, c := range cases {
		if got := sameAutostartPath(c.a, c.b); got != c.same {
			t.Errorf("%s: sameAutostartPath(%q, %q) = %v, want %v", c.name, c.a, c.b, got, c.same)
		}
	}
}

// TestEnsureConfigFile 锁定「配置缺失时生成默认值、已存在时绝不覆盖」。
// 生成的目的是让用户有文件可改；不覆盖则守住一条底线：配置文件属于用户。
func TestEnsureConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if !ensureConfigFile(path, defaultConfig()) {
		t.Fatal("配置缺失时应生成默认值")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("生成后应能读到文件: %v", err)
	}

	// 第二次调用不应再写：已有配置一律不动
	if ensureConfigFile(path, defaultConfig()) {
		t.Error("配置已存在时不应重写")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Error("已存在的配置内容被改动了")
	}

	// 生成出来的内容必须能被 loadConfig 解析回同样的字段
	var got Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("生成的配置应为合法 JSON: %v", err)
	}
	want := defaultConfig()
	if got.Color != want.Color || got.FontFamily != want.FontFamily || got.FontSize != want.FontSize {
		t.Errorf("默认值不符: %+v", got)
	}
}
