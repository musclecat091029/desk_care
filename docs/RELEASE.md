# DeskCare 1.9 배포 및 체크섬

## Windows 실행 파일 다운로드

- [DeskCare v1.9 릴리스](https://github.com/musclecat091029/desk_care/releases/tag/v1.9)
- [deskcare_window_1.9.exe 직접 다운로드](https://github.com/musclecat091029/desk_care/releases/download/v1.9/deskcare_window_1.9.exe)
- [SHA256SUMS.txt 체크섬 파일](https://github.com/musclecat091029/desk_care/releases/download/v1.9/SHA256SUMS.txt)

GitHub Actions Windows 빌드가 Go 테스트와 실행 파일 빌드를 통과했고, 파일을 v1.9 릴리스에 첨부했습니다.

**주의:** 이 릴리스는 **Pre-release**입니다. 실행 파일은 디지털 서명되지 않았고 Windows 실기기에서의 종합적인 실행·보안 검증이 완료되지 않았습니다. SmartScreen 경고가 나타날 수 있습니다. 보호 기능을 끄지 말고 출처와 체크섬을 확인하세요. 중요한 파일은 백업한 뒤 테스트하세요.

## 체크섬 검증

GitHub 릴리스에서 내려받은 `SHA256SUMS.txt`의 해시와 다음 명령의 결과가 일치하는지 확인합니다.

```powershell
Get-FileHash .\deskcare_window_1.9.exe -Algorithm SHA256
```

소스에서 직접 빌드한 파일은 빌드 환경에 따라 해시가 달라질 수 있습니다. 위 체크섬은 릴리스에 첨부된 EXE 기준입니다.

## 개발자 빌드

도구: Go 1.23, Python 3, Windows 64비트 환경.

```bat
build_windows.bat
```

빌드 스크립트는 `scripts/embed_windows_icon.py`와 `assets/windows/deskcare_icon_resource.bin`을 사용해 Windows 실행 파일에 고양이 아이콘을 적용합니다.

릴리스 빌드 자동화 파일: [publish-v1.9.yml](../.github/workflows/publish-v1.9.yml). 현재 1.9 릴리스가 존재하면 자동으로 덮어쓰지 않습니다.

앱 내부 자동 업데이트 알림 기능은 아직 구현되지 않았습니다. 이후 버전은 Releases 페이지에서 확인할 수 있습니다.
