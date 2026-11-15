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

set "VERSION=1.1.0"
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

REM 压缩用 PowerShell 的 Compress-Archive，原因见下方 [打包] 处的注释
set "PS=%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe"
if not exist "%PS%" (
    echo [错误] 找不到 %PS%
    pause
    exit /b 1
)

set "NAME=DesktopClock-v%VERSION%-win64"
set "STAGE=dist\%NAME%"
set "ZIPFILE=%CD%\dist\%NAME%.zip"

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
if exist "%ZIPFILE%" del /q "%ZIPFILE%"
REM 用 Compress-Archive 而不是系统自带的 tar.exe：后者写 zip 时会把中文文件名
REM 按本地代码页存、且不置 UTF-8 标志位，换到非中文系统解压就是乱码。
REM Compress-Archive 会为中文名写上 UTF-8 标志，跨语言环境都正常。
"%PS%" -NoProfile -NonInteractive -Command "Compress-Archive -Path '%CD%\%STAGE%\*' -DestinationPath '%ZIPFILE%' -CompressionLevel Optimal"
if errorlevel 1 (
    echo [错误] 压缩失败
    pause
    exit /b 1
)

echo.
echo 打包完成：
echo   目录  : %STAGE%
echo   压缩包: dist\%NAME%.zip
for %%F in ("%ZIPFILE%") do echo   大小  : %%~zF 字节
echo.
if /i not "%~1"=="nopause" pause
