import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

assert.equal(process.platform, "win32", "Run this process regression in the Windows CI job");
const source = readFileSync(new URL("./win-audit-verify.bat", import.meta.url), "utf8");

function invoke(script, status) {
  const directory = mkdtempSync(join(tmpdir(), "mem audit evidence "));
  try {
    writeFileSync(join(directory, "verify.bat"), script);
    // cmd.exe resolves the real .cmd fixture from its working directory.
    // No registry, installed package, or user npm configuration is involved.
    writeFileSync(join(directory, "npm.cmd"),
      `@echo off\r\necho fixture npm audit status: ${status}\r\nexit /b ${status}\r\n`);
    const result = spawnSync(process.env.ComSpec || "cmd.exe", ["/d", "/c", "verify.bat"], {
      cwd: directory,
      encoding: "utf8",
      timeout: 15_000,
      env: { ...process.env, AUDIT_RC: "" },
    });
    assert.ifError(result.error);
    assert.match(result.stdout, new RegExp(`fixture npm audit status: ${status}`));
    return result;
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

for (const status of [0, 7]) {
  test(`prints completed evidence and preserves audit exit ${status}`, () => {
    const result = invoke(source, status);
    assert.equal(result.status, status);
    assert.match(result.stdout, /\[win-audit-verify\] finished at/);
    assert.match(result.stdout, new RegExp(`npm run audit exit code: ${status}`));
  });
}

test("negative control: omitting CALL loses the post-audit evidence", () => {
  const broken = source.replace("call npm run audit", "npm run audit");
  assert.notEqual(broken, source);
  const result = invoke(broken, 7);
  assert.doesNotMatch(result.stdout, /\[win-audit-verify\] finished at/);
});

test("negative control: separate ENDLOCAL loses a nonzero saved exit status", () => {
  const broken = source.replace("endlocal & exit /b %AUDIT_RC%", "endlocal\r\nexit /b %AUDIT_RC%");
  assert.notEqual(broken, source);
  const result = invoke(broken, 7);
  assert.match(result.stdout, /npm run audit exit code: 7/);
  assert.equal(result.status, 0);
});
