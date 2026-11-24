# DeskCare 1.9 — GitHub 배포 안내

## 2026-11-24 현재 상태

- README, CHANGELOG, 설치·사용·보안 문서: GitHub 게시됨
- Windows EXE: **GitHub Releases 업로드 전**
- 전체 Go 및 UI 소스: **GitHub 업로드 완료 (main 브랜치)**
- 공개 저장소: https://github.com/musclecat091029/desk_care
- 버전: **1.9**

## 실행 파일 배포 절차

1. [새 Release 만들기](https://github.com/musclecat091029/desk_care/releases/new)를 엽니다.
2. **Choose a tag**에서 새로운 태그 `v1.9`를 만듭니다.
3. Release title: `DeskCare 1.9 — Installed App Paths & Smart Cleanup Filters`
4. 다음 변경 사항을 본문에 적습니다.
   - 설치된 앱의 설치 경로 표시 및 폴더 열기
   - 스마트 정리와 전체 파일 필터 연동
   - 오래된 파일 검색을 분석한 전체 사용자 폴더로 확대
   - 기존 자동 보호·휴지통 이동 유지
   - **디지털 서명 없음: SmartScreen 경고 가능**
5. 컴퓨터에 저장한 `deskcare_window_1.9.exe`를 **Attach binaries** 영역에 넣습니다.
6. **Publish release**를 클릭합니다.

## 체크섬

EXE 파일명: `deskcare_window_1.9.exe`

SHA-256: `69de78dbfda2bfc1670bfa9b134f3b90d23f3b0953519846ae7aa3ae9e981bd3`

Windows PowerShell에서 확인:

```powershell
Get-FileHash .\deskcare_window_1.9.exe -Algorithm SHA256
```

해시가 다르면 동일한 배포 파일인지 다시 확인하세요.

## 소스코드 업로드 절차

1. 준비된 `deskcare_window_1.9_source.zip`을 압축 해제합니다.
2. GitHub 저장소의 [Add file → Upload files](https://github.com/musclecat091029/desk_care/upload/main)를 엽니다.
3. 압축을 푼 **Go 소스코드, `ui/`, `docs/` 및 빌드 파일**을 폴더 구조 그대로 등록합니다. 이미 있는 README·CHANGELOG·문서는 새 버전과 내용이 중복되므로 기존 파일을 덮어쓸 경우 비교하세요.
4. EXE 실행 파일은 저장소 루트보다 **Releases 첨부 파일**로 배포하는 편을 권장합니다.

> 주의: 디지털 서명 없는 EXE의 배포는 출처 검증과 Windows 실기기 보안 테스트가 필요합니다. 보안 검증 완료라고 표기하지 마세요.
