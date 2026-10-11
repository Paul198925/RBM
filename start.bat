@echo off
rem Starts mining with all GPUs on what pays best. Stop with Ctrl+C.
cd /d "%~dp0"

if not exist rbm.exe (
    echo rbm.exe not found. Build it first:  go build -o rbm.exe ./cmd/rbm
    pause
    exit /b 1
)
if not exist config.yaml (
    echo config.yaml not found. Copy config.example.yaml and fill in your wallet.
    pause
    exit /b 1
)

rbm.exe run
echo.
echo rbm has stopped.
pause
