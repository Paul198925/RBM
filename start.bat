@echo off
rem Starts mining with all devices on what pays best. Stop with Ctrl+C.
rem Watch and configure it in the web interface: http://127.0.0.1:4000/
cd /d "%~dp0"

rem a newer build waits next to the running program until the next start
if exist rbm.new.exe move /y rbm.new.exe rbm.exe >nul

if not exist rbm.exe call :build
if not exist rbm.exe (
    pause
    exit /b 1
)

rem by its full path: Windows can be set not to look in the current folder
"%~dp0rbm.exe" run
echo.
echo rbm has stopped.
pause
exit /b 0

rem Builds rbm.exe from the source code next to this file, if that and Go
rem are there.
:build
if not exist cmd\rbm\main.go (
    echo rbm.exe not found, and neither is its source code.
    echo Get the program from https://github.com/Paul198925/RBM/releases
    exit /b 1
)
set "GO=go"
where go >nul 2>nul
if errorlevel 1 set "GO=%ProgramFiles%\Go\bin\go.exe"
"%GO%" version >nul 2>nul
if errorlevel 1 (
    echo rbm.exe not found, and Go is not installed to build it.
    echo Install Go from https://go.dev/dl/ or get the program from
    echo https://github.com/Paul198925/RBM/releases
    exit /b 1
)
echo rbm.exe not found. Building it, this takes a minute the first time ...
set CGO_ENABLED=0
"%GO%" build -o rbm.exe ./cmd/rbm
if errorlevel 1 (
    echo The build failed.
    if exist rbm.exe del rbm.exe
    exit /b 1
)
echo Built rbm.exe.
exit /b 0
