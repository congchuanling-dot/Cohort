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

## Requirements

- Node.js 16 or newer.
- macOS arm64 or x64.
- A GitHub Release asset matching the npm package version, for example npm `0.2.4` downloads release `v0.2.4`.

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

## Fallback Installer

```bash
curl -fsSL https://raw.githubusercontent.com/congchuanling-dot/Cohort/master/scripts/install.sh | sh -s -- --repo https://github.com/congchuanling-dot/Cohort.git
```
