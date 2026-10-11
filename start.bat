@echo off
rem Starts mining with all devices on what pays best. Stop with Ctrl+C.
rem Watch and configure it in the web interface: http://127.0.0.1:4000/
cd /d "%~dp0"

if not exist rbm.exe (
    echo rbm.exe not found. Build it first:  go build -o rbm.exe ./cmd/rbm
    pause
    exit /b 1
)

rbm.exe run
echo.
echo rbm has stopped.
pause
