# 인수인계 (2026-10-21)

집(macOS)에서 이어서 작업하기 위한 메모입니다. 사용법은 [README.md](README.md)를 보세요.

## 지금 상태

- 레포: https://github.com/jinsyu/server-time-click (공개)
- 최신 릴리스: [v1.0.0](https://github.com/jinsyu/server-time-click/releases/tag/v1.0.0)
  - `servertime-windows.exe`: Windows PC에서 직접 빌드. **실제 클릭까지 검수 완료**
  - `ServerTimeClick-macos.zip`: GitHub Actions 맥에서 빌드. **실제 맥에서는 아직 테스트하지 않음**
- 모든 변경이 커밋·푸시되어 있음 (v1.0.0 = `11ee39a`, 이후 이 문서만 추가)

## 출발점

원래 쓰던 `server_time.py` / `server_time.ps1`(레포에 포함하지 않음)을 Go + Fyne GUI로 옮겼습니다.
서버 시각 계산 방법은 같고, 실제로 성공한 사람이 쓴 설정은 아래와 같습니다.

```
py server_time.py --target 14:00 --click --click-delay 50 --interval 5
```

그래서 GUI의 클릭 지연 기본값은 **50ms**입니다. 0ms로 하면 예매 버튼이 바뀌기 직전에 눌리는 것을 Windows에서 확인했습니다.

## 파일 구조

| 파일 | 내용 |
| --- | --- |
| `main.go` | Fyne GUI: 시계 표시, 시·분·초 목록, 다음 정각/연습 버튼, 예약·취소, 클릭 실행 |
| `internal/clock/clock.go` | 서버 시각 추정 (HTTP `Date` 헤더 이진 탐색), 백그라운드 재보정, KST 시각 계산 |
| `internal/clock/mono_windows.go` | Windows는 `QueryPerformanceCounter`로 시각을 잼 (Go 기본 단조 시계는 1~16ms 단위라 거침) |
| `internal/clock/mono_other.go` | 그 외 OS는 Go 단조 시계 (macOS는 `mach_absolute_time`) |
| `click_windows.go` | `SendInput` 클릭, 앞 창 확인, 알림음, 절전 방지, `timeBeginPeriod(1)` |
| `click_darwin.go` | CoreGraphics 클릭(cgo/Objective-C), 손쉬운 사용 권한 확인, App Nap·절전 방지, `NSBeep` |
| `click_other.go` | 그 외 OS용 빈 구현 (빌드만 되게) |
| `internal/clock/clock_test.go` | 알고리즘·시각 파싱·단조 시계 해상도 테스트, 실제 서버 동기화 테스트(`-short`면 건너뜀) |
| `ui_test.go` | 소프트웨어 렌더링으로 화면 스냅샷(GPU 불필요), 시각 목록·지연 입력 검증 |
| `.github/workflows/build.yml` | Windows 테스트, 맥 universal `.app` 빌드·서명·스냅샷, 태그면 릴리스에 맥 zip 첨부 |

## 동작 원리 요약

1. 예매 사이트에 HEAD 요청 → 응답 `Date` 헤더(초 단위)와 보낸/받은 시각 t0, t1로 `서버−내 시각`이 `[D−t1, D+1−t0]` 안에 있다고 좁힘.
2. 다음 요청을 현재 추정치의 초 경계에 맞춰 보내 범위를 절반씩 줄임. 12번이면 보통 ±20ms 안팎.
3. 15초마다 재보정. 클릭 5초 전 ~ 2초 후에는 요청 안 보냄.
4. 잠자기/시계 보정으로 `벽시계 − 단조 시계`가 0.5초 넘게 변하면 범위를 넓혀 다시 좁힘.
5. 클릭 0.3초 전부터 스레드를 고정하고 계속 시각을 확인하다가 정확한 순간에 한 번 클릭.

## 맥에서 개발 환경

```bash
xcode-select --install
brew install go
git clone https://github.com/jinsyu/server-time-click.git
cd server-time-click
go test ./...
go run .
```

- 첫 빌드는 Fyne(cgo) 때문에 몇 분 걸림.
- `go run .`으로 실행하면 **손쉬운 사용 권한은 터미널 앱**에 줘야 클릭이 들어감. 권한을 바꾼 뒤엔 터미널을 완전히 껐다 켜야 함.
- 릴리스 `.app`은 ad-hoc 서명이라 처음 열 때 `그래도 열기` 필요. 새로 빌드하면 서명이 바뀌어 손쉬운 사용 권한을 다시 켜야 함.

## 맥에서 해야 할 검수 (아직 안 함)

- [ ] 릴리스 `.app` 실행, 한글·레이아웃 정상인지 (CI 스냅샷은 정상이었음)
- [ ] `예약` 누를 때 손쉬운 사용 권한 요청이 뜨는지, 권한 후 재실행하면 통과하는지
- [ ] `연습: 1~2분 뒤`로 실제 클릭이 브라우저 버튼에 들어가는지
- [ ] 브라우저가 앞에 없을 때 첫 클릭이 창 활성화에만 쓰이고 버튼은 안 눌리는지 (그렇다면 README 안내로 충분한지)
- [ ] 기록 창의 `목표보다 +x.xms` 값이 수 ms 이내인지 (App Nap 방지가 효과 있는지)
- [ ] 10·3·2·1초 알림음, "브라우저를 클릭해 앞으로!" 경고가 뜨는지

## 알려진 문제 / 확인 필요

- **Windows 새 빌드 첫 실행이 한 번 저절로 종료됨.** 이후 연속 4번은 정상이었고, 이벤트 로그에도 기록이 없어 원인 미확인. 다시 생기면 `servertime-windows.exe 2> err.txt`처럼 표준 오류를 받아 확인할 것.
- GitHub 맥 러너는 GPU가 없어 창을 띄울 수 없음(`NSGL: Failed to find a suitable pixel format`). 그래서 CI는 실행 대신 소프트웨어 렌더링 스냅샷으로 확인함. **실제 맥에서는 문제 아님.**
- Actions 경고: `actions/checkout@v4`, `actions/setup-go@v5`가 Node 20 기반이라 경고가 뜸. 새 메이저 버전이 나오면 올리기.
- 재보정 주기는 15초 고정. 원래 성공 설정은 5초였음. 필요하면 옵션으로.
- 라이선스 파일 없음. 공개 레포라 필요하면 정할 것.

## 빌드·릴리스 절차

**Windows exe** (Windows PC에서, Go + MinGW-w64 필요)

```bash
go test -short ./...
go build -trimpath -ldflags "-H windowsgui -s -w" -o dist/servertime-windows.exe .
```

- 경로가 길면 Go 도구 실행과 git이 실패함. 짧은 경로에 두거나 `git config core.longpaths true`.

**맥 zip**은 GitHub Actions가 만듦. 새 버전 릴리스:

```bash
gh release create v1.0.1 dist/servertime-windows.exe --target main --title v1.0.1 --notes "..."
```

태그가 생기면 워크플로가 돌아 맥 zip을 같은 릴리스에 붙임(`gh release upload --clobber`). 맥에서만 릴리스할 땐 exe 없이 만들고 Windows exe는 나중에 `gh release upload`로 붙이면 됨.

## 주의

예매 사이트 약관은 보통 자동화 도구를 금지합니다. 클릭 기능은 한 번만 누르고 로그인·결제는 하지 않지만, 계정 제재 위험은 사용자 판단입니다.
