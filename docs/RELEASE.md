# 배포 안내

## 현재 상태
- 공개된 GitHub 저장소: `musclecat091029/desk_care`
- README 및 사용설명서 게시
- **Windows 실행 파일 및 전체 소스코드: 아직 이 저장소에 업로드되지 않음**

## 실행 파일을 GitHub Releases에 올리는 방법

1. GitHub 저장소 화면 오른쪽 **Releases** → **Create a new release** 선택
2. 새 태그 `v1.8` 생성, 제목 `DeskCare 1.8 — Security Update`
3. 로컬의 `deskcare_window_1.8.exe` 파일을 **Attach binaries** 영역에 드래그
4. 변경 사항과 '코드 서명되지 않아 SmartScreen 경고가 나타날 수 있음' 문구 추가
5. **Publish release** 클릭

릴리스 파일을 업로드한 후 다운로드 링크가 실제로 열리는지 확인합니다. 테스트를 마치기 전에는 '보안 검증 완료'라고 표기하지 마세요.

소스코드는 zip을 그대로 파일 1개로 올리는 것보다 압축을 풀어 Go 파일 및 `ui/` 폴더를 그대로 커밋하는 편이 GitHub에서 읽기 좋습니다.
