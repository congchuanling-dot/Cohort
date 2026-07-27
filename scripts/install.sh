#!/usr/bin/env sh
set -eu

BIN_NAME="${COHERT_BIN_NAME:-cohert}"
INSTALL_DIR="${COHERT_INSTALL_DIR:-"$HOME/.cohert/bin"}"
CONFIG_DIR="${COHERT_CONFIG_DIR:-"$HOME/.cohert"}"
CONFIG_PATH="${COHERT_CONFIG:-"$CONFIG_DIR/config.yaml"}"
WORKSPACE_DIR="${COHERT_WORKSPACE:-"$CONFIG_DIR/workspace"}"
LOG_DIR="${COHERT_LOG_DIR:-"$CONFIG_DIR/logs/model_responses"}"
GO_BIN="${GO_BIN:-go}"
REPO_URL="${COHERT_REPO_URL:-}"
REPO_REF="${COHERT_REPO_REF:-}"
UPDATE_SHELL="${COHERT_UPDATE_SHELL:-auto}"

info() {
  printf '%s\n' "$*"
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required but was not found in PATH"
}

usage() {
  cat <<EOF
Cohert macOS installer

Usage:
  ./scripts/install.sh [options]
  curl -fsSL <install.sh-url> | sh -s -- --repo <git-url>

Options:
  --repo <git-url>      Clone and install from a git repository when not running inside the repo.
  --ref <git-ref>       Checkout a branch, tag, or commit before building.
  --install-dir <dir>   Install binary to this directory. Default: ~/.cohert/bin
  --config <file>       Initialize config at this path. Default: ~/.cohert/config.yaml
  --no-shell            Do not append PATH setup to ~/.zshrc.
  --help                Show this help.

Environment:
  COHERT_REPO_URL       Same as --repo.
  COHERT_REPO_REF       Same as --ref.
  COHERT_INSTALL_DIR    Same as --install-dir.
  COHERT_CONFIG         Same as --config.
  COHERT_UPDATE_SHELL   auto|never. Default: auto.
EOF
}

parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo)
        [ "$#" -ge 2 ] || fail "--repo requires a git URL"
        REPO_URL="$2"
        shift 2
        ;;
      --repo=*)
        REPO_URL="${1#--repo=}"
        shift
        ;;
      --ref)
        [ "$#" -ge 2 ] || fail "--ref requires a git ref"
        REPO_REF="$2"
        shift 2
        ;;
      --ref=*)
        REPO_REF="${1#--ref=}"
        shift
        ;;
      --install-dir)
        [ "$#" -ge 2 ] || fail "--install-dir requires a directory"
        INSTALL_DIR="$2"
        shift 2
        ;;
      --install-dir=*)
        INSTALL_DIR="${1#--install-dir=}"
        shift
        ;;
      --config)
        [ "$#" -ge 2 ] || fail "--config requires a file path"
        CONFIG_PATH="$2"
        shift 2
        ;;
      --config=*)
        CONFIG_PATH="${1#--config=}"
        shift
        ;;
      --no-shell)
        UPDATE_SHELL="never"
        shift
        ;;
      --help|-h)
        usage
        exit 0
        ;;
      *)
        fail "unknown option: $1"
        ;;
    esac
  done
}

check_macos() {
  os=$(uname -s 2>/dev/null || true)
  arch=$(uname -m 2>/dev/null || true)
  [ "$os" = "Darwin" ] || fail "this installer currently supports macOS only; detected: ${os:-unknown}"
  case "$arch" in
    arm64|x86_64) ;;
    *) fail "unsupported macOS architecture: ${arch:-unknown}" ;;
  esac
}

find_source_dir() {
  if [ -f "go.mod" ] && grep -q '^module cohert$' "go.mod"; then
    pwd
    return 0
  fi

  script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || true)
  if [ -n "$script_dir" ]; then
    candidate=$(CDPATH= cd -- "$script_dir/.." 2>/dev/null && pwd || true)
    if [ -f "$candidate/go.mod" ] && grep -q '^module cohert$' "$candidate/go.mod"; then
      printf '%s\n' "$candidate"
      return 0
    fi
  fi

  if [ -n "$REPO_URL" ]; then
    need_cmd git
    git clone --depth 1 "$REPO_URL" "$BUILD_TMP/repo" >/dev/null 2>&1 || fail "failed to clone repo: $REPO_URL"
    if [ -n "$REPO_REF" ]; then
      (cd "$BUILD_TMP/repo" && git fetch --depth 1 origin "$REPO_REF" >/dev/null 2>&1 && git checkout FETCH_HEAD >/dev/null 2>&1) || fail "failed to checkout ref: $REPO_REF"
    fi
    printf '%s\n' "$BUILD_TMP/repo"
    return 0
  fi

  fail "run this script from the Cohert repository, or pass --repo <git-url> for one-line remote install"
}

write_default_config() {
  mkdir -p "$(dirname "$CONFIG_PATH")" "$WORKSPACE_DIR" "$LOG_DIR"
  if [ -f "$CONFIG_PATH" ]; then
    info "config exists: $CONFIG_PATH"
    return 0
  fi

  cat >"$CONFIG_PATH" <<EOF
language: zh
workspace: "$WORKSPACE_DIR"
log_dir: "$LOG_DIR"
max_turns: 100

llm:
  active_profile: deepseek
  # fallback_profiles: [local]
  profiles:
    deepseek:
      provider: openai
      name: deepseek
      api_key: \${DEEPSEEK_API_KEY}
      api_base: https://api.deepseek.com
      model: deepseek-v4-pro
      stream: true
      connect_timeout_seconds: 10
      read_timeout_seconds: 120
      max_retries: 2

    local:
      provider: openai
      name: local
      api_key: \${LOCAL_OPENAI_API_KEY}
      api_base: http://127.0.0.1:11434/v1
      model: qwen3-coder
      stream: true
      connect_timeout_seconds: 10
      read_timeout_seconds: 120
      max_retries: 1

    claude:
      provider: anthropic
      name: claude
      api_key: \${ANTHROPIC_API_KEY}
      api_base: https://api.anthropic.com
      model: claude-3-5-sonnet-latest
      stream: true
      connect_timeout_seconds: 10
      read_timeout_seconds: 120
      max_retries: 2

context:
  max_history_messages: 40
  keep_recent_tool_results: 2
  max_tool_result_chars: 12000
  compacted_tool_head_chars: 4000
  compacted_tool_tail_chars: 4000
  max_request_chars: 100000
  max_session_memory_chars: 20000
  max_compact_summary_chars: 60000
  enable_micro_compact: true
EOF
  info "created config: $CONFIG_PATH"
}

update_shell_path() {
  case "$UPDATE_SHELL" in
    never) return 0 ;;
    auto|"") ;;
    *) fail "COHERT_UPDATE_SHELL must be auto or never" ;;
  esac

  case ":$PATH:" in
    *":$INSTALL_DIR:"*) return 0 ;;
  esac

  shell_name=$(basename "${SHELL:-}")
  if [ "$shell_name" != "zsh" ]; then
    info "PATH not updated automatically for shell: ${shell_name:-unknown}"
    return 0
  fi

  zshrc="$HOME/.zshrc"
  line="export PATH=\"$INSTALL_DIR:\$PATH\""
  mkdir -p "$(dirname "$zshrc")"
  if [ -f "$zshrc" ] && grep -F "$INSTALL_DIR" "$zshrc" >/dev/null 2>&1; then
    info "PATH already configured in $zshrc"
    return 0
  fi
  {
    printf '\n# Cohert CLI\n'
    printf '%s\n' "$line"
  } >>"$zshrc"
  info "updated PATH in $zshrc"
}

parse_args "$@"
check_macos
BUILD_TMP=$(mktemp -d "${TMPDIR:-/tmp}/cohert-build.XXXXXX")
trap 'rm -rf "$BUILD_TMP"' EXIT INT TERM
need_cmd "$GO_BIN"
SOURCE_DIR=$(find_source_dir)

info "building $BIN_NAME from $SOURCE_DIR"
(cd "$SOURCE_DIR" && "$GO_BIN" build -o "$BUILD_TMP/$BIN_NAME" ./cmd/cohert)

mkdir -p "$INSTALL_DIR"
cp "$BUILD_TMP/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"
chmod 0755 "$INSTALL_DIR/$BIN_NAME"
write_default_config
update_shell_path

info "installed: $INSTALL_DIR/$BIN_NAME"
info "config:    $CONFIG_PATH"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    info ""
    info "Add Cohert to PATH:"
    info "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

info ""
info "Next:"
info "  export DEEPSEEK_API_KEY=\"sk-...\""
info "  $INSTALL_DIR/$BIN_NAME config"
info "  $INSTALL_DIR/$BIN_NAME"
