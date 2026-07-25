# Cohert

도구 호출, 브라우저 자동화, 데스크톱 인식, 긴 컨텍스트, SOP, 검증된 메모리를 지원하는 로컬 우선 명령줄 Agent Runtime 입니다.

**언어:** [简体中文](../../README.md) · [English](README.en.md) · [日本語](README.ja.md) · **한국어** · [Español](README.es.md) · [Français](README.fr.md) · [हिन्दी](README.hi.md)

## Cohert 소개

Cohert 는 Go 로 작성된 로컬 Agent Runtime 입니다. OpenAI-compatible LLM, 통제된 도구 계층, 영속 세션, 브라우저 자동화, macOS Desktop Computer Use, 컨텍스트 압축, SOP 라우팅, 검증된 장기 메모리를 연결합니다.

```text
사용자 의도
  -> Agent Loop
  -> Context Manager
  -> LLM tool calling
  -> 로컬 도구 / 브라우저 / 데스크톱 / Shell
  -> 증거 기록
  -> 세션 기록과 검증된 메모리
```

핵심 원칙은 간단합니다. 모델은 추론하고, 실행은 명시적이고 감사 가능하며 복구 가능하고 증거 기반이어야 합니다.

## 빠른 시작

```bash
git clone <repo-url>
cd Cohort
export DEEPSEEK_API_KEY="sk-xxx"
go run .
```

단일 작업 실행:

```bash
go run . ask "README.md 를 읽고 현재 runtime 기능을 요약해줘"
```

상태 확인:

```bash
go run . config
go run . tools
go run . session list
```

빌드:

```bash
go build -o cohert ./cmd/cohert
./cohert
```

기본 설정은 [`configs/config.yaml`](../../configs/config.yaml)에 있습니다. 자세한 사용법은 [`docs/usage.md`](../usage.md)를 참고하세요.

## 주요 기능

| 영역 | 기능 |
| --- | --- |
| Agent Loop | 스트리밍 대화, 도구 호출, 최대 턴 제어 |
| 로컬 도구 | 파일 읽기/쓰기/patch, shell 실행, 사용자 질문, 구조화 오류 |
| 브라우저 자동화 | Chrome bridge, 페이지 스캔, JS 실행, 요소 snapshot, click/type/key/wait/screenshot/OCR |
| Desktop Computer Use | macOS 권한, 창 목록, PID 활성화, 스크린샷, AX tree, desktop OCR, 통제된 `AXPress`, 제한된 키 입력, 텍스트 초안 |
| 세션 | `history.jsonl`, 메타데이터, 목록, 재개, 로컬 감사 기록 |
| 컨텍스트 관리 | 도구 결과 압축, 안전한 히스토리 트리밍, session memory, full compact |
| SOP Runtime | SOP index, 작업 기반 힌트, working checkpoint |
| Evolution Memory | 증거 기반 entry, project memory, 중복 검사, read-back 확인, audit |

## CLI

```bash
cohert                         # 대화 모드
cohert ask "task"              # 작업 하나 실행 후 종료
cohert tools                   # 도구 목록
cohert config                  # 유효 설정 보기
cohert session list            # 세션 목록
cohert session resume <id>     # 세션 재개
```

대화 모드 명령:

```text
/help
/model
/config
/tools
/session
/session list
/resume <session_id>
/compact
/full-compact
/memory
/exit
```

## 브라우저 자동화

Cohert 는 로컬 Browser Bridge 로 Chrome 을 제어합니다.

```text
ws://127.0.0.1:18777/browser
```

권장 흐름:

```text
open -> wait -> snapshot -> click/type/key -> wait -> verify
```

DOM text 와 `browser_dom_summary` 로 읽을 수 없는 렌더링 텍스트에만 `browser_ocr` 를 사용하세요. OCR bbox 는 `screenshot-local` 이며 시스템 마우스 좌표가 아닙니다.

OCR 선택 의존성:

```bash
python3 -m pip install rapidocr-onnxruntime pillow numpy
```

`browser_not_connected` 가 나오면 `assert/cohert_browser_bridge` 에서 Chrome 확장을 로드하세요.

## Desktop Computer Use

Cohert 는 macOS 범용 데스크톱 인식과 통제된 AX 의미 동작을 제공합니다.

```text
desktop_permissions
  -> desktop_windows
  -> desktop_activate
  -> desktop_ax_snapshot
  -> desktop_screenshot
  -> desktop_ocr
  -> desktop_ax_press
  -> desktop_press_key
  -> desktop_type_text
```

OCR 보다 Accessibility / AX 를 우선합니다. `desktop_ax_press` 는 frontmost PID, 최신 AX node metadata, 실행 전 재검증, 실행 후 AX 검증이 필요합니다. `desktop_press_key` 는 제한된 key allowlist 만 지원합니다. 저위험 navigation key 는 바로 실행할 수 있고, Enter, Cmd+Enter, Delete, Backspace 같은 제출/삭제 키는 확인이 필요합니다. `desktop_type_text` 는 현재 focus 된 editable field 에 초안 텍스트만 입력하며 전송하지 않습니다.

위험 정책:

- R1 복구 가능한 동작은 직접 실행할 수 있습니다.
- R2 외부 부작용은 `ask_user` 의 일회성 confirmation token 이 필요합니다.
- R3 결제, 승인, 권한 부여, 로그인 검증, 삭제 등은 자동 실행을 거부합니다.

좌표 클릭 도구는 아직 없습니다.

macOS helper 의존성:

```bash
python3 -m pip install pyobjc-framework-Quartz pyobjc-framework-Cocoa pyobjc-framework-ApplicationServices
```

Cohert 를 실행하는 Terminal 또는 IDE 에 Accessibility 와 Screen Recording 권한을 부여하세요.

## 메모리와 SOP

장기 메모리는 엄격한 3 단계 파이프라인을 사용합니다.

```text
start_long_term_update
  -> memory_propose_update
  -> memory_apply_update
```

메모리 쓰기는 검증된 도구 증거를 참조해야 하며, 민감 정보와 중복을 거부하고 성공 전 read-back 합니다.

SOP 는 가벼운 운영 제약입니다. Cohert 는 [`sops/index.md`](../../sops/index.md)를 navigation 으로 주입하고, 필요한 SOP 를 읽은 뒤 작업합니다.

## 개발

```bash
go test ./...
go vet ./...
go run . tools
go run . config
```

## 원칙

- 로컬 우선.
- 감사 가능한 도구.
- 불변 히스토리.
- 계층화된 컨텍스트.
- 검증된 메모리.
- 점진적 진화.
