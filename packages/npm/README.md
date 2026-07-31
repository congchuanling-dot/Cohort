# Cohort npm wrapper

This package installs the `cohort` command by downloading the matching macOS binary from GitHub Releases.

```bash
npm install -g @cohort-ai/cohort@latest
export DEEPSEEK_API_KEY="sk-xxx"
cohort --version
cohort
```

## What It Installs

The npm package is a thin wrapper. During `postinstall`, it:

1. Detects `darwin-arm64` or `darwin-x64`.
2. Downloads the matching binary from `https://github.com/congchuanling-dot/Cohort/releases`.
3. Downloads the `.sha256` file.
4. Verifies the checksum.
5. Exposes the `cohort` command.
6. Bundles the Cohort Browser Bridge Chrome extension.
7. Bundles the macOS desktop and OCR helper scripts used by Computer Use tools.

## Requirements

- Node.js 16 or newer.
- macOS arm64 or x64.
- A GitHub Release asset matching the npm package version, for example npm `1.0.0` downloads release `v1.0.0`.

## Chrome Extension

Chrome does not allow npm packages to silently install unpacked extensions. Use:

```bash
cohort extension path
cohort extension open
```

Then enable Developer mode in `chrome://extensions`, click `Load unpacked`, and select the printed extension path.

## Environment Overrides

- `COHORT_NPM_GITHUB_REPO`: override the GitHub repository, default `congchuanling-dot/Cohort`.
- `COHORT_NPM_RELEASE_TAG`: override the release tag, default `v${npm_package_version}`.
- `COHORT_NPM_SKIP_DOWNLOAD=1`: skip binary download, useful for packaging tests.
- `COHORT_BROWSER_EXTENSION_DIR`: override the browser extension directory passed to the `cohort` binary.
- `COHORT_RUNTIME_SCRIPTS_DIR`: override the bundled runtime helper script directory.
- `COHORT_DESKTOP_DARWIN_HELPER_PATH`: override the macOS desktop helper script path.
- `COHORT_BROWSER_OCR_HELPER_PATH`: override the OCR helper script path.

## Fallback Installer

```bash
curl -fsSL https://raw.githubusercontent.com/congchuanling-dot/Cohort/master/scripts/install.sh | sh -s -- --repo https://github.com/congchuanling-dot/Cohort.git
```
