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

// The registry derives its namespace from the repository owner, so every rename
// leaves the published identifier pointing at an organization that no longer
// exists, and the submission is rejected with no hint that a stale string in a
// manifest caused it. The repository coordinate is therefore asserted here
// against the identifier the submission carries, rather than repeated in a
// third file.
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

// Both manifests are published and read by different tools, so a disagreement
// between them decides which one the validator saw rather than which one is right.
test("both manifests name the same server", () => {
  assert.equal(serverManifest.mcpName, packageManifest.mcpName);
  assert.equal(serverManifest.version, packageManifest.version);
});

// A registry identifier is a primary key, so the npm scope is allowed to differ
// from it while the package name is not allowed to drift from it: `mcpName`
// ends in the unscoped package name by construction, and moving the package
// without moving the identifier would silently fork the registry record.
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
