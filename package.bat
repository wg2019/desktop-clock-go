@echo off
chcp 936 >nul
REM ============================================================
REM  打包：编译 → 组装 dist\DesktopClock-v<版本>-win64\ → 压成 zip
REM  素材在 packaging\ 下（进版本库），产物在 dist\ 下（不进版本库）
REM
REM  为什么要这个脚本：以前打包靠手工拼命令，素材只存在于已解压的
REM  dist 目录里，那个目录被删过一次，于是「包还在、重新打不出来」。
REM ============================================================

setlocal
cd /d "%~dp0"

set "VERSION=1.0.0"
if exist "packaging\version.txt" (
    for /f "usebackq delims=" %%V in ("packaging\version.txt") do set "VERSION=%%V"
)
if "%VERSION%"=="" (
    echo [错误] packaging\version.txt 里没有版本号
    pause
    exit /b 1
)

REM 素材先查齐，缺一件就退出，别打出半成品
for %%F in ("packaging\使用说明.txt" "icon.ico" "LICENSE") do (
    if not exist %%F (
        echo [错误] 缺少打包素材：%%F
        pause
        exit /b 1
    )
)

REM 用系统自带的 bsdtar（Win10 1803+）。Git 自带的 GNU tar 不支持写 zip，
REM 所以这里写死绝对路径，避免被 PATH 里的 GNU tar 顶掉。
set "TAR=%SystemRoot%\System32\tar.exe"
if not exist "%TAR%" (
    echo [错误] 找不到 %TAR%（需要 Windows 10 1803 或更高版本）
    pause
    exit /b 1
)

set "NAME=DesktopClock-v%VERSION%-win64"
set "STAGE=dist\%NAME%"

REM 编译交给 build.bat，传 nopause 免得打包卡在暂停上
call build.bat nopause
if errorlevel 1 (
    echo [错误] 编译失败，打包中止
    pause
    exit /b 1
)

echo [组装] 清理旧目录 %STAGE% ...
if exist "%STAGE%" rmdir /s /q "%STAGE%"
mkdir "%STAGE%" 2>nul
if errorlevel 1 (
    echo [错误] 无法创建 %STAGE%
    pause
    exit /b 1
)

copy /y DesktopClockGo.exe "%STAGE%\" >nul
copy /y "packaging\使用说明.txt" "%STAGE%\" >nul
copy /y icon.ico "%STAGE%\" >nul
copy /y LICENSE "%STAGE%\" >nul

REM config.json 刻意不放进包：里面是本机的坐标/字体/颜色，别人解压会继承
REM 这台机器的设置；程序首次运行会按默认值生成自己那份。
if exist "%STAGE%\config.json" del /q "%STAGE%\config.json"

echo [打包] 压缩 dist\%NAME%.zip ...
pushd "%STAGE%"
if exist "..\%NAME%.zip" del /q "..\%NAME%.zip"
REM -a 按扩展名选格式（.zip）；bsdtar 默认 zip 用 store，必须显式指定 deflate
"%TAR%" -a --options "zip:compression=deflate" -c -f "..\%NAME%.zip" *
if errorlevel 1 (
    popd
    echo [错误] 压缩失败
    pause
    exit /b 1
)
popd

echo.
echo 打包完成：
echo   目录  : %STAGE%
echo   压缩包: dist\%NAME%.zip
for %%F in ("dist\%NAME%.zip") do echo   大小  : %%~zF 字节
echo.
if /i not "%~1"=="nopause" pause
