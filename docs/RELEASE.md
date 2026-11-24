# DeskCare 1.9 배포 안내

현재 GitHub에는 **소스코드와 사용설명서**가 공개되어 있습니다. **공식 Release 실행 파일은 아직 게시되지 않았습니다.**

## Windows 빌드 (개발자)

필요 도구: Go 1.23 이상(프로젝트 기준 1.23), Python 3, Windows 64비트 환경.

```bat
build_windows.bat
```

이 스크립트는 `deskcare_window_1.9.exe`를 빌드하고 `scripts/embed_windows_icon.py`를 통해 `assets/windows/deskcare_icon_resource.bin`의 고양이 아이콘을 포함합니다.

GitHub Actions도 소스가 빌드되는지 검사하지만, **Windows 실기기 실행·보안 검증 및 코드 서명은 별도**로 진행해야 합니다.

## 새 GitHub Release 게시 (저장소 소유자)

1. [새 릴리스 생성](https://github.com/musclecat091029/desk_care/releases/new) 페이지에서 **Choose a tag → v1.9**를 생성합니다.
2. 제목은 `DeskCare 1.9`로 지정합니다.
3. `deskcare_window_1.9.exe` 파일을 **Attach binaries** 영역에 추가합니다.
4. 사용자 눈높이로 주요 변경점과 **코드 서명 미적용 / SmartScreen 경고 가능** 사항을 적습니다.
5. 배포 전 바이러스 검사, 최신 Windows 실기기 테스트 및 SHA-256 확인을 완료하고 **Publish release**를 누릅니다.

PowerShell에서 해시 확인:

```powershell
Get-FileHash .\deskcare_window_1.9.exe -Algorithm SHA256
```

과거에 제작된 1.9 실행 파일의 SHA-256: `69de78dbfda2bfc1670bfa9b134f3b90d23f3b0953519846ae7aa3ae9e981bd3`.

**주의:** 소스코드에서 새로 빌드한 EXE의 체크섬은 빌드 도구나 환경에 따라 다를 수 있습니다. 실제 배포 파일의 체크섬을 다시 계산한 후 릴리스 본문에 표기하세요.

현재는 최신 버전 자동 업데이트 알림 기능이 없습니다. 사용자는 Releases 페이지에서 수동으로 새 버전을 확인해야 합니다.
