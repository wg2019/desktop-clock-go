@echo off
chcp 65001 >nul
REM ============================================================
REM  桌面时钟（Go / Win32 版）一键构建
REM  纯 Go 直接调 Win32 API，零第三方依赖，静态单文件产物
REM ============================================================

setlocal
set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0

where go >nul 2>nul
if errorlevel 1 (
    echo [错误] 找不到 go 命令，请先把 Go 的 bin 目录加入 PATH
    pause
    exit /b 1
)

REM 图标资源（.syso）不存在时自动生成；需要 go-winres 工具
if not exist rsrc_windows_amd64.syso (
    if not exist icon.ico (
        echo [错误] 缺少 icon.ico，无法生成图标资源
        pause
        exit /b 1
    )
    echo [1/3] 安装 go-winres ...
    go install github.com/tc-hib/go-winres@latest
    if errorlevel 1 (
        echo [错误] go-winres 安装失败（检查网络或 GOPROXY）
        pause
        exit /b 1
    )
    echo [2/3] 生成图标资源 ...
    go-winres simply --icon icon.ico --manifest gui --product-name "桌面时钟" --product-version "1.0.0.0" --file-version "1.0.0.0" --file-description "Windows 桌面时钟"
    if errorlevel 1 (
        echo [错误] 资源生成失败
        pause
        exit /b 1
    )
) else (
    echo [1/2] 图标资源已存在，跳过生成
)

echo [3/3] 编译 ...
REM -H windowsgui : GUI 子系统，双击不弹黑框
REM -s -w         : 去掉符号表和调试信息，体积减约 30%%
go build -trimpath -ldflags "-H windowsgui -s -w" -o DesktopClockGo.exe .
if errorlevel 1 (
    echo.
    echo [错误] 编译失败
    pause
    exit /b 1
)

echo.
echo 构建完成: DesktopClockGo.exe
for %%F in (DesktopClockGo.exe) do echo 文件大小: %%~zF 字节
echo.
echo 提示: 首次运行会在 exe 同目录生成 config.json
pause
