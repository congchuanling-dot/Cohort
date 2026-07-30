#!/usr/bin/env node
"use strict";

const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");

const packageRoot = path.resolve(__dirname, "..");
const binary = path.join(packageRoot, "vendor", process.platform === "win32" ? "cohort.exe" : "cohort");
const extensionDir = path.join(packageRoot, "extension", "cohort_browser_bridge");
const runtimeScriptsDir = path.join(packageRoot, "runtime-scripts");
const desktopHelper = path.join(runtimeScriptsDir, "desktop_darwin.py");
const browserOCRHelper = path.join(runtimeScriptsDir, "browser_ocr.py");

if (!fs.existsSync(binary)) {
  console.error("[cohort] binary not found. Reinstall the npm package or run:");
  console.error("  npm rebuild @cohort-ai/cohort");
  process.exit(1);
}

const env = { ...process.env };
if (!env.COHORT_BROWSER_EXTENSION_DIR && fs.existsSync(path.join(extensionDir, "manifest.json"))) {
  env.COHORT_BROWSER_EXTENSION_DIR = extensionDir;
}
if (!env.COHORT_RUNTIME_SCRIPTS_DIR && fs.existsSync(runtimeScriptsDir)) {
  env.COHORT_RUNTIME_SCRIPTS_DIR = runtimeScriptsDir;
}
if (!env.COHORT_DESKTOP_DARWIN_HELPER_PATH && fs.existsSync(desktopHelper)) {
  env.COHORT_DESKTOP_DARWIN_HELPER_PATH = desktopHelper;
}
if (!env.COHORT_BROWSER_OCR_HELPER_PATH && fs.existsSync(browserOCRHelper)) {
  env.COHORT_BROWSER_OCR_HELPER_PATH = browserOCRHelper;
}

const result = spawnSync(binary, process.argv.slice(2), {
  env,
  stdio: "inherit"
});

if (result.error) {
  console.error(`[cohort] failed to start binary: ${result.error.message}`);
  process.exit(1);
}

if (typeof result.status === "number") {
  process.exit(result.status);
}

if (result.signal) {
  console.error(`[cohort] terminated by signal ${result.signal}`);
  process.exit(1);
}

process.exit(0);
