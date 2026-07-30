#!/usr/bin/env node
"use strict";

const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");

const packageRoot = path.resolve(__dirname, "..");
const binary = path.join(packageRoot, "vendor", process.platform === "win32" ? "cohort.exe" : "cohort");

if (!fs.existsSync(binary)) {
  console.error("[cohort] binary not found. Reinstall the npm package or run:");
  console.error("  npm rebuild @cohort-ai/cohort");
  process.exit(1);
}

const result = spawnSync(binary, process.argv.slice(2), {
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
