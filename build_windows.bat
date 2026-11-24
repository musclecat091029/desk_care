@echo off
chcp 65001 >nul
cd /d "%~dp0"
where go >nul 2>&1
if errorlevel 1 (
  echo Go 개발환경이 필요합니다. https://go.dev/dl/
  pause
  exit /b 1
)
set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0
go build -trimpath -ldflags="-s -w -H windowsgui" -o deskcare_window_1.9.exe .
if errorlevel 1 (echo 빌드 실패 & pause & exit /b 1)
where python >nul 2>&1
if errorlevel 1 (
  echo Python을 찾을 수 없어 EXE 아이콘 리소스가 추가되지 않았습니다.
  echo 배포 파일에는 이미 아이콘이 들어 있습니다.
) else (
  python embed_windows_icon.py deskcare_window_1.9.exe
  if errorlevel 1 (echo 아이콘 삽입 실패) else (echo 고양이 아이콘 삽입 완료)
)
echo 빌드 완료: deskcare_window_1.9.exe
pause
