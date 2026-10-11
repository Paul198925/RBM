@echo off
rem Starts mining with all devices on what pays best. Stop with Ctrl+C.
rem Watch and configure it in the web interface: http://127.0.0.1:4000/
cd /d "%~dp0"

rem tells hashsteer that it is started again when it ends to finish an update
set HASHSTEER_RESTART=1

:run
rem a newer build waits next to the running program until the next start
if exist hashsteer.new.exe move /y hashsteer.new.exe hashsteer.exe >nul

if not exist hashsteer.exe call :build
if not exist hashsteer.exe (
    pause
    exit /b 1
)

rem by its full path: Windows can be set not to look in the current folder
"%~dp0hashsteer.exe" run
rem 75 is hashsteer asking to be started again, with the update it has fetched
if errorlevel 75 if not errorlevel 76 goto run
echo.
echo hashsteer has stopped.
pause
exit /b 0

rem Builds hashsteer.exe from the source code next to this file, if that is there.
rem Go is needed for it; if it is missing, the user is asked before it is
rem installed.
:build
if not exist cmd\hashsteer\main.go (
    echo hashsteer.exe not found, and neither is its source code.
    echo Get the program from https://github.com/Paul198925/hashsteer/releases
    exit /b 1
)
call :findgo
if not errorlevel 1 goto compile

echo hashsteer.exe not found, and Go is not installed to build it.
where winget >nul 2>nul
if errorlevel 1 goto nogo
choice /c YN /n /m "Install Go now with winget? Windows may ask for administrator rights. [Y/N] "
rem 1 is yes; no, Ctrl+C and a missing keyboard are everything else
if errorlevel 2 goto nogo
if not errorlevel 1 goto nogo
winget install --id GoLang.Go --exact --source winget
if errorlevel 1 (
    echo Go could not be installed.
    goto nogo
)
call :findgo
if not errorlevel 1 goto compile
echo Go is installed, but this window does not see it yet. Start this file again.
exit /b 1

:nogo
echo Install Go from https://go.dev/dl/ or get the program from
echo https://github.com/Paul198925/hashsteer/releases
exit /b 1

:compile
echo hashsteer.exe not found. Building it, this takes a minute the first time ...
set CGO_ENABLED=0
"%GO%" build -o hashsteer.exe ./cmd/hashsteer
if errorlevel 1 (
    echo The build failed.
    if exist hashsteer.exe del hashsteer.exe
    exit /b 1
)
echo Built hashsteer.exe.
exit /b 0

rem Sets GO to the Go program: the one on the path, or the one in the folder
rem its installer uses, which a window opened before the installation does
rem not have on its path. Fails if there is none.
:findgo
set "GO=go"
where go >nul 2>nul
if not errorlevel 1 exit /b 0
set "GO=%ProgramFiles%\Go\bin\go.exe"
if exist "%GO%" exit /b 0
exit /b 1
