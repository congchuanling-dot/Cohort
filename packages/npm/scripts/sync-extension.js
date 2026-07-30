#!/usr/bin/env node
"use strict";

const fs = require("fs");
const path = require("path");

const packageRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(packageRoot, "..", "..");
const source = path.join(repoRoot, "assert", "cohort_browser_bridge");
const target = path.join(packageRoot, "extension", "cohort_browser_bridge");

main();

function main() {
  if (!fs.existsSync(path.join(source, "manifest.json"))) {
    throw new Error(`browser extension source not found: ${source}`);
  }
  fs.rmSync(target, { recursive: true, force: true });
  copyDir(source, target);
  console.log(`[cohort] synced browser extension to ${target}`);
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
