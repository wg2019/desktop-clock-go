@echo off
chcp 936 >nul
REM ============================================================
REM  桌面时钟：Go + Win32，一条命令编译
REM  用 Go 直接调 Win32 API，不依赖第三方库，产物是单个静态文件
REM ============================================================

setlocal
set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0

REM 双击运行时结尾暂停，方便看结果；package.bat 调用时传 nopause
set "NOPAUSE="
if /i "%~1"=="nopause" set "NOPAUSE=1"

REM 版本号只有一处：packaging\version.txt
set "VERSION=1.0.0"
if exist "packaging\version.txt" (
    for /f "usebackq delims=" %%V in ("packaging\version.txt") do set "VERSION=%%V"
)

REM Go 不一定装在默认位置。这里先按常见路径补 PATH，找不到再报错
if not defined GOROOT (
    if exist "%USERPROFILE%\Documents\go\bin\go.exe" set "PATH=%USERPROFILE%\Documents\go\bin;%PATH%"
    if exist "C:\Go\bin\go.exe" set "PATH=C:\Go\bin;%PATH%"
    if exist "%LOCALAPPDATA%\Programs\Go\bin\go.exe" set "PATH=%LOCALAPPDATA%\Programs\Go\bin;%PATH%"
)

where go >nul 2>nul
if errorlevel 1 (
    echo [错误] 找不到 go 命令。
    echo        请把 Go 的 bin 目录加入 PATH，或把 Go 解压到以下任一位置：
    echo          %%USERPROFILE%%\Documents\go
    echo          C:\Go
    echo          %%LOCALAPPDATA%%\Programs\Go
    if not defined NOPAUSE pause
    exit /b 1
)

REM 图标资源（.syso）只在缺失时生成，需要 go-winres
if not exist rsrc_windows_amd64.syso (
    if not exist icon.ico (
        echo [错误] 缺少 icon.ico，无法生成图标资源
        if not defined NOPAUSE pause
        exit /b 1
    )
    echo [图标] 安装 go-winres ...
    go install github.com/tc-hib/go-winres@latest
    if errorlevel 1 (
        echo [错误] go-winres 安装失败，请检查网络或 GOPROXY
        if not defined NOPAUSE pause
        exit /b 1
    )
    echo [图标] 生成资源文件（版本 %VERSION%）...
    go-winres simply --icon icon.ico --manifest none --product-name "桌面时钟" --product-version "%VERSION%.0" --file-version "%VERSION%.0" --file-description "Windows 桌面时钟"
    if errorlevel 1 (
        echo [错误] 资源生成失败
        if not defined NOPAUSE pause
        exit /b 1
    )
) else (
    echo [图标] 资源已存在，跳过
)

echo [编译] 开始编译 ...
REM -H windowsgui : GUI 子系统，双击不弹控制台黑框
REM -s -w         : 去掉符号表和调试信息，体积小约 30%%
go build -trimpath -ldflags "-H windowsgui -s -w" -o DesktopClockGo.exe .
if errorlevel 1 (
    echo.
    echo [错误] 编译失败
    if not defined NOPAUSE pause
    exit /b 1
)

echo.
echo 编译完成: DesktopClockGo.exe   版本 %VERSION%
for %%F in (DesktopClockGo.exe) do echo 文件大小: %%~zF 字节
echo.
if not defined NOPAUSE pause
