"use strict";

const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const { join } = require("node:path");
const test = require("node:test");
const { assetFor } = require("./platforms");
const { installerLogger, run } = require("./mem-mcp");

const ASSET = assetFor("linux", "x64");
const BIN_DIR = join("test-root", "bin");
const BIN_PATH = join(BIN_DIR, ASSET);

function captureLogger() {
  const errors = [];
  return {
    errors,
    error(...args) {
      errors.push(args.join(" "));
    },
  };
}

function exitingChild(code, signal = null) {
  const child = new EventEmitter();
  queueMicrotask(() => child.emit("exit", code, signal));
  return child;
}

test("missing binary bootstraps with stderr-only logging before spawn", async () => {
  const logger = captureLogger();
  const calls = [];

  const code = await run({
    osPlatform: "linux",
    osArch: "x64",
    binDir: BIN_DIR,
    args: ["--server", "http://127.0.0.1:8787"],
    env: { MEM_TOKEN: "test-token" },
    logger,
    install: async (options) => {
      calls.push(["install", options.osPlatform, options.osArch, options.binDir]);
      options.logger.log("manifest download");
      options.logger.warn("cache warning");
      return BIN_PATH;
    },
    spawn: (path, args, options) => {
      calls.push(["spawn", path, args, options]);
      return exitingChild(0);
    },
  });

  assert.equal(code, 0);
  assert.deepEqual(calls, [
    ["install", "linux", "x64", BIN_DIR],
    [
      "spawn",
      BIN_PATH,
      ["--server", "http://127.0.0.1:8787"],
      { env: { MEM_TOKEN: "test-token" }, stdio: "inherit" },
    ],
  ]);
  assert.deepEqual(logger.errors, ["manifest download", "cache warning"]);
});

test("present binary is reverified while the installer reuses its cache", async () => {
  let verifications = 0;
  let spawned = null;

  const code = await run({
    osPlatform: "linux",
    osArch: "x64",
    binDir: BIN_DIR,
    install: async (options) => {
      verifications += 1;
      assert.equal(options.binDir, BIN_DIR);
      return BIN_PATH;
    },
    spawn: (path) => {
      spawned = path;
      return exitingChild(0);
    },
  });

  assert.equal(code, 0);
  assert.equal(verifications, 1);
  assert.equal(spawned, BIN_PATH);
});

test("bootstrap failure is actionable and never starts a child", async () => {
  const logger = captureLogger();
  let spawns = 0;

  const code = await run({
    osPlatform: "linux",
    osArch: "x64",
    binDir: BIN_DIR,
    logger,
    install: async () => {
      throw new Error("checksum mismatch");
    },
    spawn: () => {
      spawns += 1;
      return exitingChild(0);
    },
  });

  assert.equal(code, 1);
  assert.equal(spawns, 0);
  assert.match(logger.errors[0], /failed to download and verify/);
  assert.match(logger.errors[0], /checksum mismatch/);
  assert.match(logger.errors[1], /retry the command/);
});

test("arguments, environment, inherited stdio, and child exit code propagate", async () => {
  const args = ["--workspace", "workspace-id", "--help"];
  const env = { MEM_SERVER: "http://127.0.0.1:8787" };
  let invocation = null;

  const code = await run({
    osPlatform: "linux",
    osArch: "x64",
    binDir: BIN_DIR,
    args,
    env,
    install: async () => BIN_PATH,
    spawn: (path, childArgs, options) => {
      invocation = { path, childArgs, options };
      return exitingChild(23);
    },
  });

  assert.equal(code, 23);
  assert.deepEqual(invocation, {
    path: BIN_PATH,
    childArgs: args,
    options: { env, stdio: "inherit" },
  });
});

test("child start errors and signal exits remain nonzero", async () => {
  const logger = captureLogger();
  const failedChild = new EventEmitter();

  const startFailure = run({
    osPlatform: "linux",
    osArch: "x64",
    binDir: BIN_DIR,
    logger,
    install: async () => BIN_PATH,
    spawn: () => {
      queueMicrotask(() => {
        failedChild.emit("error", new Error("EACCES"));
        failedChild.emit("exit", 1, null);
      });
      return failedChild;
    },
  });

  assert.equal(await startFailure, 1);
  assert.equal(logger.errors.length, 1);
  assert.match(logger.errors[0], /EACCES/);

  const signalExit = await run({
    osPlatform: "linux",
    osArch: "x64",
    binDir: BIN_DIR,
    install: async () => BIN_PATH,
    spawn: () => exitingChild(null, "SIGTERM"),
  });
  assert.equal(signalExit, 1);
});

test("unsupported platforms fail before bootstrap or spawn", async () => {
  const logger = captureLogger();
  let sideEffects = 0;

  const code = await run({
    osPlatform: "freebsd",
    osArch: "riscv64",
    logger,
    install: async () => {
      sideEffects += 1;
    },
    spawn: () => {
      sideEffects += 1;
      return exitingChild(0);
    },
  });

  assert.equal(code, 1);
  assert.equal(sideEffects, 0);
  assert.match(logger.errors[0], /Unsupported platform: freebsd-riscv64/);
});

test("installer log and warning channels are mapped only to stderr", () => {
  const calls = [];
  const logger = {
    error(...args) {
      calls.push(["error", ...args]);
    },
    log(...args) {
      calls.push(["stdout", ...args]);
    },
  };
  const mapped = installerLogger(logger);

  mapped.log("download");
  mapped.warn("warning");

  assert.deepEqual(calls, [
    ["error", "download"],
    ["error", "warning"],
  ]);
});
