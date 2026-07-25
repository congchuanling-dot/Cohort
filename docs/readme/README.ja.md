# Cohert

ツール呼び出し、ブラウザ自動化、デスクトップ認識、長いコンテキスト、SOP、検証済みメモリを扱う、ローカル優先のコマンドライン Agent Runtime です。

**言語:** [简体中文](../../README.md) · [English](README.en.md) · **日本語** · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [हिन्दी](README.hi.md)

## Cohert とは

Cohert は Go で書かれたローカル Agent Runtime です。OpenAI-compatible LLM、制御されたツール層、永続セッション、ブラウザ自動化、macOS Desktop Computer Use、コンテキスト圧縮、SOP ルーティング、検証済み長期メモリを接続します。

```text
ユーザー意図
  -> Agent Loop
  -> Context Manager
  -> LLM tool calling
  -> ローカルツール / ブラウザ / デスクトップ / Shell
  -> 証拠ログ
  -> セッション履歴と検証済みメモリ
```

基本方針は、モデルが推論し、実行は明示的で監査可能、復旧可能、証拠に基づくものにすることです。

## クイックスタート

```bash
git clone <repo-url>
cd Cohort
export DEEPSEEK_API_KEY="sk-xxx"
go run .
```

1 回だけタスクを実行：

```bash
go run . ask "README.md を読み、現在の Runtime 能力を要約して"
```

状態確認：

```bash
go run . config
go run . tools
go run . session list
```

ビルド：

```bash
go build -o cohert ./cmd/cohert
./cohert
```

設定は [`configs/config.yaml`](../../configs/config.yaml) にあります。詳しい使い方は [`docs/usage.md`](../usage.md) を参照してください。

## 主な機能

| 領域 | 機能 |
| --- | --- |
| Agent Loop | ストリーミング対話、ツール呼び出し、最大ターン制御 |
| ローカルツール | ファイル読書き、patch、shell 実行、ユーザー確認、構造化エラー |
| ブラウザ自動化 | Chrome bridge、ページ読み取り、JS 実行、要素 snapshot、click/type/key/wait/screenshot/OCR |
| Desktop Computer Use | macOS 権限、ウィンドウ列挙、PID activation、スクリーンショット、AX tree、desktop OCR、制御済み `AXPress`、制限付きキー入力 |
| セッション | `history.jsonl`、メタデータ、一覧、再開、ローカル監査 |
| コンテキスト管理 | ツール結果圧縮、安全な履歴裁剪、session memory、full compact |
| SOP Runtime | SOP index、タスク別ヒント、working checkpoint |
| Evolution Memory | 証拠付きメモリ、project memory、重複検査、read-back、audit |

## CLI

```bash
cohert                         # 対話モード
cohert ask "task"              # 1 タスクだけ実行
cohert tools                   # ツール一覧
cohert config                  # 有効な設定
cohert session list            # セッション一覧
cohert session resume <id>     # セッション再開
```

対話モード：

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

## ブラウザ自動化

Cohert はローカル Browser Bridge 経由で Chrome を操作します。

```text
ws://127.0.0.1:18777/browser
```

推奨フロー：

```text
open -> wait -> snapshot -> click/type/key -> wait -> verify
```

DOM text と `browser_dom_summary` で読めない文字だけ `browser_ocr` を使います。OCR の bbox は `screenshot-local` であり、システムのマウス座標ではありません。

OCR 依存：

```bash
python3 -m pip install rapidocr-onnxruntime pillow numpy
```

`browser_not_connected` の場合は `assert/cohert_browser_bridge` から Chrome 拡張を読み込んでください。

## Desktop Computer Use

Cohert は macOS の汎用デスクトップ認識と制御済み AX セマンティック操作を提供します。

```text
desktop_permissions
  -> desktop_windows
  -> desktop_activate
  -> desktop_ax_snapshot
  -> desktop_screenshot
  -> desktop_ocr
  -> desktop_ax_press
  -> desktop_press_key
```

OCR より Accessibility / AX を優先します。`desktop_ax_press` は frontmost PID、最新の AX node metadata、実行前の再検証、実行後の AX 検証が必要です。`desktop_press_key` は制限付きキー allowlist のみ対応し、低リスクのナビゲーションキーは直接実行できます。Enter、Cmd+Enter、Delete、Backspace などの送信/削除系キーは確認が必要です。

リスクポリシー：

- R1 の復旧可能な操作は直接実行できます。
- R2 の外部副作用は `ask_user` の一回限り confirmation token が必要です。
- R3 の支払い、承認、認可、ログイン確認、削除などは自動実行を拒否します。

座標クリックとテキスト入力はまだありません。

macOS helper 依存：

```bash
python3 -m pip install pyobjc-framework-Quartz pyobjc-framework-Cocoa pyobjc-framework-ApplicationServices
```

Cohert を実行する Terminal または IDE に Accessibility と Screen Recording 権限を付与してください。

## メモリと SOP

長期メモリは厳格な 3 段階です。

```text
start_long_term_update
  -> memory_propose_update
  -> memory_apply_update
```

書き込みは検証済みツール証拠を参照し、機密情報と重複を拒否し、成功前に read-back します。

SOP は軽量な運用制約です。Cohert は [`sops/index.md`](../../sops/index.md) をナビゲーションとして注入し、必要な SOP を読んでから実行します。

## 開発

```bash
go test ./...
go vet ./...
go run . tools
go run . config
```

## 原則

- ローカル優先。
- 監査可能なツール。
- 不変の履歴。
- 階層化されたコンテキスト。
- 検証済みメモリ。
- 段階的な進化。
