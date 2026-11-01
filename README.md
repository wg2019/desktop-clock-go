# 桌面时钟

Windows 11 桌面时钟。贴在桌面上、永远位于所有应用窗口之下，点击不抢焦点。

**纯 Go + 直接调 Win32 API，零第三方依赖**（连 `golang.org/x/sys` 都不用），
`CGO_ENABLED=0` 静态编译，产物是单个 exe，离线可构建。

## 设计取向

不引入第三方库，所有 Win32 调用手写声明：

- 单文件 3.23 MB，静态链接，不依赖任何运行库
- 启动约 50 ms，常驻内存 16 MB
- 配置写在 exe 同目录，自启项只写 `HKCU`，不碰系统目录，也不解压到 `%TEMP%`
- 结构体布局用**编译期断言**校验（`uint(unsafe.Sizeof(x)) - 80`），
  字段对不上是编译错误，而不是运行时静默失败

## 功能

| 需求 | 实现 |
|---|---|
| 12/24 小时切换 | 托盘右键菜单「12 小时制」，窗口自动变宽 |
| 改颜色 / 字体 | 系统原生 `ChooseColorW` / `ChooseFontW`（含斜体） |
| 开机自启 | `HKCU\...\Run`，托盘菜单可开关 |
| 拖动 + 锁定 | 取消「锁定(鼠标穿透)」后可拖动；开启后点击穿透到桌面 |
| 桌面层 | 贴在桌面上、压在所有应用之下，点它不抢焦点 |

## 文件结构

```
desktop-clock-go/
├── go.mod
├── main.go        入口：LockOSThread + 消息循环 + wndProc        ~120 行
├── win32.go       DLL/常量/结构体声明 + 编译期尺寸断言          ~320 行
├── config.go      JSON 配置 + 注册表自启（advapi32）+ 颜色工具   ~160 行
├── clock.go       窗口/字体/绘制/桌面层级/拖动                  ~260 行
├── tray.go        托盘图标 + 右键菜单 + 命令分发                ~110 行
├── dialog.go      ChooseColorW / ChooseFontW                   ~90 行
├── clock_test.go  单元测试（19 个用例）                        ~280 行
├── icon.ico       图标源（也用于托盘）
├── rsrc_windows_amd64.syso   编译进 exe 的图标资源（go-winres 生成）
└── DesktopClockGo.exe        产物
```

## 构建

```bash
# 一次性：生成图标资源（之后 go build 会自动链接 .syso）
go install github.com/tc-hib/go-winres@latest
go-winres simply --icon icon.ico --manifest gui \
    --product-name "桌面时钟" --product-version "1.0.0.0" \
    --file-version "1.0.0.0" --file-description "Windows 桌面时钟"

# 编译
CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w" -o DesktopClockGo.exe .
```

或直接双击 `build.bat`。

`-H windowsgui` 必不可少：否则生成的控制台子系统程序双击会附带一个黑框窗口。

## 测试

```bash
go test -v ./...     # 19 个用例
```

覆盖：配置解析与往返一致性、未知字段兼容、12/24 制边界（0 点/12 点）、
颜色 COLORREF 字节序、非法颜色回退、Win32 结构体尺寸、`lfFaceName` 偏移、
负常量符号扩展、`HKEY_CURRENT_USER` 值、UTF-16 截断、菜单 ID 唯一性、exe 路径绝对性。

## 实现要点（Win32 + Go 的三个坑）

### 1. 窗口消息是「线程绑定」的

Windows 只把消息投递给**创建该窗口的那个 OS 线程**。Go 的 goroutine 会被调度到不同
线程上，所以必须在 `main` 里 `runtime.LockOSThread()` 把整个消息循环钉死在一个线程。

漏了这句：窗口能出来，但鼠标点不动、托盘菜单弹不开、定时器不触发，**而且完全不报错**。

### 2. 结构体字段错一个，静默失败

`WNDCLASSEXW` 在 64 位下必须正好 80 字节。写成 79 或 84，`RegisterClassExW` 不会报错，
只是返回 0，窗口根本不出现。

对策是 `win32.go` 末尾的编译期断言：

```go
const (
	_ = uint(unsafe.Sizeof(WNDCLASSEXW{}) - 80)
	_ = uint(80 - unsafe.Sizeof(WNDCLASSEXW{}))
)
```

断言两遍（多也报错、少也报错），字段一改错就**编译不过**，而不是上线后排查半天。

### 3. 桌面层不能用 HWND_BOTTOM

`SetWindowPos(hwnd, HWND_BOTTOM, ...)` 会把窗口沉到 `Progman`（桌面窗口）**之下**，
结果是被壁纸盖住、画面上完全看不见 —— z 序"正确"但人眼看不到，极难判断。

正确锚点是 **`Progman` 正上方那个窗口**（`GetWindow(Progman, GW_HWNDPREV)`），
插在它之后 = 紧贴桌面之上、所有应用之下。

另外 `Win+D` / 右键"显示桌面"会把 `Progman` 抬到前台把时钟挤走，所以每秒自检一次
（`isAnchored` 会跳过 z 序里夹着的隐藏 IME 辅助窗口，避免误判），被挤走就重新落位。

## 托盘菜单

- 选择颜色… / 选择字体…
- 12 小时制（勾选）
- 锁定(鼠标穿透)（勾选）
- 只在桌面显示(不遮挡窗口)（勾选，取消则变回传统置顶悬浮）
- 开机自启动（勾选）
- 退出

## 配置

与 exe 同目录的 `config.json`：

```json
{
  "hour_format": "24",
  "color": "#00FFFF",
  "font_family": "Consolas",
  "font_size": 48,
  "font_weight": "bold",
  "italic": false,
  "x": 60,
  "y": 60,
  "autostart": false,
  "click_through": true,
  "desktop_layer": true
}
```

## 测试与验收

```bash
go test ./...     # 19 个单元测试：配置兼容性、时间格式化、颜色字节序、
                  # 结构体尺寸、UTF-16 转换、注册表路径、菜单 ID 唯一性等
go vet ./...      # 仅 1 条提示，见下方说明
gofmt -l .        # 应无输出
```

**关于 `go vet` 的那条 `possible misuse of unsafe.Pointer`（clock.go 的
`onWindowPosChanging`）**：这是 Win32 消息回调的固有写法。`lparam` 指向的是
系统传入的 `WINDOWPOS`，不是 Go 堆对象，指针不逃逸、只在回调内读写，因此安全。
任何等价写法（`unsafe.Add`、`unsafe.Slice`）都躲不过 vet 的 `unsafeptr` 静态检查，
代码里已就地注释说明。

功能回归验证思路（当时用一个临时小工具完成，工具未入库，方法可复用）：
向时钟窗口 `PostMessage(hwnd, WM_COMMAND, <菜单ID>, 0)` 模拟点击菜单，
再用 `GetWindowLong(GWL_EXSTYLE)` / `GetWindowRect` 读回扩展样式与尺寸来断言，
全程不抢焦点、不弹窗、不影响前台程序。

菜单 ID：`101` 颜色 · `102` 字体 · `103` 12小时制 · `104` 鼠标穿透 ·
`105` 开机自启 · `106` 退出 · `107` 桌面层

验收记录（2026-11-01）：

| 项目 | 结果 |
| --- | --- |
| 单元测试 | 19/19 通过 |
| 24→12 小时制 | 窗口 248→393px，还原正常 |
| 鼠标穿透开关 | 扩样式 `TRANSPARENT` 正确增删 |
| 桌面层开关 | 关闭后变 `TOPMOST` 悬浮，开启后回到 `NOACTIVATE` 底层 |
| 开机自启开关 | 注册表项正确增删 |
| 静置 8 秒 | 位置/尺寸/样式无漂移 |
| 退出 | 进程干净结束 |
| 正式构建 | 3.2MB，Subsystem=2(GUI)，无控制台窗口 |

## 许可证

MIT，见 [LICENSE](LICENSE)。可自由使用、修改、分发，包括商用；保留版权声明即可。

Copyright 行目前写的是 "DesktopClock Contributors"，如果你想署自己的名字，
改 LICENSE 第一段的 `Copyright (c) 2026 ...` 那一行即可。
