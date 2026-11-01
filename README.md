# 桌面时钟

Windows 11 桌面时钟。贴在桌面上、永远位于所有应用窗口之下，点击不抢焦点。

**纯 Go + 直接调 Win32 API，零第三方依赖**（连 `golang.org/x/sys` 都不用），
`CGO_ENABLED=0` 静态编译，产物是单个 exe，离线可构建。

> **关于 "Win32" 与 "64 位"**：本项目编译目标是 64 位 Windows
> （`GOARCH=amd64`，PE 头 `Machine = 0x8664`、`PE32+`）。Win32 是 Windows
> 原生接口**家族的名字**——从 1993 年 Windows NT 起就叫这个，64 位程序用的
> 是同一套 API，`user32.dll` / `gdi32.dll` 里的 "32" 同样是 16→32 位年代
> 的遗留命名。两者并不矛盾，也不表示这是 32 位程序。

## 设计取向

不引入第三方库，所有 Win32 调用手写声明：

- 单文件约 3.1 MB（3,227,136 字节），静态链接，不依赖任何运行库
- 启动约 50 ms，常驻内存约 16 MB
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
| 防重复启动 | 命名互斥体（`Local\DesktopClockSingleInstance`），第二个实例提示后自行退出 |
| 托盘记录自愈 | 启动时删掉指向已删除文件的旧登记记录；发现仍有其他副本时置顶提示 |
| 托盘图标自愈 | 监听 `TaskbarCreated`，资源管理器重启后自动把图标登记回去 |

## 文件结构

```
desktop-clock-go/
├── go.mod                     module desktopclock（无第三方依赖）
├── main.go                    入口：LockOSThread + 单实例检查 + 消息循环 + wndProc   225 行
├── win32.go                   DLL/常量/结构体声明 + 编译期尺寸断言                   423 行
├── config.go                  JSON 配置 + 注册表自启（advapi32）+ 颜色工具           200 行
├── clock.go                   窗口/字体/绘制/桌面层级/拖动/自检                       408 行
├── tray.go                    托盘图标 + 右键菜单 + 命令分发                          150 行
├── trayrecords.go             托盘登记记录的清理与副本检测                            199 行
├── dialog.go                  ChooseColorW / ChooseFontW                              85 行
├── clock_test.go              单元测试（20 个用例）                                  311 行
├── trayrecords_test.go        记录分类逻辑的单测（3 个用例 + 子测试）                 107 行
├── build.bat                  一键构建（图标资源 + 正式参数编译）
├── icon.ico                   图标源文件（托盘与 exe 共用）
├── rsrc_windows_amd64.syso    编译进 exe 的图标与版本资源（go-winres 生成）
├── config.example.json        配置模板（仓库不含本机实际配置）
├── .gitignore / .gitattributes / LICENSE
│
└── DesktopClockGo.exe         构建产物，不入库
```

源码共约 1690 行（不含测试），测试 418 行。

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
go test -v ./...     # 23 个用例
```

覆盖：配置解析与往返一致性、未知字段兼容、12/24 制边界（0 点/12 点）、
颜色 COLORREF 字节序、非法颜色回退、Win32 结构体尺寸、`lfFaceName` 偏移、
负常量符号扩展、`HKEY_CURRENT_USER` 值、UTF-16 截断、菜单 ID 唯一性、
exe 路径绝对性、单实例互斥体语义、托盘记录分类（自己/副本/死记录）。

## 实现要点（Win32 + Go 的几个坑）

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

### 4. 进程内的变量挡不住第二次双击

`sync.Mutex` 或包级变量只在**当前进程**内有效。用户双击两次是两个进程，各自持有
自己的变量，谁也拦不住谁 —— 结果是两个时钟叠在同一位置、两个托盘图标，退出其中一个
另一个还在。

跨进程互斥只能交给操作系统：`CreateMutexW` 建一个命名互斥体，第二个进程拿到
`ERROR_ALREADY_EXISTS` 就说明已有实例在跑。名字加 `Local\` 前缀，把作用域限定在当前
登录会话，不同用户各开一个互不干扰 —— 和自启项写在 `HKCU` 的隔离粒度保持一致。

注意别在测试里直接用生产环境的互斥体名：用户正开着时钟时，测试会「首次获取即失败」
而假红。所以 `acquireMutex` 把名字作为参数，测试传自己的名字。

### 5. 托盘图标记录按 exe 完整路径存，换个目录就多一条

这一点不在 Win32 API 文档里，但属于同一类问题：系统行为、静默发生、事后极难说清。

Win11 把「托盘里有哪些图标、是否常显」记在
`HKCU\Control Panel\NotifyIconSettings` 下，**每条记录对应一个 exe 的完整路径**。
后果是：

- 同一个程序换个目录运行一次，「设置 > 个性化 > 任务栏 > 其他系统托盘图标」里
  就多出一条同名条目；
- 原目录被删掉之后那条记录仍然留着，**永远不会自己消失**（实测清理前累积了 4 条，
  其中 3 条指向已被删除的目录）；
- 系统按路径区分身份，所以程序自己也无法让它们合并成一条。

`trayrecords.go` 因此在每次启动时做两件事：

1. **删死记录**：exe 文件名与本程序相同、但对应文件已不存在的记录，直接删掉
   （`RegEnumKeyExW` 枚举 → `RegQueryValueExW` 读 `ExecutablePath` →
   `RegDeleteKeyW`）。别的程序的记录一律不碰。
2. **检测活副本**：文件仍在、但不是当前这份的记录，说明机器上还有另一个副本在跑，
   弹一个**置顶**提示把路径列出来，让用户自己决定删哪个。

两个实现细节值得记：

- 枚举时**先收集全部子键名再删**。边枚举边删会让后续索引整体前移，漏掉条目。
- 提示框必须带 `MB_TOPMOST | MB_SETFOREGROUND`。开机自启拉起的进程没有前台权限，
  普通 MessageBox 会被前台窗口盖住 —— 第一次实测时就踩到了，截图里完全看不到弹窗。

### 6. explorer 重启会清空所有人的托盘图标

托盘图标不是程序自己画的，是**登记给任务栏**的。任务栏属于 `explorer.exe`，它一退出
（崩溃恢复、注销重登、装完软件自动重启、手工"重新启动资源管理器"），那些登记就全没了。

系统会在这时候广播一条注册消息 **`TaskbarCreated`**，标准做法是收到后重新
`Shell_NotifyIcon(NIM_ADD)` 一次。漏了这一步的现象是：**程序明明还在跑、时钟也还在画，
托盘图标却凭空消失，直到重启程序才回来** —— 而且完全没有报错可查。

```go
// 注册消息的数值由系统动态分配，只能运行期取，不能写成 case 常量
taskbarCreatedMsg, _, _ = pRegisterWindowMessageW.Call(
    uintptr(unsafe.Pointer(utf16Ptr("TaskbarCreated"))))
```

`wndProc` 里在 `switch` 之前拦这条消息（它的值不是编译期常量，进不了 `case`），
命中就重新登记。登记前先 `NIM_DELETE` 再 `NIM_ADD`，重复调用才不会因为"图标已存在"失败；
失败则挂一个 1.5 秒的定时器重试（explorer 刚起来时任务栏还没准备好），连续失败才弹窗。

顺带一个不能靠代码解决的坑：**explorer 自己的托盘状态也会卡死**。这时新加的任何图标都
不会出现，连任务栏上的"^"折叠按钮都没有，`Shell_NotifyIcon` 却返回成功、注册表里也照常
写下记录 —— 只能重启 explorer 恢复。排查时用截图对比最可靠：加图标前后各截一张任务栏。

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
  "color": "#39FF14",
  "font_family": "Consolas",
  "font_size": 48,
  "font_weight": "bold",
  "italic": false,
  "x": 100,
  "y": 100,
  "autostart": false,
  "click_through": true,
  "desktop_layer": true
}
```

上表即 `defaultConfig()` 的值：配置文件不存在时按这套生成。字段含义：

| 字段 | 说明 |
| --- | --- |
| `hour_format` | `"24"` 或 `"12"` |
| `color` | `#RRGGBB`，格式非法时回退到 `#39FF14` |
| `font_size` | 字符高度，单位像素 |
| `x` / `y` | 窗口左上角坐标，拖动后会写回 |
| `autostart` | 启动时以注册表实际状态为准，这里只是落盘记录 |

## 测试与验收

```bash
go test ./...     # 23 个单元测试：配置兼容性、时间格式化、颜色字节序、
                  # 结构体尺寸、UTF-16 转换、注册表路径、菜单 ID 唯一性等
go vet ./...      # 仅 1 条提示，见下方说明
gofmt -l .        # 应无输出
```

**关于 `go vet` 的那条 `possible misuse of unsafe.Pointer`（clock.go 的
`onWindowPosChanging`）**：这是 Win32 消息回调的固有写法。`lparam` 指向的是
系统传入的 `WINDOWPOS`，不是 Go 堆对象，指针不逃逸、只在回调内读写，因此安全。
任何等价写法（`unsafe.Add`、`unsafe.Slice`）都躲不过 vet 的 `unsafeptr` 静态检查，
代码里已就地注释说明。

功能回归的复现方法（用的是临时小工具，未入库，思路可照搬）：
向时钟窗口 `PostMessage(hwnd, WM_COMMAND, <菜单ID>, 0)` 模拟点击菜单，
再用 `GetWindowLong(GWL_EXSTYLE)` / `GetWindowRect` 读回扩展样式与尺寸来断言，
全程不抢焦点、不弹窗、不影响前台程序。

菜单 ID：`101` 颜色 · `102` 字体 · `103` 12小时制 · `104` 鼠标穿透 ·
`105` 开机自启 · `106` 退出 · `107` 桌面层

验收记录（2026-11-01，Windows 11 26H2 / build 26300）：

| 项目 | 结果 |
| --- | --- |
| 单元测试 | 23/23 通过 |
| 24→12 小时制 | 窗口 248→393px，还原正常 |
| 鼠标穿透开关 | 扩样式 `TRANSPARENT` 正确增删 |
| 桌面层开关 | 关闭后变 `TOPMOST` 悬浮，开启后回到 `NOACTIVATE` 底层 |
| 开机自启开关 | 注册表项正确增删 |
| 静置 8 秒 | 位置/尺寸/样式无漂移 |
| 防重复启动 | 第二个实例弹出提示后以退出码 0 结束，首个实例窗口不受影响 |
| 退出 | 进程干净结束 |
| 正式构建 | 3.1MB（3,227,136 字节），Subsystem=2(GUI)，无控制台窗口 |
| 死记录自愈 | 构造指向已删除文件的记录，启动后被自动删除 |
| 多副本提示 | 构造指向现存副本的记录，启动后弹出置顶提示，路径与设置位置正确 |

## 许可证

MIT，见 [LICENSE](LICENSE)。可自由使用、修改、分发，包括商用；保留版权声明即可。
