@echo off
REM win-audit-verify.bat — Verify audit-retry.mjs works on real Windows.
REM Must be run from a real Windows terminal (cmd.exe), NOT WSL.
REM Captures: commit, npm_execpath, full audit output, exit code.

setlocal enabledelayedexpansion

echo === win-audit-verify ===
echo.

REM 1. Show which commit is being tested
echo --- git head ---
git rev-parse HEAD
git log -1 --format=^"%%h %%s^"
echo.

REM 2. Show Node version
echo --- node ---
node --version
echo.

REM 3. Prove npm_execpath is set by npm run, and that a direct node.exe
REM    invocation does NOT have it (the failure mode from the review).
echo --- npm_execpath probe via node -e (should be EMPTY) ---
node -e "console.log('npm_execpath=' + (process.env.npm_execpath || '(unset)'))"
echo.

REM 4. Run the actual audit via npm run — this is the path that supplies npm_execpath.
echo --- npm run audit (full output) ---
echo [win-audit-verify] starting npm run audit at %DATE% %TIME%
call npm run audit
set AUDIT_RC=!ERRORLEVEL!
echo [win-audit-verify] finished at %DATE% %TIME%
echo.

REM 5. Report exit code
echo --- result ---
echo [win-audit-verify] npm run audit exit code: !AUDIT_RC!
echo.

endlocal & exit /b %AUDIT_RC%
