// Repository-owned stable npm release gate. Importing this module has no effects.
// Test adapters never invoke a real publisher; the CLI requires hosted OIDC and
// a fresh, exact-release human attestation from the protected npm-release environment.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const PACKAGE = '@bytefolk/mem-mcp';
export const REGISTRY = 'https://registry.npmjs.org';
export const ASSETS = [
  'mem-mcp-darwin-amd64', 'mem-mcp-darwin-arm64',
  'mem-mcp-linux-amd64', 'mem-mcp-linux-arm64',
  'mem-mcp-windows-amd64.exe', 'mem-mcp-windows-arm64.exe',
];
const FILES = ['LICENSE', 'README.md', 'install.js', 'mem-mcp', 'package.json', 'platforms.js'];
const MANIFEST = 'mem-mcp-checksums.txt';
const stable = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
const canonicalRepository = 'git+https://github.com/bytefolk/mem.git';
const readJSON = path => JSON.parse(readFileSync(path, 'utf8'));
const sha = (algorithm, value) => createHash(algorithm).update(value).digest(algorithm === 'sha512' ? 'base64' : 'hex');
const requireValue = (condition, message) => assert.ok(condition, message);
// Downloads change counters; compare the publication identity and asset bytes,
// not incidental API statistics, when checking for a race before publishing.
const releaseIdentity = release => ({
  id: release.id, tag: release.tag_name, draft: release.draft,
  prerelease: release.prerelease, publishedAt: release.published_at,
  assets: release.assets.map(({ id, name, size, state, digest, updated_at }) =>
    ({ id, name, size, state, digest, updated_at })).sort((a, b) => a.name.localeCompare(b.name)),
});

export function checkContext(tag, env, nodeVersion, npmVersion) {
  requireValue(typeof tag === 'string' && stable.test(tag) && !tag.includes('\n'), 'exact stable vX.Y.Z tag required');
  requireValue(env.GITHUB_ACTIONS === 'true' && env.GITHUB_REPOSITORY === 'bytefolk/mem', 'canonical GitHub Actions repository required');
  requireValue(['release', 'workflow_dispatch'].includes(env.GITHUB_EVENT_NAME), 'unsupported release event');
  requireValue(env.GITHUB_REF === `refs/tags/${tag}`, 'dispatch from the exact tag, so provenance identifies the packaged source');
  requireValue(env.GITHUB_WORKFLOW_REF === `bytefolk/mem/.github/workflows/npm-publish.yml@refs/tags/${tag}`, 'unexpected workflow identity');
  requireValue(/^[a-f0-9]{40}$/.test(env.GITHUB_SHA || ''), 'exact GitHub event commit required');
  requireValue(env.RUNNER_ENVIRONMENT === 'github-hosted' && env.RUNNER_OS === 'Linux', 'GitHub-hosted Linux runner required');
  requireValue(env.ACTIONS_ID_TOKEN_REQUEST_URL && env.ACTIONS_ID_TOKEN_REQUEST_TOKEN, 'OIDC id-token permission unavailable');
  requireValue(/^24\.[0-9]+\.[0-9]+$/.test(nodeVersion), 'Node 24 required');
  const npm = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.exec(npmVersion);
  requireValue(npm && (+npm[1] > 11 || (+npm[1] === 11 && +npm[2] >= 15)), 'stable npm >=11.15.0 required');
  for (const [key, value] of Object.entries(env)) {
    if (!value) continue;
    requireValue(!/^(NPM_TOKEN|NODE_AUTH_TOKEN|NPM_AUTH_TOKEN|NPM_ID_TOKEN)$/i.test(key), 'npm token fallback is forbidden');
    requireValue(!/^npm_config_/i.test(key), 'ambient npm configuration is forbidden; the release runner isolates it');
  }
}

export function checkProof(proof, tag, commit, now = Date.now()) {
  requireValue(proof && typeof proof === 'object', 'HOLD: owner/org/Trusted Publisher proof unavailable');
  const exact = { schema: 1, package: PACKAGE, tag, commit, channel: 'next', organization: 'bytefolk',
    repository: 'bytefolk/mem', workflow: 'npm-publish.yml', environment: 'npm-release',
    organizationControlVerified: true, packageAccessVerified: true, twoFactorVerified: true,
    publisherVerified: true, allowPublish: true };
  for (const [key, value] of Object.entries(exact)) assert.equal(proof[key], value, `HOLD: owner proof ${key} mismatch`);
  requireValue(/^oidc:[A-Za-z0-9-]+$/.test(proof.publisherId || ''), 'HOLD: exact npm publisher configuration id required');
  requireValue(/^[A-Za-z0-9][A-Za-z0-9-]{0,38}$/.test(proof.approvedBy || ''), 'HOLD: responsible human approver required');
  requireValue(/^https:\/\/github\.com\/bytefolk\/(mem|\.github)\/issues\/(153|22)#issuecomment-[0-9]+$/.test(proof.evidence || ''), 'HOLD: sanitized owner evidence comment required');
  const verified = Date.parse(proof.verifiedAt);
  const expires = Date.parse(proof.expiresAt);
  requireValue(Number.isFinite(verified) && Number.isFinite(expires) && verified <= now && now < expires &&
    expires - verified <= 24 * 60 * 60 * 1000, 'HOLD: proof must be current and valid for at most 24 hours');
}

export function checkPackage(pkg, server, tag) {
  assert.equal(pkg.name, PACKAGE, 'wrong npm scope/package');
  assert.equal(pkg.version, tag.slice(1), 'npm/tag version mismatch');
  assert.equal(pkg.mcpName, 'io.github.bytefolk/mem-mcp', 'wrong MCP identity');
  assert.equal(pkg.repository?.url, canonicalRepository, 'wrong provenance repository');
  assert.equal(pkg.repository?.type, 'git', 'git repository metadata required');
  requireValue(pkg.private !== true, 'private package must never publish');
  assert.deepEqual(Object.keys(pkg.bin || {}), ['mem-mcp'], 'unexpected CLI mapping');
  // npm normalizes the equivalent ./mem-mcp path to mem-mcp in the registry.
  requireValue(['./mem-mcp', 'mem-mcp'].includes(pkg.bin['mem-mcp']), 'unexpected CLI path');
  assert.equal(server.version, pkg.version, 'MCP metadata version mismatch');
  assert.equal(server.mcpName, pkg.mcpName, 'MCP metadata identity mismatch');
  for (const key of ['dependencies', 'optionalDependencies', 'peerDependencies', 'bundledDependencies', 'bundleDependencies']) {
    requireValue(!pkg[key] || Object.keys(pkg[key]).length === 0, 'wrapper dependency changes require release guard review');
  }
  for (const key of Object.keys(pkg.scripts || {})) {
    requireValue(['test', 'test:tarball'].includes(key), 'unexpected lifecycle script in release package');
  }
  const allowed = { access: 'public', tag: 'next', registry: REGISTRY, provenance: true };
  for (const [key, value] of Object.entries(pkg.publishConfig || {})) {
    requireValue(Object.hasOwn(allowed, key) && allowed[key] === value, 'unsafe publishConfig override');
  }
}

export function checkRelease(release, tag) {
  requireValue(release && release.tag_name === tag && release.draft === false && release.prerelease === false &&
    Number.isSafeInteger(release.id) && release.id > 0 && Number.isFinite(Date.parse(release.published_at)), 'published stable GitHub Release required');
  assert.equal(release.html_url, `https://github.com/bytefolk/mem/releases/tag/${tag}`, 'wrong release repository');
  assert.deepEqual(release.assets?.map(a => a.name).sort(), [...ASSETS, MANIFEST].sort(), 'exact seven release assets required');
  requireValue(release.assets.every(a => Number.isSafeInteger(a.size) && a.size > 0 && a.state === 'uploaded'), 'all release assets must be uploaded and nonempty');
}

export function checkAssets(directory, release, commit, run) {
  assert.deepEqual(readdirSync(directory).sort(), [...ASSETS, MANIFEST].sort(), 'downloaded asset set mismatch');
  for (const asset of release.assets) {
    const file = join(directory, asset.name);
    requireValue(lstatSync(file).isFile() && !lstatSync(file).isSymbolicLink(), 'asset must be a regular file');
    const bytes = readFileSync(file);
    assert.equal(bytes.length, asset.size, 'downloaded asset size mismatch');
    if (asset.digest != null) assert.equal(asset.digest, `sha256:${sha('sha256', bytes)}`, 'GitHub asset digest mismatch');
  }
  const manifest = readFileSync(join(directory, MANIFEST), 'utf8');
  requireValue(manifest.endsWith('\n'), 'checksum manifest must end in newline');
  const rows = manifest.slice(0, -1).split('\n').map(line => {
    const row = /^([a-f0-9]{64})  (mem-mcp-[a-z0-9.-]+)$/.exec(line);
    requireValue(row, 'malformed checksum row');
    return { digest: row[1], name: row[2] };
  });
  assert.deepEqual(rows.map(row => row.name).sort(), [...ASSETS].sort(), 'exactly one checksum per expected binary required');
  for (const { name, digest } of rows) {
    const file = join(directory, name);
    assert.equal(sha('sha256', readFileSync(file)), digest, 'binary checksum mismatch');
    // go version -m reads metadata; it never executes the downloaded binary.
    const metadata = run('go', ['version', '-m', file]);
    const [, , os, arch] = name.replace('.exe', '').split('-');
    for (const field of [`GOOS=${os}`, `GOARCH=${arch}`, `vcs.revision=${commit}`, 'vcs.modified=false']) {
      requireValue(metadata.split('\n').some(line => line.trim() === `build\t${field}`), `binary build metadata mismatch: ${field}`);
    }
    requireValue(/(?:^|\n)\s*path\s+[^\s]+\/server\/cmd\/mem-mcp\s*\n/.test(metadata), 'wrong binary command');
  }
}

export function checkRegistryBefore(data, tag) {
  requireValue(data && data.name === PACKAGE && data.versions && data['dist-tags'], 'HOLD: public package/bootstrap unavailable; 404 is not org proof');
  const bootstrap = data.versions['0.1.2-rc.0'];
  requireValue(bootstrap?.name === PACKAGE && bootstrap.version === '0.1.2-rc.0', 'HOLD: reviewed 0.1.2-rc.0 bootstrap required');
  requireValue(!Object.hasOwn(data.versions, tag.slice(1)), 'npm version already exists; never republish, including after partial failure');
  requireValue(!Object.values(data['dist-tags']).includes(tag.slice(1)), 'registry dist-tag references candidate before publish');
}

export function checkRegistryAfter(data, before, tag, integrity, proof) {
  assert.equal(data?.name, PACKAGE, 'registry package mismatch');
  const version = tag.slice(1);
  const published = data.versions?.[version];
  requireValue(published, 'published version missing from registry');
  checkPackage(published, { version, mcpName: 'io.github.bytefolk/mem-mcp' }, tag);
  assert.equal(data['dist-tags']?.next, version, 'next readback mismatch');
  const tagsWithoutNext = tags => Object.fromEntries(Object.entries(tags).filter(([name]) => name !== 'next'));
  assert.deepEqual(tagsWithoutNext(data['dist-tags']), tagsWithoutNext(before['dist-tags']), 'non-next dist-tags changed; owner investigation required');
  assert.equal(published.dist?.integrity, integrity, 'registry/tarball integrity mismatch');
  assert.equal(published._npmUser?.trustedPublisher?.id, 'github', 'publication was not GitHub OIDC');
  assert.equal(published._npmUser?.trustedPublisher?.oidcConfigId, proof.publisherId, 'unexpected Trusted Publisher');
  requireValue(Array.isArray(published.dist.signatures) && published.dist.signatures.length > 0 &&
    published.dist.signatures.every(s => s.keyid && s.sig), 'registry signatures unavailable');
  assert.equal(published.dist.attestations?.provenance?.predicateType, 'https://slsa.dev/provenance/v1', 'provenance unavailable');
  requireValue(published.dist.attestations?.url?.startsWith(`${REGISTRY}/-/npm/v1/attestations/`), 'unexpected attestation URL');
  assert.equal(published.dist.tarball, `${REGISTRY}/@bytefolk/mem-mcp/-/mem-mcp-${version}.tgz`, 'unexpected registry tarball URL');
  return published;
}

async function registryJSON(url) {
  const response = await fetch(url, { redirect: 'error', signal: AbortSignal.timeout(30000),
    headers: { accept: 'application/json', 'cache-control': 'no-cache' } });
  requireValue(response.status === 200, `registry read failed (${response.status}); no write permitted`);
  return response.json();
}

export async function runRelease(tag, options = {}) {
  const repo = options.repo || resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const env = options.env || process.env;
  const now = options.now || Date.now;
  const proof = JSON.parse(env.NPM_RELEASE_PROOF || 'null');
  checkProof(proof, tag, env.GITHUB_SHA, now());
  const directory = resolve(options.directory || join(env.RUNNER_TEMP || '', 'mem-npm-release'));
  requireValue(!existsSync(directory), 'release output directory must be fresh');
  // Refuse ambient npmrc files, and bypass user/global config in all npm calls.
  requireValue(!existsSync(join(repo, '.npmrc')) && !existsSync(join(repo, 'npm/.npmrc')), 'repository npmrc requires explicit security review');
  mkdirSync(directory, { recursive: true });
  const npmEnv = { ...env, NPM_CONFIG_USERCONFIG: join(directory, 'user.npmrc'),
    NPM_CONFIG_GLOBALCONFIG: join(directory, 'global.npmrc'), NPM_CONFIG_CACHE: join(directory, 'cache') };
  delete npmEnv.GH_TOKEN;
  delete npmEnv.GITHUB_TOKEN;
  for (const name of ['user.npmrc', 'global.npmrc']) writeFileSync(join(directory, name), '', { flag: 'wx', mode: 0o600 });
  const run = options.run || ((command, args, cwd = repo) => {
    try {
      return execFileSync(command, args, { cwd, encoding: 'utf8', env: command === 'npm' ? npmEnv : env,
        stdio: ['ignore', 'pipe', 'pipe'], timeout: 120000, maxBuffer: 16 * 1024 * 1024 }).trim();
    } catch {
      // Never echo raw auth errors, subprocess output or environment values.
      throw new Error(`${command} ${args[0]} failed; stop and inspect the private run. Do not retry publication automatically.`);
    }
  });
  const getJSON = options.getJSON || registryJSON;
  checkContext(tag, env, options.nodeVersion || process.versions.node, run('npm', ['--version']));
  const source = () => {
    run('git', ['fetch', '--no-tags', 'origin', 'refs/heads/main:refs/remotes/origin/main', `refs/tags/${tag}:refs/tags/${tag}`]);
    assert.equal(run('git', ['cat-file', '-t', `refs/tags/${tag}`]), 'tag', 'annotated tag required');
    const commit = run('git', ['rev-parse', `refs/tags/${tag}^{commit}`]);
    assert.equal(commit, env.GITHUB_SHA, 'tag/event commit mismatch');
    assert.equal(run('git', ['rev-parse', 'HEAD']), commit, 'checkout/tag mismatch');
    run('git', ['merge-base', '--is-ancestor', commit, 'refs/remotes/origin/main']);
    assert.equal(run('git', ['status', '--porcelain', '--untracked-files=all']), '', 'release checkout must be clean');
    run('bash', [join(repo, 'scripts/validate_release_version.sh'), tag.slice(1)]);
    checkPackage(readJSON(join(repo, 'npm/package.json')), readJSON(join(repo, 'npm/server.json')), tag);
    return commit;
  };
  const commit = source();
  const releaseJSON = () => JSON.parse(run('gh', ['api', `repos/bytefolk/mem/releases/tags/${tag}`]));
  let release = releaseJSON();
  checkRelease(release, tag);
  const releaseId = release.id;
  if (env.GITHUB_EVENT_NAME === 'release') {
    const event = readJSON(env.GITHUB_EVENT_PATH);
    requireValue(event.action === 'published' && event.release?.id === releaseId && event.release?.tag_name === tag, 'Release event mismatch');
  }
  const url = `${REGISTRY}/@bytefolk%2fmem-mcp`;
  const before = await getJSON(url);
  checkRegistryBefore(before, tag);
  const assets = join(directory, 'assets');
  mkdirSync(assets);
  run('gh', ['release', 'download', tag, '--repo', 'bytefolk/mem', '--dir', assets,
    ...[...ASSETS, MANIFEST].flatMap(name => ['--pattern', name])]);
  release = releaseJSON();
  checkRelease(release, tag);
  assert.equal(release.id, releaseId, 'Release replaced while downloading');
  checkAssets(assets, release, commit, run);
  const packed = JSON.parse(run('npm', ['pack', '--json', '--ignore-scripts', '--pack-destination', directory], join(repo, 'npm')));
  requireValue(Array.isArray(packed) && packed.length === 1, 'exactly one packed tarball required');
  const pack = packed[0];
  assert.equal(pack.name, PACKAGE, 'packed package name mismatch');
  assert.equal(pack.version, tag.slice(1), 'packed package version mismatch');
  assert.equal(pack.filename, `bytefolk-mem-mcp-${tag.slice(1)}.tgz`, 'unexpected tarball filename');
  assert.deepEqual(pack.files?.map(f => f.path).sort(), FILES, 'unexpected packed file inventory');
  const tarball = join(directory, pack.filename);
  const integrity = `sha512-${sha('sha512', readFileSync(tarball))}`;
  assert.equal(pack.integrity, integrity, 'packed tarball integrity mismatch');
  // Final checks immediately before the only registry write. Missing reads,
  // races and moved tags stop; no exception is interpreted as version absence.
  checkProof(proof, tag, source(), now());
  const currentRelease = releaseJSON();
  checkRelease(currentRelease, tag);
  assert.deepEqual(releaseIdentity(currentRelease), releaseIdentity(release), 'GitHub Release changed after verification');
  const current = await getJSON(url);
  checkRegistryBefore(current, tag);
  assert.deepEqual(current['dist-tags'], before['dist-tags'], 'registry channels changed during preflight');
  assert.equal(`sha512-${sha('sha512', readFileSync(tarball))}`, integrity, 'tarball changed after packing');
  writeFileSync(join(directory, 'preflight.json'), JSON.stringify({ package: PACKAGE, tag, commit, integrity, releaseId, channel: 'next' }, null, 2));
  run('npm', ['publish', tarball, '--tag', 'next', '--access', 'public', '--provenance', '--ignore-scripts', `--registry=${REGISTRY}`], directory);
  // A failure here may mean publish succeeded. Never retry npm publish, move a
  // dist-tag, delete a version, or mark the run successful on that basis.
  const published = checkRegistryAfter(await getJSON(url), before, tag, integrity, proof);
  const consumer = join(directory, 'consumer');
  mkdirSync(consumer);
  writeFileSync(join(consumer, 'package.json'), '{"private":true}\n');
  run('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', '--save-exact', `${PACKAGE}@${tag.slice(1)}`, `--registry=${REGISTRY}`], consumer);
  run('npm', ['audit', 'signatures', `--registry=${REGISTRY}`], consumer);
  checkRegistryAfter(await getJSON(url), before, tag, integrity, proof);
  const receipt = { package: PACKAGE, tag, commit, integrity, releaseId, channel: 'next',
    publisherId: proof.publisherId, attestations: published.dist.attestations.url,
    signatures: 'verified by npm audit signatures', latestPromotion: 'NOT PERFORMED: separate release-owner gate',
    platformLaunch: 'NOT VERIFIED: release owner must record Linux/macOS/Windows clean launches' };
  writeFileSync(join(directory, 'receipt.json'), JSON.stringify(receipt, null, 2));
  return receipt;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    requireValue(process.argv.length === 3, 'usage: node scripts/npm-release.mjs vX.Y.Z');
    const receipt = await runRelease(process.argv[2]);
    process.stdout.write(`${JSON.stringify(receipt, null, 2)}\n`);
  } catch (error) {
    process.stderr.write(`HOLD: ${error.message}\n`);
    process.exitCode = 1;
  }
}
