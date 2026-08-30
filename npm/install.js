#!/usr/bin/env node

/**
 * install.js — Downloads and verifies the mem-mcp binary for this platform.
 *
 * The checksum manifest and binary must come from the same GitHub Release.
 * A binary is written to its final executable path only after its SHA-256
 * digest matches the manifest entry for the exact platform asset.
 */

"use strict";

const { createHash, randomBytes, timingSafeEqual } = require("crypto");
const {
  chmodSync,
  createReadStream,
  createWriteStream,
  existsSync,
  mkdirSync,
  renameSync,
  unlinkSync,
} = require("fs");
const { get } = require("https");
const { platform, arch } = require("os");
const { join } = require("path");
const { pipeline } = require("stream/promises");
const { TextDecoder } = require("util");
const { assetFor } = require("./platforms");

const PACKAGE = "@fullstack-ai-infra/mem-mcp";
const REPO = "fullstack-ai-infra/mem";
const CHECKSUM_ASSET = "mem-mcp-checksums.txt";
const MAX_CHECKSUM_BYTES = 64 * 1024;
const MAX_REDIRECTS = 5;
const guardedResponses = new WeakSet();

// Version from package.json — single source of truth for both release URLs.
// eslint-disable-next-line import/no-unresolved
const { version: VERSION } = require("./package.json");

function removeIfPresent(path) {
  try {
    unlinkSync(path);
  } catch (err) {
    if (err.code !== "ENOENT") throw err;
  }
}

function checkedHttpsUrl(value, base) {
  const parsed = new URL(value, base);
  if (parsed.protocol !== "https:") {
    throw new Error(`Refusing non-HTTPS download URL: ${parsed.toString()}`);
  }
  return parsed;
}

/**
 * Stops an untrusted response body that the caller will not consume.
 *
 * IncomingMessage may emit an error after the status callback returns (for
 * example, when a redirect body is reset). Keep an error listener attached so
 * an intentionally discarded body cannot escape the install failure path as
 * an uncaught exception.
 */
function guardResponseErrors(response) {
  if (guardedResponses.has(response)) return;
  guardedResponses.add(response);
  // This prevents an error emitted between the HTTPS status callback and the
  // consumer's pipeline/async iterator from becoming an uncaught exception.
  // Stream consumers still receive the same error through their own listener
  // or the Readable's stored errored state and therefore reject normally.
  response.on("error", () => {});
}

function discardResponse(response) {
  guardResponseErrors(response);
  response.destroy();
}

/** Opens an HTTPS response, following at most five redirects. */
function openResponse(url, redirects = 0, requestGet = get) {
  return new Promise((resolve, reject) => {
    let parsed;
    try {
      parsed = checkedHttpsUrl(url);
    } catch (err) {
      reject(err);
      return;
    }

    let request;
    try {
      request = requestGet(
        parsed,
        { headers: { "User-Agent": `${PACKAGE}/${VERSION}` } },
        (response) => {
          guardResponseErrors(response);
          const status = response.statusCode || 0;
          if (status >= 300 && status < 400 && response.headers.location) {
            discardResponse(response);
            if (redirects >= MAX_REDIRECTS) {
              reject(new Error("Too many redirects"));
              return;
            }
            let next;
            try {
              next = checkedHttpsUrl(response.headers.location, parsed);
            } catch (err) {
              reject(err);
              return;
            }
            openResponse(next, redirects + 1, requestGet).then(resolve, reject);
            return;
          }
          if (status !== 200) {
            discardResponse(response);
            reject(new Error(`Download failed: HTTP ${status}`));
            return;
          }
          resolve(response);
        },
      );
    } catch (err) {
      reject(err);
      return;
    }
    request.on("error", reject);
  });
}

async function downloadFile(url, destination) {
  const response = await openResponse(url);
  await pipeline(
    response,
    createWriteStream(destination, { flags: "wx", mode: 0o600 }),
  );
}

async function downloadText(
  url,
  maxBytes = MAX_CHECKSUM_BYTES,
  open = openResponse,
) {
  const response = await open(url);
  const declaredLength = Number(response.headers["content-length"] || 0);
  if (Number.isFinite(declaredLength) && declaredLength > maxBytes) {
    discardResponse(response);
    throw new Error(`Checksum manifest exceeds ${maxBytes} bytes`);
  }

  const chunks = [];
  let size = 0;
  for await (const chunk of response) {
    size += chunk.length;
    if (size > maxBytes) {
      discardResponse(response);
      throw new Error(`Checksum manifest exceeds ${maxBytes} bytes`);
    }
    chunks.push(chunk);
  }
  return new TextDecoder("utf-8", { fatal: true }).decode(Buffer.concat(chunks));
}

/**
 * Returns the unique lowercase SHA-256 hex digest for an exact asset name.
 * Every manifest row must use the output format emitted by GNU sha256sum:
 * 64 lowercase hexadecimal characters, two spaces, then one safe basename.
 */
function checksumForAsset(manifest, asset) {
  if (typeof manifest !== "string" || !manifest.endsWith("\n")) {
    throw new Error("Checksum manifest must be non-empty and newline-terminated");
  }
  const lines = manifest.slice(0, -1).split("\n");
  if (lines.length === 0 || lines.some((line) => line.length === 0)) {
    throw new Error("Checksum manifest contains an empty line");
  }

  let expected = null;
  for (const [index, line] of lines.entries()) {
    const match = /^([0-9a-f]{64})  ([A-Za-z0-9][A-Za-z0-9._-]*)$/.exec(line);
    if (!match) {
      throw new Error(`Invalid checksum manifest line ${index + 1}`);
    }
    if (match[2] === asset) {
      if (expected !== null) {
        throw new Error(`Duplicate checksum entry for ${asset}`);
      }
      expected = match[1];
    }
  }
  if (expected === null) {
    throw new Error(`Checksum manifest has no entry for ${asset}`);
  }
  return expected;
}

async function sha256File(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) {
    hash.update(chunk);
  }
  return hash.digest();
}

async function verifyFile(path, expectedHex) {
  if (!/^[0-9a-f]{64}$/.test(expectedHex)) {
    throw new Error("Expected checksum is not lowercase SHA-256 hex");
  }
  const expected = Buffer.from(expectedHex, "hex");
  const actual = await sha256File(path);
  if (!timingSafeEqual(actual, expected)) {
    throw new Error(
      `Checksum mismatch: expected ${expectedHex}, got ${actual.toString("hex")}`,
    );
  }
}

function temporaryPath(binDir, asset) {
  const nonce = randomBytes(12).toString("hex");
  return join(binDir, `.${asset}.${process.pid}.${nonce}.tmp`);
}

async function install(options = {}) {
  const osPlatform = options.osPlatform || platform();
  const osArch = options.osArch || arch();
  const version = options.version || VERSION;
  const repository = options.repository || REPO;
  const binDir = options.binDir || join(__dirname, "bin");
  const fetchText = options.downloadText || downloadText;
  const fetchFile = options.downloadFile || downloadFile;
  const logger = options.logger || console;

  const asset = assetFor(osPlatform, osArch);
  const binPath = join(binDir, asset);
  const releaseBase = `https://github.com/${repository}/releases/download/v${version}`;
  const checksumUrl = `${releaseBase}/${CHECKSUM_ASSET}`;
  const binaryUrl = `${releaseBase}/${asset}`;
  let tempPath = null;

  mkdirSync(binDir, { recursive: true });

  try {
    logger.log(`${PACKAGE}: downloading ${checksumUrl}...`);
    const manifest = await fetchText(checksumUrl);
    const expected = checksumForAsset(manifest, asset);

    if (existsSync(binPath)) {
      try {
        await verifyFile(binPath, expected);
        chmodSync(binPath, 0o755);
        logger.log(`${PACKAGE}: verified existing binary at ${binPath}`);
        return binPath;
      } catch (err) {
        removeIfPresent(binPath);
        logger.warn(`${PACKAGE}: removed unverified cached binary: ${err.message}`);
      }
    }

    tempPath = temporaryPath(binDir, asset);
    logger.log(`${PACKAGE}: downloading ${binaryUrl}...`);
    await fetchFile(binaryUrl, tempPath);
    await verifyFile(tempPath, expected);
    chmodSync(tempPath, 0o755);
    renameSync(tempPath, binPath);
    tempPath = null;
    logger.log(`${PACKAGE}: installed verified binary at ${binPath}`);
    return binPath;
  } catch (err) {
    const cleanupErrors = [];
    if (tempPath !== null) {
      try {
        removeIfPresent(tempPath);
      } catch (cleanupError) {
        cleanupErrors.push(cleanupError);
      }
    }
    // Fail closed: an install error must not leave an executable that this run
    // could not verify against the release manifest.
    try {
      removeIfPresent(binPath);
    } catch (cleanupError) {
      cleanupErrors.push(cleanupError);
    }
    if (cleanupErrors.length > 0) {
      throw new AggregateError(
        [err, ...cleanupErrors],
        `${err.message}; failed to remove unverified install files`,
      );
    }
    throw err;
  }
}

async function main() {
  try {
    await install();
  } catch (err) {
    console.error(`${PACKAGE}: failed to install verified binary: ${err.message}`);
    console.error(`${PACKAGE}: you can build manually from https://github.com/${REPO}`);
    process.exitCode = 1;
  }
}

if (require.main === module) {
  main();
}

module.exports = {
  checksumForAsset,
  discardResponse,
  downloadFile,
  downloadText,
  install,
  openResponse,
  sha256File,
  verifyFile,
};
