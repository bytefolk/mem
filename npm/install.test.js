"use strict";

const assert = require("node:assert/strict");
const { createHash } = require("node:crypto");
const {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} = require("node:fs");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const test = require("node:test");
const { assetFor } = require("./platforms");
const { checksumForAsset, install } = require("./install");

const ASSET = assetFor("linux", "x64");
const QUIET_LOGGER = { log() {}, warn() {} };

function digest(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

function manifestFor(bytes, asset = ASSET) {
  return `${digest(bytes)}  ${asset}\n`;
}

function testDirectory(t) {
  const directory = mkdtempSync(join(tmpdir(), "mem-mcp-install-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}

test("checksumForAsset accepts one exact sha256sum row", () => {
  const wanted = Buffer.from("verified binary");
  const other = Buffer.from("other binary");
  const manifest = [
    `${digest(other)}  mem-mcp-darwin-arm64`,
    `${digest(wanted)}  ${ASSET}`,
  ].join("\n") + "\n";

  assert.equal(checksumForAsset(manifest, ASSET), digest(wanted));
});

test("checksumForAsset rejects malformed, missing, and duplicate rows", () => {
  const hash = digest(Buffer.from("binary"));
  const cases = [
    ["missing final newline", `${hash}  ${ASSET}`, /newline-terminated/],
    ["uppercase hex", `${hash.toUpperCase()}  ${ASSET}\n`, /Invalid/],
    ["single separator", `${hash} ${ASSET}\n`, /Invalid/],
    ["binary marker", `${hash} *${ASSET}\n`, /Invalid/],
    ["path instead of basename", `${hash}  .\/${ASSET}\n`, /Invalid/],
    ["missing asset", `${hash}  mem-mcp-darwin-arm64\n`, /no entry/],
    [
      "duplicate asset",
      `${hash}  ${ASSET}\n${hash}  ${ASSET}\n`,
      /Duplicate/,
    ],
  ];

  for (const [name, manifest, expected] of cases) {
    assert.throws(() => checksumForAsset(manifest, ASSET), expected, name);
  }
});

test("install verifies a temporary download before exposing it", async (t) => {
  const root = testDirectory(t);
  const binDir = join(root, "bin");
  const bytes = Buffer.from("trusted mem-mcp binary");
  const requested = [];

  const installed = await install({
    osPlatform: "linux",
    osArch: "x64",
    version: "0.1.1",
    repository: "example/mem",
    binDir,
    logger: QUIET_LOGGER,
    downloadText: async (url) => {
      requested.push(url);
      return manifestFor(bytes);
    },
    downloadFile: async (url, destination) => {
      requested.push(url);
      writeFileSync(destination, bytes, { flag: "wx", mode: 0o600 });
    },
  });

  assert.equal(installed, join(binDir, ASSET));
  assert.deepEqual(readFileSync(installed), bytes);
  assert.notEqual(statSync(installed).mode & 0o111, 0);
  assert.deepEqual(readdirSync(binDir), [ASSET]);
  assert.deepEqual(requested, [
    "https://github.com/example/mem/releases/download/v0.1.1/mem-mcp-checksums.txt",
    `https://github.com/example/mem/releases/download/v0.1.1/${ASSET}`,
  ]);
});

test("install verifies and reuses a cached binary", async (t) => {
  const root = testDirectory(t);
  const binDir = join(root, "bin");
  const binPath = join(binDir, ASSET);
  const bytes = Buffer.from("cached verified binary");
  mkdirSync(binDir, { recursive: true });
  writeFileSync(binPath, bytes, { mode: 0o600 });
  chmodSync(binPath, 0o600);
  let binaryDownloads = 0;

  const installed = await install({
    osPlatform: "linux",
    osArch: "x64",
    binDir,
    logger: QUIET_LOGGER,
    downloadText: async () => manifestFor(bytes),
    downloadFile: async () => {
      binaryDownloads += 1;
      throw new Error("verified cache must not be downloaded again");
    },
  });

  assert.equal(installed, binPath);
  assert.equal(binaryDownloads, 0);
  assert.deepEqual(readFileSync(binPath), bytes);
  assert.notEqual(statSync(binPath).mode & 0o111, 0);
});

test("install replaces a cached binary that fails verification", async (t) => {
  const root = testDirectory(t);
  const binDir = join(root, "bin");
  const binPath = join(binDir, ASSET);
  const expected = Buffer.from("replacement verified binary");
  mkdirSync(binDir, { recursive: true });
  writeFileSync(binPath, "stale unverified binary", { mode: 0o755 });
  let binaryDownloads = 0;

  const installed = await install({
    osPlatform: "linux",
    osArch: "x64",
    binDir,
    logger: QUIET_LOGGER,
    downloadText: async () => manifestFor(expected),
    downloadFile: async (_url, destination) => {
      binaryDownloads += 1;
      writeFileSync(destination, expected, { flag: "wx", mode: 0o600 });
    },
  });

  assert.equal(installed, binPath);
  assert.equal(binaryDownloads, 1);
  assert.deepEqual(readFileSync(binPath), expected);
  assert.deepEqual(readdirSync(binDir), [ASSET]);
});

test("checksum mismatch removes the temporary and final binary", async (t) => {
  const root = testDirectory(t);
  const binDir = join(root, "bin");
  const expected = Buffer.from("expected binary");
  const substituted = Buffer.from("substituted binary");

  await assert.rejects(
    install({
      osPlatform: "linux",
      osArch: "x64",
      binDir,
      logger: QUIET_LOGGER,
      downloadText: async () => manifestFor(expected),
      downloadFile: async (_url, destination) => {
        writeFileSync(destination, substituted, { flag: "wx", mode: 0o600 });
      },
    }),
    /Checksum mismatch/,
  );

  assert.deepEqual(readdirSync(binDir), []);
});

test("partial download failure leaves no executable or temp file", async (t) => {
  const root = testDirectory(t);
  const binDir = join(root, "bin");
  const expected = Buffer.from("expected binary");

  await assert.rejects(
    install({
      osPlatform: "linux",
      osArch: "x64",
      binDir,
      logger: QUIET_LOGGER,
      downloadText: async () => manifestFor(expected),
      downloadFile: async (_url, destination) => {
        writeFileSync(destination, "partial", { flag: "wx", mode: 0o600 });
        throw new Error("simulated HTTP stream reset");
      },
    }),
    /simulated HTTP stream reset/,
  );

  assert.deepEqual(readdirSync(binDir), []);
});

test("manifest HTTP failure removes an unverified cached executable", async (t) => {
  const root = testDirectory(t);
  const binDir = join(root, "bin");
  const binPath = join(binDir, ASSET);
  mkdirSync(binDir, { recursive: true });
  writeFileSync(binPath, "unverified cache", { mode: 0o755 });

  await assert.rejects(
    install({
      osPlatform: "linux",
      osArch: "x64",
      binDir,
      logger: QUIET_LOGGER,
      downloadText: async () => {
        throw new Error("Download failed: HTTP 503");
      },
      downloadFile: async () => {
        throw new Error("binary download must not start");
      },
    }),
    /HTTP 503/,
  );

  assert.deepEqual(readdirSync(binDir), []);
});
