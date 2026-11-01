package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
	"unsafe"
)

// Config 对应 exe 同目录的 config.json。
// json tag 是外部契约，改名等于破坏用户已有配置，测试里锁死了字段名。
type Config struct {
	HourFormat   string `json:"hour_format"` // "24" | "12"
	Color        string `json:"color"`       // "#RRGGBB"
	FontFamily   string `json:"font_family"` // 字体名
	FontSize     int    `json:"font_size"`   // 字符高度（像素）
	FontWeight   string `json:"font_weight"` // "bold" | "normal"
	Italic       bool   `json:"italic"`      // 斜体
	X            int    `json:"x"`           // 窗口左上角
	Y            int    `json:"y"`
	Autostart    bool   `json:"autostart"`     // 开机自启（实际状态以注册表为准）
	ClickThrough bool   `json:"click_through"` // 鼠标穿透（锁定）
	// DesktopLayer: true  = 桌面层，贴在桌面上、永远被应用窗口盖住（默认）
	//               false = 传统置顶悬浮窗
	DesktopLayer bool `json:"desktop_layer"`
}

const (
	regPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	regName = "DesktopClock"

	// HKEY_CURRENT_USER 是 (HKEY)(LONG)0x80000001，64 位下需符号扩展为全 1 高位。
	// 直接写 0x80000001 会变成 0x0000000080000001，RegOpenKeyExW 会返回 ERROR_INVALID_HANDLE。
	hkeyCurrentUser uintptr = 0xFFFFFFFF80000001
)

func defaultConfig() *Config {
	return &Config{
		HourFormat:   "24",
		Color:        "#39FF14",
		FontFamily:   "Consolas",
		FontSize:     48,
		FontWeight:   "bold",
		Italic:       false,
		X:            100,
		Y:            100,
		Autostart:    false,
		ClickThrough: true,
		DesktopLayer: true,
	}
}

// exePath 返回程序真实磁盘路径。
//
// 必须是磁盘上的真实路径，不能是临时目录：配置和开机自启项都要写在这里，
// 路径一旦落在 %TEMP% 下，重启后就会指向一个已被删除的文件。
// os.Executable() 对静态链接的 Go 程序直接返回 exe 自身路径，天然满足；
// 再解一次符号链接，防止从快捷方式/软链启动时拿到的是链接路径。
func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

// configPath 配置文件与 exe 同目录：拷走整个文件夹就带走了设置。
func configPath() string {
	return filepath.Join(filepath.Dir(exePath()), "config.json")
}

// loadConfig 读配置：文件不存在 / 解析失败 / 字段缺失都退回默认值，永不报错中断启动。
func loadConfig() *Config {
	cfg := defaultConfig()
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, cfg) // 未知字段自动忽略，缺失字段保留默认
	return cfg
}

func saveConfig(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(configPath(), data, 0o644)
}

// ---------- 开机自启（HKCU\...\Run） ----------

// getAutostart 只判断自启项是否存在，不比对它的值。
// RegQueryValueExW 的 lpData 传 NULL、只给 &size，系统就只回填长度不拷贝数据。
func getAutostart() bool {
	var hKey uintptr
	ret, _, _ := pRegOpenKeyExW.Call(
		hkeyCurrentUser,
		uintptr(unsafe.Pointer(utf16Ptr(regPath))),
		0,
		KEY_QUERY_VALUE,
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != ERROR_SUCCESS {
		return false
	}
	defer pRegCloseKey.Call(hKey)

	var typ, size uint32
	ret, _, _ = pRegQueryValueExW.Call(
		hKey,
		uintptr(unsafe.Pointer(utf16Ptr(regName))),
		0,
		uintptr(unsafe.Pointer(&typ)),
		0,
		uintptr(unsafe.Pointer(&size)),
	)
	return ret == ERROR_SUCCESS
}

func setAutostart(enable bool) bool {
	var hKey uintptr
	ret, _, _ := pRegOpenKeyExW.Call(
		hkeyCurrentUser,
		uintptr(unsafe.Pointer(utf16Ptr(regPath))),
		0,
		KEY_SET_VALUE,
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != ERROR_SUCCESS {
		return false
	}
	defer pRegCloseKey.Call(hKey)

	if enable {
		if !strings.Contains(exePath(), "\\") {
			return false
		}
		value := `"` + exePath() + `"`
		u := utf16.Encode([]rune(value))
		u = append(u, 0) // 必须 NUL 结尾
		ret, _, _ = pRegSetValueExW.Call(
			hKey,
			uintptr(unsafe.Pointer(utf16Ptr(regName))),
			0,
			REG_SZ,
			uintptr(unsafe.Pointer(&u[0])),
			uintptr(len(u)*2), // 字节数，不是字符数
		)
		return ret == ERROR_SUCCESS
	}

	ret, _, _ = pRegDeleteValueW.Call(
		hKey,
		uintptr(unsafe.Pointer(utf16Ptr(regName))),
	)
	return ret == ERROR_SUCCESS || ret == ERROR_FILE_NOT_FOUND
}

// ---------- 颜色工具 ----------

// parseColor 把 "#RRGGBB" 解析为 (r,g,b)；格式非法时退回绿色，避免启动即崩。
func parseColor(s string) (int, int, int) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return 0x39, 0xFF, 0x14
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0x39, 0xFF, 0x14
	}
	return int(v >> 16 & 0xFF), int(v >> 8 & 0xFF), int(v & 0xFF)
}

// toColorRef 转 Win32 的 COLORREF（0x00BBGGRR，注意与 RGB 字节序相反）。
func toColorRef(r, g, b int) uint32 {
	return uint32(r) | uint32(g)<<8 | uint32(b)<<16
}

// colorRefToHex 从 COLORREF 转回 "#RRGGBB"。
func colorRefToHex(ref uint32) string {
	r := ref & 0xFF
	g := ref >> 8 & 0xFF
	b := ref >> 16 & 0xFF
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

// parseColorRef 便捷封装：先把 "#RRGGBB" 解析成 COLORREF。
func parseColorRef(s string) uint32 {
	r, g, b := parseColor(s)
	return toColorRef(r, g, b)
}
