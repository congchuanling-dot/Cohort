#!/usr/bin/env sh
set -eu

BIN_NAME="${COHORT_BIN_NAME:-cohort}"
INSTALL_DIR="${COHORT_INSTALL_DIR:-"$HOME/.cohort/bin"}"
CONFIG_DIR="${COHORT_CONFIG_DIR:-"$HOME/.cohort"}"
CONFIG_PATH="${COHORT_CONFIG:-"$CONFIG_DIR/config.yaml"}"
WORKSPACE_DIR="${COHORT_WORKSPACE:-"$CONFIG_DIR/workspace"}"
LOG_DIR="${COHORT_LOG_DIR:-"$CONFIG_DIR/logs/model_responses"}"
SCRIPTS_DIR="${COHORT_SCRIPTS_DIR:-"$CONFIG_DIR/scripts"}"
GO_BIN="${GO_BIN:-go}"
REPO_URL="${COHORT_REPO_URL:-}"
REPO_REF="${COHORT_REPO_REF:-}"
GITHUB_REPO="${COHORT_GITHUB_REPO:-congchuanling-dot/Cohort}"
RELEASE_TAG="${COHORT_VERSION:-latest}"
FROM_SOURCE="${COHORT_INSTALL_FROM_SOURCE:-0}"
UPDATE_SHELL="${COHORT_UPDATE_SHELL:-auto}"
DARWIN_ARCH=""

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
Cohort macOS installer

Usage:
  ./scripts/install.sh [options]
  curl -fsSL <install.sh-url> | sh -s -- --repo <git-url>

Options:
  --repo <git-url>      Clone and install from a git repository when not running inside the repo.
  --ref <git-ref>       Checkout a branch, tag, or commit before building.
  --version <tag>       Download a specific GitHub release tag. Default: latest.
  --github-repo <repo>  GitHub repo for release downloads. Default: congchuanling-dot/Cohort.
  --from-source         Skip release download and build from source.
  --install-dir <dir>   Install binary to this directory. Default: ~/.cohort/bin
  --config <file>       Initialize config at this path. Default: ~/.cohort/config.yaml
  --no-shell            Do not append PATH setup to ~/.zshrc.
  --help                Show this help.

Environment:
  COHORT_REPO_URL       Same as --repo.
  COHORT_REPO_REF       Same as --ref.
  COHORT_VERSION        Same as --version.
  COHORT_GITHUB_REPO    Same as --github-repo.
  COHORT_INSTALL_FROM_SOURCE
                         Set to 1 to skip release download.
  COHORT_INSTALL_DIR    Same as --install-dir.
  COHORT_CONFIG         Same as --config.
  COHORT_UPDATE_SHELL   auto|never. Default: auto.
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
      --version)
        [ "$#" -ge 2 ] || fail "--version requires a release tag"
        RELEASE_TAG="$2"
        shift 2
        ;;
      --version=*)
        RELEASE_TAG="${1#--version=}"
        shift
        ;;
      --github-repo)
        [ "$#" -ge 2 ] || fail "--github-repo requires owner/repo"
        GITHUB_REPO="$2"
        shift 2
        ;;
      --github-repo=*)
        GITHUB_REPO="${1#--github-repo=}"
        shift
        ;;
      --from-source)
        FROM_SOURCE="1"
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
    arm64) DARWIN_ARCH="arm64" ;;
    x86_64) DARWIN_ARCH="amd64" ;;
    *) fail "unsupported macOS architecture: ${arch:-unknown}" ;;
  esac
}

release_url() {
  asset="$1"
  if [ "$RELEASE_TAG" = "latest" ]; then
    printf 'https://github.com/%s/releases/latest/download/%s\n' "$GITHUB_REPO" "$asset"
  else
    printf 'https://github.com/%s/releases/download/%s/%s\n' "$GITHUB_REPO" "$RELEASE_TAG" "$asset"
  fi
}

raw_script_url() {
  script="$1"
  ref="$REPO_REF"
  if [ -z "$ref" ]; then
    if [ "$RELEASE_TAG" = "latest" ]; then
      ref="master"
    else
      ref="$RELEASE_TAG"
    fi
  fi
  printf 'https://raw.githubusercontent.com/%s/%s/scripts/%s\n' "$GITHUB_REPO" "$ref" "$script"
}

download_release_binary() {
  [ "$FROM_SOURCE" != "1" ] || return 1
  command -v curl >/dev/null 2>&1 || return 1

  asset="cohort-darwin-${DARWIN_ARCH}"
  url=$(release_url "$asset")
  info "downloading $BIN_NAME from GitHub release: $url"
  if curl -fsSL "$url" -o "$BUILD_TMP/$asset"; then
    checksum_url=$(release_url "$asset.sha256")
    if command -v shasum >/dev/null 2>&1 && curl -fsSL "$checksum_url" -o "$BUILD_TMP/$asset.sha256"; then
      (cd "$BUILD_TMP" && shasum -a 256 -c "$asset.sha256") || return 1
    fi
    mv "$BUILD_TMP/$asset" "$BUILD_TMP/$BIN_NAME"
    chmod 0755 "$BUILD_TMP/$BIN_NAME"
    return 0
  fi
  info "release binary unavailable; falling back to source build"
  return 1
}

install_runtime_scripts() {
  mkdir -p "$SCRIPTS_DIR"
  for script in desktop_darwin.py browser_ocr.py; do
    if [ -n "${SOURCE_DIR:-}" ] && [ -f "$SOURCE_DIR/scripts/$script" ]; then
      cp "$SOURCE_DIR/scripts/$script" "$SCRIPTS_DIR/$script"
      chmod 0755 "$SCRIPTS_DIR/$script"
      continue
    fi
    command -v curl >/dev/null 2>&1 || fail "curl is required to install runtime helper script: $script"
    url=$(raw_script_url "$script")
    info "downloading helper script: $url"
    curl -fsSL "$url" -o "$SCRIPTS_DIR/$script" || fail "failed to download helper script: $script"
    chmod 0755 "$SCRIPTS_DIR/$script"
  done
}

find_source_dir() {
  if [ -f "go.mod" ] && grep -q '^module cohort$' "go.mod"; then
    pwd
    return 0
  fi

  script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || true)
  if [ -n "$script_dir" ]; then
    candidate=$(CDPATH= cd -- "$script_dir/.." 2>/dev/null && pwd || true)
    if [ -f "$candidate/go.mod" ] && grep -q '^module cohort$' "$candidate/go.mod"; then
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

  fail "run this script from the Cohort repository, or pass --repo <git-url> for one-line remote install"
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
    *) fail "COHORT_UPDATE_SHELL must be auto or never" ;;
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
    printf '\n# Cohort CLI\n'
    printf '%s\n' "$line"
  } >>"$zshrc"
  info "updated PATH in $zshrc"
}

parse_args "$@"
check_macos
BUILD_TMP=$(mktemp -d "${TMPDIR:-/tmp}/cohort-build.XXXXXX")
trap 'rm -rf "$BUILD_TMP"' EXIT INT TERM

if ! download_release_binary; then
  need_cmd "$GO_BIN"
  SOURCE_DIR=$(find_source_dir)
  info "building $BIN_NAME from $SOURCE_DIR"
  BUILD_VERSION="dev"
  BUILD_COMMIT="unknown"
  if [ "$RELEASE_TAG" != "latest" ]; then
    BUILD_VERSION="$RELEASE_TAG"
  elif command -v git >/dev/null 2>&1 && [ -d "$SOURCE_DIR/.git" ]; then
    BUILD_VERSION=$(cd "$SOURCE_DIR" && git describe --tags --dirty --always 2>/dev/null || printf 'dev')
  fi
  if command -v git >/dev/null 2>&1 && [ -d "$SOURCE_DIR/.git" ]; then
    BUILD_COMMIT=$(cd "$SOURCE_DIR" && git rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')
  fi
  BUILD_AT=$(date -u '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date)
  BUILD_LDFLAGS="-s -w -X cohort/internal/version.Version=$BUILD_VERSION -X cohort/internal/version.Commit=$BUILD_COMMIT -X cohort/internal/version.BuiltAt=$BUILD_AT"
  (cd "$SOURCE_DIR" && "$GO_BIN" build -trimpath -ldflags "$BUILD_LDFLAGS" -o "$BUILD_TMP/$BIN_NAME" ./cmd/cohort)
fi

mkdir -p "$INSTALL_DIR"
cp "$BUILD_TMP/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"
chmod 0755 "$INSTALL_DIR/$BIN_NAME"
install_runtime_scripts
write_default_config
update_shell_path

info "installed: $INSTALL_DIR/$BIN_NAME"
info "helpers:   $SCRIPTS_DIR"
info "config:    $CONFIG_PATH"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    info ""
    info "Add Cohort to PATH:"
    info "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

info ""
info "Next:"
info "  export DEEPSEEK_API_KEY=\"sk-...\""
info "  $INSTALL_DIR/$BIN_NAME config"
info "  $INSTALL_DIR/$BIN_NAME"
