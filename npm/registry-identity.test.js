"use strict";

const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");
const test = require("node:test");
const { REPO } = require("./install");

const serverManifest = JSON.parse(
  readFileSync(join(__dirname, "server.json"), "utf8"),
);
const packageManifest = JSON.parse(
  readFileSync(join(__dirname, "package.json"), "utf8"),
);

// Adapted from bytefolk/mem#162: the registry namespace must follow the
// repository owner used by the installer to resolve Release assets.
test("the registry namespace follows the repository owner", () => {
  const [owner] = REPO.split("/");
  assert.match(REPO, /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\/[a-z0-9._-]+$/i);
  for (const [label, manifest] of [
    ["server.json", serverManifest],
    ["package.json", packageManifest],
  ]) {
    assert.equal(
      manifest.mcpName,
      `io.github.${owner}/${serverManifest.name}`,
      `${label} mcpName must carry the owner of the repository the installer downloads from (${REPO})`,
    );
  }
});

test("both manifests name the same server", () => {
  assert.equal(serverManifest.mcpName, packageManifest.mcpName);
  assert.equal(serverManifest.version, packageManifest.version);
});

test("the registry name is the unscoped package name", () => {
  const [, packageName] = packageManifest.name.split("/");
  assert.equal(
    serverManifest.mcpName.split("/").pop(),
    packageName,
    "the trailing segment of mcpName must be the published package name without its scope",
  );
  assert.equal(
    serverManifest.name,
    packageName,
    "server.json name must match the published package name without its scope",
  );
});
