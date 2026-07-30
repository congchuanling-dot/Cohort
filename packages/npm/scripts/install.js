#!/usr/bin/env node
"use strict";

const crypto = require("crypto");
const fs = require("fs");
const https = require("https");
const path = require("path");

const packageRoot = path.resolve(__dirname, "..");
const vendorDir = path.join(packageRoot, "vendor");
const targetPlatform = process.env.COHORT_NPM_TARGET_PLATFORM || process.platform;
const targetArch = process.env.COHORT_NPM_TARGET_ARCH || process.arch;
const binaryPath = path.join(vendorDir, targetPlatform === "win32" ? "cohort.exe" : "cohort");
const dryRun = process.argv.includes("--dry-run");

const repo = process.env.COHORT_NPM_GITHUB_REPO || "congchuanling-dot/Cohort";
const version = process.env.npm_package_version || readPackageVersion();
const releaseTag = process.env.COHORT_NPM_RELEASE_TAG || `v${version}`;

main().catch((err) => {
  console.error(`[cohort] install failed: ${err.message}`);
  console.error("[cohort] You can still install with:");
  console.error("  curl -fsSL https://raw.githubusercontent.com/congchuanling-dot/Cohort/master/scripts/install.sh | sh -s -- --repo https://github.com/congchuanling-dot/Cohort.git");
  process.exit(1);
});

async function main() {
  if (process.env.COHORT_NPM_SKIP_DOWNLOAD === "1") {
    log("skipping binary download because COHORT_NPM_SKIP_DOWNLOAD=1");
    return;
  }

  const asset = resolveAssetName();
  const binaryURL = releaseURL(asset);
  const checksumURL = releaseURL(`${asset}.sha256`);

  if (dryRun) {
    log(`dry run: would download ${binaryURL}`);
    log(`dry run: would verify ${checksumURL}`);
    log(`dry run: would install to ${binaryPath}`);
    return;
  }

  fs.mkdirSync(vendorDir, { recursive: true });
  const tempPath = path.join(vendorDir, `${asset}.download`);

  log(`downloading ${binaryURL}`);
  const binary = await fetchBuffer(binaryURL);

  log(`verifying ${checksumURL}`);
  const checksumText = (await fetchBuffer(checksumURL)).toString("utf8");
  verifyChecksum(binary, checksumText, asset);

  fs.writeFileSync(tempPath, binary, { mode: 0o755 });
  fs.renameSync(tempPath, binaryPath);
  fs.chmodSync(binaryPath, 0o755);
  log(`installed ${binaryPath}`);
}

function readPackageVersion() {
  const data = JSON.parse(fs.readFileSync(path.join(packageRoot, "package.json"), "utf8"));
  return data.version;
}

function resolveAssetName() {
  if (targetPlatform !== "darwin") {
    throw new Error(`unsupported platform ${targetPlatform}; current release provides macOS binaries only`);
  }

  switch (targetArch) {
    case "arm64":
      return "cohort-darwin-arm64";
    case "x64":
      return "cohort-darwin-amd64";
    default:
      throw new Error(`unsupported architecture ${targetArch}`);
  }
}

function releaseURL(asset) {
  return `https://github.com/${repo}/releases/download/${releaseTag}/${asset}`;
}

function verifyChecksum(binary, checksumText, asset) {
  const expected = checksumText.trim().split(/\s+/)[0];
  if (!/^[a-f0-9]{64}$/i.test(expected)) {
    throw new Error(`invalid checksum file for ${asset}`);
  }

  const actual = crypto.createHash("sha256").update(binary).digest("hex");
  if (actual.toLowerCase() !== expected.toLowerCase()) {
    throw new Error(`checksum mismatch for ${asset}: expected ${expected}, got ${actual}`);
  }
}

function fetchBuffer(url, redirects = 0) {
  return new Promise((resolve, reject) => {
    const req = https.get(url, {
      headers: {
        "User-Agent": `cohort-npm-installer/${version}`
      }
    }, (res) => {
      if ([301, 302, 303, 307, 308].includes(res.statusCode)) {
        res.resume();
        if (redirects >= 5) {
          reject(new Error(`too many redirects while fetching ${url}`));
          return;
        }
        const location = res.headers.location;
        if (!location) {
          reject(new Error(`redirect without location while fetching ${url}`));
          return;
        }
        resolve(fetchBuffer(new URL(location, url).toString(), redirects + 1));
        return;
      }

      if (res.statusCode < 200 || res.statusCode >= 300) {
        const chunks = [];
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("end", () => {
          const body = Buffer.concat(chunks).toString("utf8").slice(0, 500);
          reject(new Error(`HTTP ${res.statusCode} for ${url}: ${body}`));
        });
        return;
      }

      const chunks = [];
      res.on("data", (chunk) => chunks.push(chunk));
      res.on("end", () => resolve(Buffer.concat(chunks)));
    });

    req.on("error", reject);
    req.setTimeout(120000, () => {
      req.destroy(new Error(`timeout while fetching ${url}`));
    });
  });
}

function log(message) {
  console.log(`[cohort] ${message}`);
}
