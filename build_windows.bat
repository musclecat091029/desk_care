@echo off
setlocal
chcp 65001 >nul
cd /d "%~dp0"

where go >nul 2>&1
if errorlevel 1 (
  echo [ERROR] Go development tools required: https://go.dev/dl/
  exit /b 1
)
where python >nul 2>&1
if errorlevel 1 (
  echo [ERROR] Python is required to embed the Windows app icon during builds.
  exit /b 1
)

set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0
go build -trimpath -ldflags="-s -w -H windowsgui" -o deskcare_window_1.9.exe .
if errorlevel 1 (
  echo [ERROR] Go build failed.
  exit /b 1
)
python scripts\embed_windows_icon.py deskcare_window_1.9.exe
if errorlevel 1 (
  echo [ERROR] Windows icon embedding failed.
  exit /b 1
)
echo [OK] Built deskcare_window_1.9.exe
exit /b 0
