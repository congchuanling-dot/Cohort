# Cohort

도구 호출, 브라우저 자동화, 데스크톱 인식, 긴 컨텍스트, SOP, 검증된 메모리를 지원하는 로컬 우선 명령줄 Agent Runtime 입니다.

**언어:** [简体中文](../../README.md) · [English](README.en.md) · [日本語](README.ja.md) · **한국어** · [Español](README.es.md) · [Français](README.fr.md) · [हिन्दी](README.hi.md)

## Cohort 소개

Cohort 는 Go 로 작성된 로컬 Agent Runtime 입니다. OpenAI-compatible 및 Anthropic LLM provider, 통제된 도구 계층, 영속 세션, 브라우저 자동화, macOS Desktop Computer Use, 컨텍스트 압축, SOP 라우팅, 검증된 장기 메모리를 연결합니다.

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
npm install -g @cohort-ai/cohort@latest
export DEEPSEEK_API_KEY="sk-xxx"
cohort --version
cohort
```

단일 작업 실행:

```bash
cohort ask "README.md 를 읽고 현재 runtime 기능을 요약해줘"
```

상태 확인:

```bash
cohort config
cohort doctor
cohort session list
```

개발을 위해 소스에서 실행:

```bash
git clone https://github.com/congchuanling-dot/Cohort.git
cd Cohort
go build -o cohort ./cmd/cohort
./cohort
```

npm package 는 public npm registry 에 게시되어 있으며, 설치 중 GitHub Releases 에서 검증된 macOS binary 를 다운로드합니다. 기본 설정은 [`configs/config.yaml`](../../configs/config.yaml)에 있습니다. 자세한 사용법은 [`docs/usage.md`](../usage.md)를 참고하세요.

## LLM Provider

현재 Cohort 가 네이티브로 지원하는 provider 계열은 두 가지입니다.

- `provider: openai`: DeepSeek, Ollama, LM Studio 같은 `/v1/chat/completions` 호환 OpenAI-compatible endpoint
- `provider: anthropic`: Anthropic Messages API

또한 `llm.profiles` 와 `fallback_profiles` 로 주 체인과 예비 체인을 명시적으로 구성할 수 있습니다.

하지만 이것이 모든 API 타입을 곧바로 지원한다는 뜻은 아닙니다. Gemini 네이티브 API, Bedrock, Vertex, Azure OpenAI 전용 인증/경로 변형은 아직 네이티브 어댑터가 없습니다.

## 주요 기능

| 영역 | 기능 |
| --- | --- |
| Agent Loop | OpenAI-compatible / Anthropic provider 기반 스트리밍 대화, 도구 호출, 최대 턴 제어 |
| 로컬 도구 | 파일 읽기/쓰기/patch, shell 실행, 사용자 질문, 구조화 오류 |
| 브라우저 자동화 | Chrome bridge, 페이지 스캔, JS 실행, 요소 snapshot, click/type/key/wait/screenshot/OCR |
| Desktop Computer Use | macOS 권한, 창 목록, PID 활성화, 스크린샷, AX tree, desktop OCR, 통제된 `AXPress`, 제한된 키 입력, 텍스트 초안 |
| 세션 | `history.jsonl`, 메타데이터, 목록, 재개, 로컬 감사 기록 |
| 컨텍스트 관리 | 도구 결과 압축, 안전한 히스토리 트리밍, session memory, full compact |
| SOP Runtime | SOP index, 작업 기반 힌트, working checkpoint |
| Evolution Memory | 증거 기반 entry, project memory, 중복 검사, read-back 확인, audit |

## CLI

```bash
cohort                         # 대화 모드
cohort ask "task"              # 작업 하나 실행 후 종료
cohort tools                   # 도구 목록
cohort config                  # 유효 설정 보기
cohort session list            # 세션 목록
cohort session resume <id>     # 세션 재개
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

Cohort 는 로컬 Browser Bridge 로 Chrome 을 제어합니다.

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

`browser_not_connected` 가 나오면 `cohort extension path` 로 로컬 확장 디렉터리를 확인하거나 `cohort extension open` 으로 `chrome://extensions` 를 열어 로드하세요.

## Desktop Computer Use

Cohort 는 macOS 범용 데스크톱 인식과 통제된 AX 의미 동작을 제공합니다.

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

Cohort 를 실행하는 Terminal 또는 IDE 에 Accessibility 와 Screen Recording 권한을 부여하세요.

## 메모리와 SOP

장기 메모리는 엄격한 3 단계 파이프라인을 사용합니다.

```text
start_long_term_update
  -> memory_propose_update
  -> memory_apply_update
```

메모리 쓰기는 검증된 도구 증거를 참조해야 하며, 민감 정보와 중복을 거부하고 성공 전 read-back 합니다.

SOP 는 가벼운 운영 제약입니다. Cohort 는 [`sops/index.md`](../../sops/index.md)를 navigation 으로 주입하고, 필요한 SOP 를 읽은 뒤 작업합니다.

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
