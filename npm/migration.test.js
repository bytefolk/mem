"use strict";

const assert = require("node:assert/strict");
const { createHash } = require("node:crypto");
const { EventEmitter } = require("node:events");
const {
  closeSync, existsSync, fstatSync, linkSync, lstatSync, mkdirSync, mkdtempSync,
  openSync, readFileSync, readdirSync, readlinkSync, rmSync, statSync, symlinkSync,
  writeFileSync,
} = require("node:fs");
const { arch, platform, tmpdir } = require("node:os");
const { dirname, join } = require("node:path");
const { PassThrough } = require("node:stream");
const test = require("node:test");
const { cacheRootFor, install, openResponse } = require("./install");
const { assetFor } = require("./platforms");
const pkg = require("./package.json");
const server = require("./server.json");

test("the npm package and MCP metadata identify the ByteFolk 0.1.2 release", () => {
  assert.equal(pkg.name, "@bytefolk/mem-mcp");
  assert.equal(pkg.version, "0.1.2");
  assert.equal(pkg.mcpName, "io.github.bytefolk/mem-mcp");
  assert.equal(server.mcpName, pkg.mcpName);
  assert.equal(server.version, pkg.version);
  assert.equal(server.name, "mem-mcp");
  assert.equal(server.command, "mem-mcp");
  assert.equal(pkg.repository.url, `git+${server.repo}.git`);
  assert.deepEqual(pkg.bin, { "mem-mcp": "./mem-mcp" });
  assert.equal(pkg.scripts.postinstall, undefined);
  assert.throws(() => assetFor("freebsd", "x64"), /@bytefolk\/mem-mcp/);
});

test("installer HTTPS requests identify the renamed package", async () => {
  const response = await openResponse("https://github.com/bytefolk/mem", 0,
    (_url, options, callback) => {
      assert.equal(options.headers["User-Agent"], "@bytefolk/mem-mcp/0.1.2");
      const request = new EventEmitter();
      queueMicrotask(() => {
        const incoming = new PassThrough();
        incoming.statusCode = 200;
        incoming.headers = {};
        callback(incoming);
        incoming.end("fixture");
      });
      return request;
    });
  for await (const _chunk of response) { /* consume response and close timeout */ }
});

test("default cache roots use ByteFolk on every supported OS", () => {
  for (const [osPlatform, environment, homeDirectory, expected] of [
    ["linux", {}, "/home/example", "/home/example/.cache/bytefolk/mem-mcp"],
    ["linux", { XDG_CACHE_HOME: "/cache" }, "/home/example", "/cache/bytefolk/mem-mcp"],
    ["darwin", {}, "/Users/example", "/Users/example/Library/Caches/bytefolk/mem-mcp"],
    ["win32", {}, "C:\\Users\\example", "C:\\Users\\example\\AppData\\Local\\bytefolk\\mem-mcp"],
    ["win32", { LOCALAPPDATA: "C:\\Cache" }, "C:\\Users\\example", "C:\\Cache\\bytefolk\\mem-mcp"],
  ]) {
    assert.equal(cacheRootFor({ osPlatform, environment, homeDirectory }), expected);
  }
});

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), "mem-mcp-migration-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const base = platform() === "darwin" ? join(root, "Library", "Caches")
    : platform() === "win32" ? join(root, "AppData", "Local") : join(root, ".cache");
  const suffix = join("v0.1.2", `${platform()}-${arch()}`);
  const asset = assetFor(platform(), arch());
  const legacyRoot = join(base, "fullstack-ai-infra", "mem-mcp");
  const legacyDir = join(legacyRoot, suffix);
  const legacy = join(legacyDir, asset);
  const destination = join(base, "bytefolk", "mem-mcp", suffix, asset);
  const bytes = Buffer.from("verified release 0.1.2 fixture");
  const digest = createHash("sha256").update(bytes).digest("hex");
  const requests = [];
  mkdirSync(legacyDir, { recursive: true });
  const options = {
    environment: {}, homeDirectory: root, version: "0.1.2",
    logger: { log() {}, warn() {} },
    downloadText: async (url) => {
      requests.push(url);
      return `${digest}  ${asset}\n`;
    },
    downloadFile: async (url, target) => {
      requests.push(url);
      writeFileSync(target, bytes, { flag: "wx" });
    },
  };
  return { root, asset, legacyRoot, legacyDir, legacy, destination, bytes, requests, options };
}

function snapshotFile(path) {
  const fd = openSync(path, "r");
  try {
    return { info: fstatSync(fd), bytes: readFileSync(fd) };
  } finally {
    closeSync(fd);
  }
}

function snapshotTree(root) {
  const info = lstatSync(root);
  if (info.isSymbolicLink()) return { mode: info.mode, link: readlinkSync(root) };
  if (info.isDirectory()) {
    return { mode: info.mode, entries: Object.fromEntries(
      readdirSync(root).sort().map((name) => [name, snapshotTree(join(root, name))]),
    ) };
  }
  const file = snapshotFile(root);
  return { mode: file.info.mode, bytes: file.bytes.toString("hex") };
}

function directoryAlias(target, link) {
  mkdirSync(dirname(link), { recursive: true });
  symlinkSync(target, link, platform() === "win32" ? "junction" : "dir");
}

for (const level of ["namespace", "root", "version", "platform", "ancestor"]) {
  for (const entry of ["readonly-file", "failed-manifest-file", "failed-manifest-directory"]) {
    test(`legacy alias at ${level} preserves ${entry} before any cache mutation`, async (t) => {
      const f = fixture(t);
      if (entry === "failed-manifest-directory") {
        mkdirSync(f.legacy);
        writeFileSync(join(f.legacy, "user-data"), "must survive");
      } else {
        writeFileSync(f.legacy, f.bytes, { mode: 0o400 });
      }
      const newRoot = dirname(dirname(dirname(f.destination)));
      const hops = { namespace: 3, root: 2, version: 1, platform: 0 };
      if (level === "ancestor") {
        // The writable root's ancestor resolves inside the protected old root;
        // the remaining destination suffix does not exist yet.
        directoryAlias(f.legacyRoot, dirname(newRoot));
      } else {
        let target = f.legacyDir;
        let link = dirname(f.destination);
        for (let i = 0; i < hops[level]; i++) {
          target = dirname(target);
          link = dirname(link);
        }
        directoryAlias(target, link);
      }
      const before = snapshotTree(f.root);
      let requests = 0;
      const downloadText = f.options.downloadText;
      f.options.downloadText = async (...args) => {
        requests++;
        if (entry !== "readonly-file") throw new Error("fixture manifest failure");
        return downloadText(...args);
      };
      const result = await install(f.options).catch((error) => error);
      assert.deepEqual(snapshotTree(f.root), before, "legacy bytes, modes, and all directory entries must survive");
      assert.equal(requests, 0, "reject aliasing before downloading or creating a cache lock");
      assert.ok(result instanceof Error, "aliased caches must fail closed");
      assert.match(result.message, /legacy cache/);
    });
  }
}

for (const override of ["environment", "cacheDir"]) {
  test(`explicit ${override} rejects overlap with the default legacy cache`, async (t) => {
    const f = fixture(t);
    writeFileSync(f.legacy, f.bytes, { mode: 0o400 });
    const alias = join(f.root, "selected-cache");
    directoryAlias(override === "environment" ? f.legacyRoot : f.legacyDir, alias);
    if (override === "environment") f.options.environment.MEM_MCP_CACHE_DIR = alias;
    else f.options.cacheDir = alias;
    const before = snapshotTree(f.root);
    const result = await install(f.options).catch((error) => error);
    assert.deepEqual(snapshotTree(f.root), before);
    assert.equal(f.requests.length, 0);
    assert.ok(result instanceof Error);
    assert.match(result.message, /legacy cache/);
  });
}

test("a legacy version alias into the new tree is rejected before creating the destination", async (t) => {
  const f = fixture(t);
  // Use a second version so no pre-existing fixture directory is removed.
  f.options.version = "0.1.3";
  const newRoot = dirname(dirname(dirname(f.destination)));
  mkdirSync(join(newRoot, "v0.1.3"), { recursive: true });
  directoryAlias(join(newRoot, "v0.1.3"), join(f.legacyRoot, "v0.1.3"));
  const before = snapshotTree(f.root);
  await assert.rejects(install(f.options), /legacy cache/);
  assert.deepEqual(snapshotTree(f.root), before);
  assert.equal(f.requests.length, 0);
});

for (const failure of [false, true]) {
  test(`hardlinked destination preserves legacy readonly mode with manifest failure=${failure}`, async (t) => {
    const f = fixture(t);
    writeFileSync(f.legacy, f.bytes, { mode: 0o400 });
    mkdirSync(dirname(f.destination), { recursive: true });
    linkSync(f.legacy, f.destination);
    const original = statSync(f.legacy);
    assert.equal(statSync(f.destination).ino, original.ino);
    const before = snapshotTree(f.root);
    let requests = 0;
    const downloadText = f.options.downloadText;
    f.options.downloadText = async (...args) => {
      requests++;
      if (failure) throw new Error("fixture manifest failure");
      return downloadText(...args);
    };
    const result = await install(f.options).catch((error) => error);
    assert.deepEqual(snapshotTree(f.root), before);
    assert.equal(statSync(f.legacy).nlink, original.nlink);
    assert.equal(requests, 0);
    assert.ok(result instanceof Error);
    assert.match(result.message, /legacy cache/);
  });
}

test("concurrent migration copies a verified legacy cache without changing its bytes or mode", async (t) => {
  const f = fixture(t);
  writeFileSync(f.legacy, f.bytes, { mode: 0o400 });
  writeFileSync(join(f.legacyDir, "user-data"), "keep me");
  const before = snapshotFile(f.legacy);
  assert.deepEqual(await Promise.all([install(f.options), install(f.options)]),
    [f.destination, f.destination]);
  assert.deepEqual(readFileSync(f.destination), f.bytes);
  const after = snapshotFile(f.legacy);
  assert.deepEqual(after.bytes, f.bytes);
  assert.deepEqual(after.bytes, before.bytes);
  assert.equal(after.info.mode, before.info.mode);
  assert.equal(after.info.mtimeMs, before.info.mtimeMs);
  assert.equal(readFileSync(join(f.legacyDir, "user-data"), "utf8"), "keep me");
  assert.deepEqual(readdirSync(f.legacyDir).sort(), [f.asset, "user-data"].sort());
  assert.deepEqual(f.requests, Array(2).fill(
    "https://github.com/bytefolk/mem/releases/download/v0.1.2/mem-mcp-checksums.txt"));
});

for (const kind of ["corrupt", "directory", "symlink", "old-version", "other-platform"]) {
  test(`migration ignores ${kind} legacy entries and preserves them`,
    { skip: kind === "symlink" && platform() === "win32" }, async (t) => {
      const f = fixture(t);
      let preserved = f.legacy;
      if (kind === "directory") {
        mkdirSync(f.legacy);
        preserved = join(f.legacy, "user-data");
      } else if (kind === "symlink") {
        preserved = join(f.root, "symlink-target");
        symlinkSync(preserved, f.legacy);
      } else if (kind === "old-version" || kind === "other-platform") {
        const directory = join(f.legacyRoot,
          kind === "old-version" ? "v0.1.1" : "v0.1.2",
          kind === "other-platform" ? "unsupported-arch" : `${platform()}-${arch()}`);
        mkdirSync(directory, { recursive: true });
        preserved = join(directory, f.asset);
      }
      const original = kind === "corrupt" ? Buffer.from("untrusted bytes") : f.bytes;
      writeFileSync(preserved, original);
      assert.equal(await install(f.options), f.destination);
      assert.deepEqual(readFileSync(f.destination), f.bytes);
      assert.deepEqual(readFileSync(preserved), original);
      if (kind === "symlink") assert.ok(lstatSync(f.legacy).isSymbolicLink());
      assert.equal(f.requests.length, 2);
      assert.equal(f.requests[1], `https://github.com/bytefolk/mem/releases/download/v0.1.2/${f.asset}`);
    });
}

for (const failure of ["manifest", "download"]) {
  test(`${failure} failure never deletes a legacy entry`, async (t) => {
    const f = fixture(t);
    writeFileSync(f.legacy, "legacy bytes to preserve");
    f.options[failure === "manifest" ? "downloadText" : "downloadFile"] = async () => {
      throw new Error("fixture failure");
    };
    await assert.rejects(install(f.options), /fixture failure/);
    assert.equal(readFileSync(f.legacy, "utf8"), "legacy bytes to preserve");
    assert.equal(existsSync(f.destination), false);
    assert.deepEqual(readdirSync(f.legacyDir), [f.asset]);
  });
}

for (const override of ["environment", "cacheDir"]) {
  test(`explicit ${override} cache selection disables default legacy lookup`, async (t) => {
    const f = fixture(t);
    writeFileSync(f.legacy, f.bytes);
    const custom = join(f.root, "custom");
    if (override === "environment") f.options.environment.MEM_MCP_CACHE_DIR = custom;
    else f.options.cacheDir = custom;
    const result = await install(f.options);
    assert.ok(result.startsWith(custom));
    assert.equal(f.requests.length, 2);
    assert.deepEqual(readFileSync(f.legacy), f.bytes);
    assert.equal(existsSync(f.destination), false);
  });
}
