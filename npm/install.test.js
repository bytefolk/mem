"use strict";

const assert = require("node:assert/strict");
const { createHash } = require("node:crypto");
const { EventEmitter } = require("node:events");
const {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} = require("node:fs");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { PassThrough } = require("node:stream");
const test = require("node:test");
const { assetFor } = require("./platforms");
const {
  cacheDirectory,
  cacheRootFor,
  checksumForAsset,
  downloadText,
  install,
  openResponse,
} = require("./install");

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

function fakeResponse(statusCode, headers = {}) {
  const response = new PassThrough();
  response.statusCode = statusCode;
  response.headers = headers;
  return response;
}

function fakeGetSequence(responses) {
  const pending = [...responses];
  return (_url, _options, callback) => {
    const request = new EventEmitter();
    const next = pending.shift();
    const response = next.response || next;
    queueMicrotask(() => {
      callback(response);
      if (next.afterCallback) next.afterCallback(response);
    });
    return request;
  };
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

test("redirect body reset cannot escape as an uncaught stream error", async () => {
  const redirect = fakeResponse(302, { location: "https://assets.example/binary" });
  const success = fakeResponse(200);
  const responsePromise = openResponse(
    "https://github.example/release/binary",
    0,
    fakeGetSequence([redirect, success]),
  );

  await new Promise((resolve) => setImmediate(resolve));
  assert.doesNotThrow(() => redirect.emit("error", new Error("redirect reset")));
  assert.equal(await responsePromise, success);
  success.destroy();
});

test("200 response reset before consumer attachment rejects normally", async () => {
  const success = fakeResponse(200);
  const reset = new Error("reset immediately after 200 headers");
  const textPromise = downloadText(
    "https://github.example/release/checksums",
    64,
    () => openResponse(
      "https://github.example/release/checksums",
      0,
      fakeGetSequence([{
        response: success,
        afterCallback: (response) => {
          response.destroy(reset);
          // Model an error event emitted in the status-callback/consumer gap.
          response.emit("error", reset);
        },
      }]),
    ),
  );

  await assert.rejects(textPromise, /reset immediately after 200 headers/);
});

test("HTTP error body reset remains a controlled rejection", async () => {
  const unavailable = fakeResponse(503);
  const responsePromise = openResponse(
    "https://github.example/release/binary",
    0,
    fakeGetSequence([unavailable]),
  );

  await assert.rejects(responsePromise, /Download failed: HTTP 503/);
  assert.doesNotThrow(() => unavailable.emit("error", new Error("error reset")));
});

test("oversized manifest body reset remains a controlled rejection", async () => {
  const oversized = fakeResponse(200, { "content-length": "5" });
  const textPromise = downloadText(
    "https://github.example/release/checksums",
    4,
    async () => oversized,
  );

  await assert.rejects(textPromise, /exceeds 4 bytes/);
  assert.doesNotThrow(() => oversized.emit("error", new Error("oversize reset")));
});

test("a stalled HTTPS request rejects on the bounded download timeout", async () => {
  const request = new EventEmitter();
  request.destroy = (err) => queueMicrotask(() => request.emit("error", err));

  await assert.rejects(
    openResponse(
      "https://github.example/release/checksums",
      0,
      () => request,
      { timeoutMs: 10 },
    ),
    /Download timed out after 10 ms/,
  );
});

test("cache paths are user-scoped, versioned, and require absolute overrides", () => {
  assert.equal(
    cacheRootFor({
      osPlatform: "linux",
      environment: { MEM_MCP_CACHE_DIR: "/var/cache/mem-mcp-test" },
      homeDirectory: "/home/example",
    }),
    "/var/cache/mem-mcp-test",
  );
  assert.equal(
    cacheRootFor({
      osPlatform: "linux",
      environment: { XDG_CACHE_HOME: "/var/cache/example" },
      homeDirectory: "/home/example",
    }),
    "/var/cache/example/fullstack-ai-infra/mem-mcp",
  );
  assert.equal(
    cacheRootFor({
      osPlatform: "darwin",
      environment: {},
      homeDirectory: "/Users/example",
    }),
    "/Users/example/Library/Caches/fullstack-ai-infra/mem-mcp",
  );
  assert.equal(
    cacheRootFor({
      osPlatform: "win32",
      environment: { LOCALAPPDATA: "C:\\Users\\example\\AppData\\Local" },
      homeDirectory: "C:\\Users\\example",
    }),
    "C:\\Users\\example\\AppData\\Local\\fullstack-ai-infra\\mem-mcp",
  );
  assert.equal(
    cacheDirectory({
      osPlatform: "linux",
      osArch: "arm64",
      version: "0.1.1",
      environment: { MEM_MCP_CACHE_DIR: "/var/cache/mem-mcp-test" },
      homeDirectory: "/home/example",
    }),
    "/var/cache/mem-mcp-test/v0.1.1/linux-arm64",
  );
  assert.throws(
    () => cacheRootFor({
      osPlatform: "linux",
      environment: { MEM_MCP_CACHE_DIR: "relative/cache" },
      homeDirectory: "/home/example",
    }),
    /must be an absolute path/,
  );
  assert.throws(
    () => cacheRootFor({
      osPlatform: "linux",
      environment: { XDG_CACHE_HOME: "relative/cache" },
      homeDirectory: "/home/example",
    }),
    /must be an absolute path/,
  );
});

test("install verifies a temporary download before exposing it", async (t) => {
  const root = testDirectory(t);
  const cacheRoot = join(root, "user-cache");
  const cacheDir = join(cacheRoot, "v0.1.1", "linux-x64");
  const bytes = Buffer.from("trusted mem-mcp binary");
  const requested = [];

  const installed = await install({
    osPlatform: "linux",
    osArch: "x64",
    version: "0.1.1",
    repository: "example/mem",
    environment: { MEM_MCP_CACHE_DIR: cacheRoot },
    homeDirectory: join(root, "read-only-package-home-must-not-be-used"),
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

  assert.equal(installed, join(cacheDir, ASSET));
  assert.deepEqual(readFileSync(installed), bytes);
  assert.notEqual(statSync(installed).mode & 0o111, 0);
  assert.deepEqual(readdirSync(cacheDir), [ASSET]);
  assert.deepEqual(requested, [
    "https://github.com/example/mem/releases/download/v0.1.1/mem-mcp-checksums.txt",
    `https://github.com/example/mem/releases/download/v0.1.1/${ASSET}`,
  ]);
});

test("install verifies and reuses a cached binary", async (t) => {
  const root = testDirectory(t);
  const cacheDir = join(root, "cache");
  const binPath = join(cacheDir, ASSET);
  const bytes = Buffer.from("cached verified binary");
  mkdirSync(cacheDir, { recursive: true });
  writeFileSync(binPath, bytes, { mode: 0o600 });
  chmodSync(binPath, 0o600);
  let binaryDownloads = 0;

  const installed = await install({
    osPlatform: "linux",
    osArch: "x64",
    cacheDir,
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

test("concurrent installers serialize and publish one verified binary", async (t) => {
  const root = testDirectory(t);
  const cacheDir = join(root, "cache");
  const binPath = join(cacheDir, ASSET);
  const bytes = Buffer.from("one verified concurrent binary");
  let binaryDownloads = 0;

  const installs = Array.from({ length: 12 }, () => install({
    osPlatform: "linux",
    osArch: "x64",
    cacheDir,
    logger: QUIET_LOGGER,
    lockPollMs: 1,
    downloadText: async () => manifestFor(bytes),
    downloadFile: async (_url, destination) => {
      binaryDownloads += 1;
      await new Promise((resolve) => setTimeout(resolve, 20));
      writeFileSync(destination, bytes, { flag: "wx", mode: 0o600 });
    },
  }));

  const installed = await Promise.all(installs);
  assert.deepEqual(installed, Array.from({ length: 12 }, () => binPath));
  assert.equal(binaryDownloads, 1);
  assert.deepEqual(readFileSync(binPath), bytes);
  assert.deepEqual(readdirSync(cacheDir), [ASSET]);
});

test("a failed concurrent installer removes only its own temporary file", async (t) => {
  const root = testDirectory(t);
  const cacheDir = join(root, "cache");
  const binPath = join(cacheDir, ASSET);
  const bytes = Buffer.from("verified winner after failed installer");
  let announceFailureDownload;
  let releaseFailureDownload;
  const failureDownloadStarted = new Promise((resolve) => {
    announceFailureDownload = resolve;
  });
  const allowFailure = new Promise((resolve) => {
    releaseFailureDownload = resolve;
  });

  const failed = install({
    osPlatform: "linux",
    osArch: "x64",
    cacheDir,
    logger: QUIET_LOGGER,
    lockPollMs: 1,
    downloadText: async () => manifestFor(bytes),
    downloadFile: async (_url, destination) => {
      writeFileSync(destination, "partial", { flag: "wx", mode: 0o600 });
      announceFailureDownload();
      await allowFailure;
      throw new Error("simulated concurrent stream failure");
    },
  });
  await failureDownloadStarted;

  const winner = install({
    osPlatform: "linux",
    osArch: "x64",
    cacheDir,
    logger: QUIET_LOGGER,
    lockPollMs: 1,
    downloadText: async () => manifestFor(bytes),
    downloadFile: async (_url, destination) => {
      writeFileSync(destination, bytes, { flag: "wx", mode: 0o600 });
    },
  });
  releaseFailureDownload();

  await assert.rejects(failed, /simulated concurrent stream failure/);
  assert.equal(await winner, binPath);
  assert.deepEqual(readFileSync(binPath), bytes);
  assert.deepEqual(readdirSync(cacheDir), [ASSET]);
});

test("install replaces a cached binary that fails verification", async (t) => {
  const root = testDirectory(t);
  const cacheDir = join(root, "cache");
  const binPath = join(cacheDir, ASSET);
  const expected = Buffer.from("replacement verified binary");
  mkdirSync(cacheDir, { recursive: true });
  writeFileSync(binPath, "stale unverified binary", { mode: 0o755 });
  let binaryDownloads = 0;

  const installed = await install({
    osPlatform: "linux",
    osArch: "x64",
    cacheDir,
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
  assert.deepEqual(readdirSync(cacheDir), [ASSET]);
});

test("failed replacement cleans only the quarantined invalid cache and own temp", async (t) => {
  const root = testDirectory(t);
  const cacheDir = join(root, "cache");
  const binPath = join(cacheDir, ASSET);
  const expected = Buffer.from("expected replacement binary");
  mkdirSync(cacheDir, { recursive: true });
  writeFileSync(binPath, "invalid cached binary", { mode: 0o755 });

  await assert.rejects(
    install({
      osPlatform: "linux",
      osArch: "x64",
      cacheDir,
      logger: QUIET_LOGGER,
      downloadText: async () => manifestFor(expected),
      downloadFile: async (_url, destination) => {
        writeFileSync(destination, "partial", { flag: "wx", mode: 0o600 });
        throw new Error("replacement download failed");
      },
    }),
    /replacement download failed/,
  );

  assert.deepEqual(readdirSync(cacheDir), []);
});

test(
  "install replaces a broken cached symlink without following or deleting its target",
  { skip: process.platform === "win32" },
  async (t) => {
    const root = testDirectory(t);
    const cacheDir = join(root, "cache");
    const binPath = join(cacheDir, ASSET);
    const missingTarget = join(root, "must-remain-missing");
    const expected = Buffer.from("verified replacement for broken symlink");
    mkdirSync(cacheDir, { recursive: true });
    symlinkSync(missingTarget, binPath);

    assert.equal(await install({
      osPlatform: "linux",
      osArch: "x64",
      cacheDir,
      logger: QUIET_LOGGER,
      downloadText: async () => manifestFor(expected),
      downloadFile: async (_url, destination) => {
        writeFileSync(destination, expected, { flag: "wx", mode: 0o600 });
      },
    }), binPath);

    assert.deepEqual(readFileSync(binPath), expected);
    assert.equal(readdirSync(root).includes("must-remain-missing"), false);
    assert.deepEqual(readdirSync(cacheDir), [ASSET]);
  },
);

test("checksum mismatch removes the temporary and final binary", async (t) => {
  const root = testDirectory(t);
  const cacheDir = join(root, "cache");
  const expected = Buffer.from("expected binary");
  const substituted = Buffer.from("substituted binary");

  await assert.rejects(
    install({
      osPlatform: "linux",
      osArch: "x64",
      cacheDir,
      logger: QUIET_LOGGER,
      downloadText: async () => manifestFor(expected),
      downloadFile: async (_url, destination) => {
        writeFileSync(destination, substituted, { flag: "wx", mode: 0o600 });
      },
    }),
    /Checksum mismatch/,
  );

  assert.deepEqual(readdirSync(cacheDir), []);
});

test("partial download failure leaves no executable or temp file", async (t) => {
  const root = testDirectory(t);
  const cacheDir = join(root, "cache");
  const expected = Buffer.from("expected binary");

  await assert.rejects(
    install({
      osPlatform: "linux",
      osArch: "x64",
      cacheDir,
      logger: QUIET_LOGGER,
      downloadText: async () => manifestFor(expected),
      downloadFile: async (_url, destination) => {
        writeFileSync(destination, "partial", { flag: "wx", mode: 0o600 });
        throw new Error("simulated HTTP stream reset");
      },
    }),
    /simulated HTTP stream reset/,
  );

  assert.deepEqual(readdirSync(cacheDir), []);
});

test("manifest HTTP failure preserves but never executes an existing cache", async (t) => {
  const root = testDirectory(t);
  const cacheDir = join(root, "cache");
  const binPath = join(cacheDir, ASSET);
  mkdirSync(cacheDir, { recursive: true });
  writeFileSync(binPath, "unverified cache", { mode: 0o755 });

  await assert.rejects(
    install({
      osPlatform: "linux",
      osArch: "x64",
      cacheDir,
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

  assert.deepEqual(readFileSync(binPath, "utf8"), "unverified cache");
  assert.deepEqual(readdirSync(cacheDir), [ASSET]);
});
