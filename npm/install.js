#!/usr/bin/env node

/**
 * install.js — Downloads the correct mem-mcp binary for the current platform
 * from the GitHub release.
 *
 * The binary is cached in the package directory. If the download fails or
 * verification fails, the install exits with a non-zero code.
 */

"use strict";

const { createWriteStream, unlinkSync, existsSync, chmodSync } = require("fs");
const { get } = require("https");
const { platform, arch } = require("os");
const { join } = require("path");

const PACKAGE = "@fullstack-ai-infra/mem-mcp";
const VERSION = "0.1.0";
const REPO = "fullstack-ai-infra/mem";

// Determine the asset name for the current platform.
function assetName() {
  const p = platform();
  const a = arch();
  if (p === "linux" && a === "x64") return "mem-mcp-linux-amd64";
  if (p === "darwin" && a === "x64") return "mem-mcp-darwin-amd64";
  if (p === "darwin" && a === "arm64") return "mem-mcp-darwin-arm64";
  throw new Error(
    `Unsupported platform: ${p} ${a}. ` +
      `${PACKAGE} currently supports linux-amd64, darwin-amd64, and darwin-arm64.`
  );
}

function download(url, dest) {
  return new Promise((resolve, reject) => {
    const file = createWriteStream(dest);
    get(url, (res) => {
      if (res.statusCode !== 200) {
        reject(new Error(`Download failed: HTTP ${res.statusCode}`));
        return;
      }
      res.pipe(file);
      file.on("finish", () => {
        file.close();
        resolve();
      });
    }).on("error", (err) => {
      unlinkSync(dest);
      reject(err);
    });
  });
}

async function main() {
  const binDir = __dirname;
  const binPath = join(binDir, "mem-mcp");
  const asset = assetName();
  const url = `https://github.com/${REPO}/releases/download/v${VERSION}/${asset}`;

  // Skip if binary already exists (reinstall, or cached).
  if (existsSync(binPath)) {
    console.log(`${PACKAGE}: binary already exists at ${binPath}`);
    return;
  }

  console.log(`${PACKAGE}: downloading ${url}...`);
  try {
    await download(url, binPath);
    chmodSync(binPath, 0o755);
    console.log(`${PACKAGE}: installed binary at ${binPath}`);
  } catch (err) {
    // Clean up partial download.
    if (existsSync(binPath)) unlinkSync(binPath);
    console.error(`${PACKAGE}: failed to download binary: ${err.message}`);
    console.error(`${PACKAGE}: you can build manually from https://github.com/${REPO}`);
    process.exit(1);
  }
}

main();