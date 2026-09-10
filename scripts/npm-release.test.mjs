import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { fstatSync, mkdtempSync, mkdirSync, readFileSync, readSync, renameSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import {
  ASSETS, PACKAGE, REGISTRY, checkContext, checkProof, checkPackage,
  checkRelease, checkAssets, checkRegistryBefore, checkRegistryAfter, commandStdio, readReleaseFile, runRelease,
} from './npm-release.mjs';

const tag = 'v0.1.2';
const commit = 'a'.repeat(40);
const now = Date.parse('2026-09-10T00:00:00Z');
const env = () => ({
  GITHUB_ACTIONS: 'true', GITHUB_REPOSITORY: 'bytefolk/mem',
  GITHUB_EVENT_NAME: 'workflow_dispatch', GITHUB_REF: `refs/tags/${tag}`,
  GITHUB_SHA: commit, GITHUB_WORKFLOW_REF: `bytefolk/mem/.github/workflows/npm-publish.yml@refs/tags/${tag}`,
  RUNNER_ENVIRONMENT: 'github-hosted', RUNNER_OS: 'Linux',
  ACTIONS_ID_TOKEN_REQUEST_URL: 'https://example.invalid/oidc',
  ACTIONS_ID_TOKEN_REQUEST_TOKEN: 'fixture-only',
});
const proof = () => ({
  schema: 1, package: PACKAGE, tag, commit, channel: 'next',
  organization: 'bytefolk', organizationControlVerified: true,
  packageAccessVerified: true, twoFactorVerified: true,
  publisherVerified: true, allowPublish: true,
  repository: 'bytefolk/mem', workflow: 'npm-publish.yml', environment: 'npm-release',
  publisherId: 'oidc:fixture', approvedBy: 'release-owner',
  evidence: 'https://github.com/bytefolk/mem/issues/153#issuecomment-123',
  verifiedAt: '2026-09-09T23:00:00Z', expiresAt: '2026-09-10T23:00:00Z',
});
const pkg = () => ({
  name: PACKAGE, version: '0.1.2', mcpName: 'io.github.bytefolk/mem-mcp',
  repository: { type: 'git', url: 'git+https://github.com/bytefolk/mem.git' },
  bin: { 'mem-mcp': './mem-mcp' },
});
const server = () => ({ version: '0.1.2', name: 'mem-mcp', mcpName: 'io.github.bytefolk/mem-mcp' });
const release = () => ({ id: 123, tag_name: tag, draft: false, prerelease: false,
  published_at: '2026-09-09T23:00:00Z', html_url: `https://github.com/bytefolk/mem/releases/tag/${tag}`,
  assets: [...ASSETS, 'mem-mcp-checksums.txt'].map(name => ({ name, size: 1, state: 'uploaded' })) });
const before = () => ({ name: PACKAGE, versions: { '0.1.2-rc.0': { name: PACKAGE, version: '0.1.2-rc.0' } },
  'dist-tags': { next: '0.1.2-rc.0' } });
const integrity = 'sha512-' + Buffer.alloc(64, 1).toString('base64');
const after = () => ({ ...before(), 'dist-tags': { next: '0.1.2' }, versions: {
  ...before().versions, '0.1.2': { ...pkg(), bin: { 'mem-mcp': 'mem-mcp' },
    _npmUser: { trustedPublisher: { id: 'github', oidcConfigId: 'oidc:fixture' } },
    dist: { integrity, tarball: `${REGISTRY}/@bytefolk/mem-mcp/-/mem-mcp-0.1.2.tgz`,
      signatures: [{ keyid: 'SHA256:fixture', sig: 'fixture' }],
      attestations: { url: `${REGISTRY}/-/npm/v1/attestations/@bytefolk%2fmem-mcp@0.1.2`,
        provenance: { predicateType: 'https://slsa.dev/provenance/v1' } } } } } });

test('accepts exact hosted tag context, current human proof, package, assets and registry readback', () => {
  checkContext(tag, env(), '24.13.0', '11.15.0');
  checkProof(proof(), tag, commit, now);
  checkPackage(pkg(), server(), tag);
  checkRelease(release(), tag);
  checkRegistryBefore(before(), tag);
  checkRegistryAfter(after(), before(), tag, integrity, proof());
});

for (const bad of ['', 'v01.2.3', '0.1.2', 'v0.1.2-rc.0', 'v0.1.2+build', 'v0.1.2\n', 'v0.1.2;id', '--help']) {
  test(`rejects unsafe/nonstable tag ${JSON.stringify(bad)}`, () => {
    assert.throws(() => checkContext(bad, env(), '24.13.0', '11.15.0'));
  });
}
for (const [field, value] of [
  ['GITHUB_REPOSITORY', 'attacker/mem'], ['GITHUB_EVENT_NAME', 'pull_request'],
  ['GITHUB_REF', 'refs/heads/main'], ['GITHUB_SHA', ''], ['GITHUB_ACTIONS', 'false'],
  ['RUNNER_ENVIRONMENT', 'self-hosted'], ['ACTIONS_ID_TOKEN_REQUEST_TOKEN', ''],
  ['GITHUB_WORKFLOW_REF', 'bytefolk/mem/.github/workflows/other.yml@refs/tags/v0.1.2'],
  ['NPM_TOKEN', 'fixture'], ['NODE_AUTH_TOKEN', 'fixture'], ['npm_config_provenance', 'false'],
]) {
  test(`rejects wrong context or credential/config injection: ${field}`, () => {
    assert.throws(() => checkContext(tag, { ...env(), [field]: value }, '24.13.0', '11.15.0'));
  });
}
for (const [node, npm] of [['22.0.0', '11.15.0'], ['24.13.0', '11.6.2'], ['24.13.0', '11.15.0-rc.1']]) {
  test(`rejects unsupported tools ${node}/${npm}`, () => assert.throws(() => checkContext(tag, env(), node, npm)));
}
for (const [field, value] of [
  ['organizationControlVerified', false], ['packageAccessVerified', false], ['twoFactorVerified', false],
  ['publisherVerified', false], ['allowPublish', false], ['publisherId', ''],
  ['environment', 'production'], ['workflow', 'release.yml'], ['repository', 'someone/mem'],
  ['package', '@fullstack-ai-infra/mem-mcp'], ['channel', 'latest'], ['tag', 'v0.1.3'],
  ['commit', 'b'.repeat(40)], ['approvedBy', ''], ['evidence', ''],
  ['expiresAt', '2026-09-09T23:59:00Z'], ['verifiedAt', '2026-09-11T00:00:00Z'],
  ['expiresAt', '2027-01-01T00:00:00Z'],
]) {
  test(`fails closed on missing/wrong/stale owner proof: ${field}`, () => {
    assert.throws(() => checkProof({ ...proof(), [field]: value }, tag, commit, now));
  });
}
test('no owner proof is a blocker', () => assert.throws(() => checkProof(null, tag, commit, now)));
for (const mutate of [
  p => { p.name = '@fullstack-ai-infra/mem-mcp'; }, p => { p.version = '0.1.1'; },
  p => { p.private = true; }, p => { p.repository.url = 'git+https://github.com/attacker/mem.git'; },
  p => { p.publishConfig = { tag: 'latest' }; }, p => { p.publishConfig = { registry: 'https://example.invalid' }; },
  p => { p.scripts = { prepublishOnly: 'dangerous' }; }, p => { p.dependencies = { surprise: '*' }; },
]) {
  test(`rejects unsafe package: ${mutate}`, () => { const p = pkg(); mutate(p); assert.throws(() => checkPackage(p, server(), tag)); });
}
for (const mutate of [
  r => { r.draft = true; }, r => { r.prerelease = true; }, r => { r.tag_name = 'v0.1.1'; },
  r => { r.assets.pop(); }, r => { r.assets.push(r.assets[0]); }, r => { r.assets[0].size = 0; },
  r => { r.assets[0].name = '../outside'; }, r => { r.assets[0].state = 'new'; },
]) {
  test(`rejects incomplete or mismatched Release: ${mutate}`, () => { const r = release(); mutate(r); assert.throws(() => checkRelease(r, tag)); });
}
test('registry 404/error-shaped or duplicate stable version is never publish permission', () => {
  for (const data of [null, {}, { error: 'Not found' }, after()]) assert.throws(() => checkRegistryBefore(data, tag));
});
for (const mutate of [
  r => { r['dist-tags'].latest = '0.1.2'; }, r => { r['dist-tags'].next = '0.1.2-rc.0'; },
  r => { r.versions['0.1.2'].dist.integrity = 'bad'; },
  r => { delete r.versions['0.1.2'].dist.attestations; },
  r => { delete r.versions['0.1.2'].dist.signatures; },
  r => { r.versions['0.1.2']._npmUser.trustedPublisher.oidcConfigId = 'oidc:other'; },
  r => { r.versions['0.1.2'].repository.url = 'https://github.com/attacker/mem'; },
]) {
  test(`readback failure blocks success: ${mutate}`, () => { const r = after(); mutate(r); assert.throws(() => checkRegistryAfter(r, before(), tag, integrity, proof())); });
}

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'mem-npm-release-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const repo = join(root, 'repo');
  mkdirSync(join(repo, 'npm'), { recursive: true });
  writeFileSync(join(repo, 'npm/package.json'), JSON.stringify(pkg()));
  writeFileSync(join(repo, 'npm/server.json'), JSON.stringify(server()));
  const directory = join(root, 'out');
  const calls = [];
  let registryReads = 0;
  let published = false;
  const r = release();
  const run = (command, args, cwd, descriptor) => {
    calls.push([command, ...args]);
    if (command === 'git') {
      if (args[0] === 'cat-file') return 'tag';
      if (args[0] === 'rev-parse') return commit;
      return '';
    }
    if (command === 'bash') return '';
    if (command === 'gh' && args[0] === 'api') return JSON.stringify(r);
    if (command === 'gh' && args[0] === 'release') {
      const assetDir = args[args.indexOf('--dir') + 1];
      let manifest = '';
      for (const name of ASSETS) {
        const body = Buffer.from(`fixture ${name}`);
        writeFileSync(join(assetDir, name), body);
        r.assets.find(a => a.name === name).size = body.length;
        manifest += `${createHash('sha256').update(body).digest('hex')}  ${name}\n`;
      }
      writeFileSync(join(assetDir, 'mem-mcp-checksums.txt'), manifest);
      r.assets.at(-1).size = Buffer.byteLength(manifest);
      return '';
    }
    if (command === 'go') {
      let bytes;
      if (descriptor === undefined) bytes = readFileSync(args.at(-1));
      else {
        assert.equal(args.at(-1), '/proc/self/fd/3');
        bytes = Buffer.alloc(fstatSync(descriptor).size);
        assert.equal(readSync(descriptor, bytes, 0, bytes.length, 0), bytes.length);
      }
      const [, name, revision = commit] = bytes.toString().split(' ');
      const [, , os, arch] = name.replace('.exe', '').split('-');
      return `\tpath\tgithub.com/PeterGuy326/mem/server/cmd/mem-mcp\n\tbuild\tGOOS=${os}\n\tbuild\tGOARCH=${arch}\n\tbuild\tvcs.revision=${revision}\n\tbuild\tvcs.modified=false\n`;
    }
    if (command === 'npm') {
      if (args[0] === '--version') return '11.15.0';
      if (args[0] === 'pack') {
        const body = Buffer.from('test tarball');
        writeFileSync(join(directory, 'bytefolk-mem-mcp-0.1.2.tgz'), body);
        return JSON.stringify([{ name: PACKAGE, version: '0.1.2', filename: 'bytefolk-mem-mcp-0.1.2.tgz',
          integrity: 'sha512-' + createHash('sha512').update(body).digest('base64'),
          files: ['LICENSE', 'README.md', 'install.js', 'mem-mcp', 'package.json', 'platforms.js'].map(path => ({ path })) }]);
      }
      if (args[0] === 'publish') { published = true; return ''; }
      if (args[0] === 'install' || args[0] === 'audit') return '';
    }
    throw Error(`unexpected command: ${command} ${args.join(' ')}, cwd=${cwd}`);
  };
  const getJSON = async () => {
    registryReads++;
    if (!published) return before();
    const data = after();
    data.versions['0.1.2'].dist.integrity = 'sha512-' + createHash('sha512').update('test tarball').digest('base64');
    return data;
  };
  return { repo, directory, env: { ...env(), NPM_RELEASE_PROOF: JSON.stringify(proof()) },
    now: () => now, nodeVersion: '24.13.0', run, getJSON, calls, r,
    readCount: () => registryReads };
}

test('asset read holds one descriptor across path replacement and always closes it', t => {
  const f = fixture(t);
  const file = join(f.repo, 'asset');
  writeFileSync(file, 'original');
  let descriptor;
  const bytes = readReleaseFile(file, 8, fd => {
    descriptor = fd;
    renameSync(file, file + '.saved');
    writeFileSync(file, 'replaced');
    return readFileSync(fd);
  });
  assert.equal(bytes.toString(), 'original');
  assert.equal(readFileSync(file, 'utf8'), 'replaced');
  assert.throws(() => fstatSync(descriptor), /EBADF/);
});

test('asset descriptor rejects symlinks, directories and wrong sizes', t => {
  const f = fixture(t);
  const file = join(f.repo, 'asset');
  writeFileSync(file, 'original');
  symlinkSync(file, file + '.link');
  assert.throws(() => readReleaseFile(file + '.link', 8));
  assert.throws(() => readReleaseFile(f.repo, 8), /regular file/);
  assert.throws(() => readReleaseFile(file, 7), /size mismatch/);
  let descriptor;
  assert.throws(() => readReleaseFile(file, 8, fd => { descriptor = fd; throw Error('read failed'); }), /read failed/);
  assert.throws(() => fstatSync(descriptor), /EBADF/);
});

test('real child metadata transport keeps the verified descriptor across path replacement', t => {
  const f = fixture(t);
  const file = join(f.repo, 'asset');
  writeFileSync(file, 'original');
  readReleaseFile(file, 8, readFileSync, fd => {
    renameSync(file, file + '.saved');
    writeFileSync(file, 'replaced');
    const path = process.platform === 'linux' ? '/proc/self/fd/3' : '/dev/fd/3';
    const child = `const fs = require('node:fs'); const fd = fs.openSync(process.argv[1], 'r');
      const bytes = Buffer.alloc(fs.fstatSync(fd).size); fs.readSync(fd, bytes, 0, bytes.length, 0);
      fs.closeSync(fd); process.stdout.write(bytes);`;
    assert.equal(execFileSync(process.execPath, ['-e', child, path], { stdio: commandStdio(fd) }).toString(), 'original');
  });
});

for (const replacedIndex of [0, 1]) {
  test(`metadata and checksum cannot validate different objects: asset ${replacedIndex}`, t => {
    const f = fixture(t);
    const assets = join(f.directory, 'assets');
    mkdirSync(assets, { recursive: true });
    f.run('gh', ['release', 'download', tag, '--dir', assets]);
    const name = ASSETS[replacedIndex];
    const path = join(assets, name);
    const wrong = Buffer.from(`fixture ${name} ${'b'.repeat(40)}`);
    writeFileSync(path, wrong);
    f.r.assets.find(a => a.name === name).size = wrong.length;
    const manifestPath = join(assets, 'mem-mcp-checksums.txt');
    const manifest = readFileSync(manifestPath, 'utf8').split('\n').map(line =>
      line.endsWith(`  ${name}`) ? `${createHash('sha256').update(wrong).digest('hex')}  ${name}` : line).join('\n');
    writeFileSync(manifestPath, manifest);
    let replaced = false;
    const run = (command, args, cwd, descriptor) => {
      if (command === 'go' && !replaced) {
        replaced = true;
        renameSync(path, path + '.saved');
        writeFileSync(path, `fixture ${name} ${commit}`);
      }
      return f.run(command, args, cwd, descriptor);
    };
    assert.throws(() => checkAssets(assets, f.r, commit, run), /checksum mismatch|build metadata mismatch/);
    assert.equal(replaced, true);
  });
}

test('receipt records verified local facts, not a registry-supplied payload', async t => {
  const f = fixture(t);
  const original = f.getJSON;
  f.getJSON = async () => {
    const data = await original();
    if (data.versions['0.1.2']) data.versions['0.1.2'].dist.attestations.url = `${REGISTRY}/-/npm/v1/attestations/server-controlled-marker`;
    return data;
  };
  const receipt = await runRelease(tag, f);
  assert.equal(receipt.registryMetadata, `${REGISTRY}/@bytefolk%2fmem-mcp`);
  assert.equal(receipt.attestations, 'verified by npm audit signatures');
  assert.ok(!readFileSync(join(f.directory, 'receipt.json'), 'utf8').includes('server-controlled-marker'));
});

test('full fixture publishes the checked tarball once to next then verifies signatures; no latest mutation', async t => {
  const f = fixture(t);
  await runRelease(tag, f);
  const writes = f.calls.filter(c => c[0] === 'npm' && c[1] === 'publish');
  assert.equal(writes.length, 1);
  assert.deepEqual(writes[0].slice(3), ['--tag', 'next', '--access', 'public', '--provenance', '--ignore-scripts', `--registry=${REGISTRY}`]);
  assert.ok(f.calls.some(c => c.join(' ') === `npm audit signatures --registry=${REGISTRY}`));
  assert.ok(f.readCount() >= 3);
  assert.ok(!f.calls.some(c => c.includes('dist-tag') || c.includes('deprecate')));
  assert.ok(readFileSync(join(f.directory, 'receipt.json'), 'utf8').includes('next'));
});

for (const failure of ['proof', 'source', 'registry', 'assets', 'pack', 'recheck', 'publish', 'readback', 'audit']) {
  test(`full fixture stops at ${failure}; no publish retry or promotion`, async t => {
    const f = fixture(t);
    const original = f.run;
    let sourceChecks = 0;
    if (failure === 'proof') delete f.env.NPM_RELEASE_PROOF;
    if (failure === 'registry' || failure === 'readback') {
      const get = f.getJSON;
      f.getJSON = async () => { if (failure === 'registry' || f.calls.some(c => c[1] === 'publish')) throw Error('fixture unavailable'); return get(); };
    }
    f.run = (command, args, cwd, descriptor) => {
      if (command === 'git' && args[0] === 'fetch') {
        sourceChecks++;
        if (failure === 'source' || (failure === 'recheck' && sourceChecks > 1)) throw Error('source moved');
      }
      if ((failure === 'pack' && args[0] === 'pack') || (failure === 'publish' && args[0] === 'publish') ||
          (failure === 'audit' && args[0] === 'audit')) { f.calls.push([command, ...args]); throw Error('fixture failure'); }
      const result = original(command, args, cwd, descriptor);
      if (failure === 'assets' && command === 'gh' && args[0] === 'release') writeFileSync(join(f.directory, 'assets', ASSETS[0]), 'tampered');
      return result;
    };
    await assert.rejects(runRelease(tag, f));
    const count = f.calls.filter(c => c[0] === 'npm' && c[1] === 'publish').length;
    assert.equal(count, ['publish', 'readback', 'audit'].includes(failure) ? 1 : 0);
    assert.ok(!f.calls.some(c => c.includes('dist-tag')));
  });
}

test('checksum parser rejects duplicate, traversal, missing and mismatched rows', t => {
  const f = fixture(t);
  mkdirSync(join(f.directory, 'assets'), { recursive: true });
  f.run('gh', ['release', 'download', tag, '--dir', join(f.directory, 'assets')]);
  const path = join(f.directory, 'assets/mem-mcp-checksums.txt');
  const good = readFileSync(path, 'utf8');
  checkAssets(join(f.directory, 'assets'), f.r, commit, f.run);
  for (const bad of [good + good.split('\n')[0] + '\n', good.replace(ASSETS[0], '../outside'), good.split('\n').slice(1).join('\n'), good.replace(/^[a-f0-9]/, 'z')]) {
    writeFileSync(path, bad);
    assert.throws(() => checkAssets(join(f.directory, 'assets'), f.r, commit, f.run));
  }
});

for (const changed of ['download_count', 'digest']) {
  test(`final Release readback handles changing ${changed}`, async t => {
    const f = fixture(t);
    const original = f.run;
    let releaseReads = 0;
    f.run = (command, args, cwd, descriptor) => {
      if (command === 'gh' && args[0] === 'api' && ++releaseReads === 3) {
        f.r.assets[0][changed] = changed === 'digest' ? 'sha256:' + 'b'.repeat(64) : 17;
      }
      return original(command, args, cwd, descriptor);
    };
    if (changed === 'digest') {
      await assert.rejects(runRelease(tag, f), /Release changed/);
      assert.ok(!f.calls.some(c => c[0] === 'npm' && c[1] === 'publish'));
    } else {
      await runRelease(tag, f);
    }
  });
}
