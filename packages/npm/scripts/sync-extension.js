#!/usr/bin/env node
"use strict";

const fs = require("fs");
const path = require("path");

const packageRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(packageRoot, "..", "..");
const extensionSource = path.join(repoRoot, "assert", "cohort_browser_bridge");
const extensionTarget = path.join(packageRoot, "extension", "cohort_browser_bridge");
const runtimeScriptsSource = path.join(repoRoot, "scripts");
const runtimeScriptsTarget = path.join(packageRoot, "runtime-scripts");

main();

function main() {
  syncBrowserExtension();
  syncRuntimeScripts();
}

function syncBrowserExtension() {
  if (!fs.existsSync(path.join(extensionSource, "manifest.json"))) {
    throw new Error(`browser extension source not found: ${extensionSource}`);
  }
  fs.rmSync(extensionTarget, { recursive: true, force: true });
  copyDir(extensionSource, extensionTarget);
  console.log(`[cohort] synced browser extension to ${extensionTarget}`);
}

function syncRuntimeScripts() {
  fs.rmSync(runtimeScriptsTarget, { recursive: true, force: true });
  fs.mkdirSync(runtimeScriptsTarget, { recursive: true });
  for (const name of ["desktop_darwin.py", "browser_ocr.py"]) {
    const source = path.join(runtimeScriptsSource, name);
    if (!fs.existsSync(source)) {
      throw new Error(`runtime script not found: ${source}`);
    }
    fs.copyFileSync(source, path.join(runtimeScriptsTarget, name));
  }
  console.log(`[cohort] synced runtime scripts to ${runtimeScriptsTarget}`);
}

function copyDir(from, to) {
  fs.mkdirSync(to, { recursive: true });
  for (const entry of fs.readdirSync(from, { withFileTypes: true })) {
    if (entry.name === ".DS_Store") {
      continue;
    }
    const src = path.join(from, entry.name);
    const dst = path.join(to, entry.name);
    if (entry.isDirectory()) {
      copyDir(src, dst);
      continue;
    }
    if (entry.isFile()) {
      fs.copyFileSync(src, dst);
    }
  }
}
