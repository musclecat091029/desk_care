@echo off
chcp 65001 >nul
echo DeskCare를 강제 종료합니다. (파일 작업 중에는 사용하지 마세요.)
taskkill /IM deskcare_window_1.6.exe /F
taskkill /IM deskcar_window_1.5.exe /F
taskkill /IM DeskCare_Windows.exe /F
echo 종료 요청을 마쳤습니다.
pause
