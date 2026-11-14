import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { createServer } from 'node:http'
import { mkdtemp, mkdir, readFile, readdir, realpath, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'
import { test } from 'node:test'
import { nodeCommands, nodeDownloadURL } from '../src/lib/node-bootstrap.ts'

const execute = promisify(execFile)
const defaults = {
  os: process.platform === 'win32' ? 'windows' : 'linux', arch: 'amd64',
  distribution: 'cyber-scan', source: 'global', version: 'v1.2.3',
  serverURL: 'http://localhost:8080', accessToken: "key-'$&`", nodeName: "node '$&` name",
}

test('download links select the profile, platform and Hub release without credentials', () => {
  for (const os of ['linux', 'darwin', 'windows']) {
    for (const arch of ['amd64', 'arm64']) {
      for (const distribution of ['cyber-scan', 'cyber-audit', 'cyber-custom']) {
        const options = { ...defaults, os, arch, distribution }
        const binary = distribution
        assert.equal(nodeDownloadURL(options), `https://github.com/chainreactors/cyber-harness/releases/download/v1.2.3/${binary}_${os}_${arch}.zip`)
        const executable = os === 'windows' ? `.\\${binary}.exe` : `./${binary}`
        const command = distribution === 'cyber-scan' ? ' agent' : ''
        assert.ok(nodeCommands(options).connect.startsWith(`${executable}${command} --server-url `))
      }
    }
  }
  assert.match(nodeDownloadURL({ ...defaults, version: 'dev' }), /releases\/latest\/download/)
  assert.match(nodeDownloadURL({ ...defaults, source: 'china' }), /^https:\/\/ghfast.top\/https:\/\/github.com\//)
})

test('one-line bootstrap downloads, executes and cleans up on success and failure', { timeout: 120_000 }, async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'cyber-bootstrap-test-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const fixture = join(root, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
  const source = fileURLToPath(new URL('./fixtures/testdata/bootstrap-node.go', import.meta.url))
  await execute('go', ['build', '-o', fixture, source], {
    env: { ...process.env, CGO_ENABLED: '0', GOWORK: 'off' }, timeout: 60_000,
  })
  const archives = new Map()
  for (const distribution of ['cyber-scan', 'cyber-audit']) {
    const archive = join(root, `${distribution}.zip`)
    const name = distribution
    const binary = name + (process.platform === 'win32' ? '.exe' : '')
    await execute(fixture, ['--archive', archive, binary])
    archives.set(distribution, await readFile(archive))
  }
  const requests = []
  const server = createServer((req, res) => {
    requests.push(req.url)
    if (req.url === '/missing') {
      res.writeHead(503).end('download failed')
    } else if (req.url === '/invalid') {
      res.end('not a zip archive')
    } else {
      res.end(archives.get(req.url.slice(1)))
    }
  })
  await new Promise(resolveListen => server.listen(0, '127.0.0.1', resolveListen))
  t.after(() => new Promise(resolveClose => server.close(resolveClose)))
  const endpoint = `http://127.0.0.1:${server.address().port}`
  for (const scenario of [
    { distribution: 'cyber-scan', path: '/cyber-scan', code: 0 },
    { distribution: 'cyber-audit', path: '/cyber-audit', code: 0 },
    { distribution: 'cyber-scan', path: '/missing', code: 0, downloadFailure: true },
    { distribution: 'cyber-audit', path: '/invalid', code: 0, downloadFailure: true },
    { distribution: 'cyber-scan', path: '/cyber-scan', code: 7 },
  ]) {
    await t.test(`${scenario.distribution} ${scenario.path} exit ${scenario.code}`, async () => {
      const workdir = await mkdtemp(join(root, 'work-'))
      const temporary = join(workdir, 'temporary files')
      await mkdir(temporary)
      await writeFile(join(workdir, 'sentinel.txt'), 'preserve workspace')
      const capture = join(workdir, 'capture.json')
      const options = { ...defaults, distribution: scenario.distribution }
      // Serve a release fixture locally while executing the exact generated
      // command. No public releases or credentials are needed for these tests.
      const script = nodeCommands(options).install.replace(nodeDownloadURL(options), endpoint + scenario.path)
      const env = { ...process.env, TMPDIR: temporary, TEMP: temporary, TMP: temporary,
        BOOTSTRAP_CAPTURE: capture, BOOTSTRAP_EXIT: String(scenario.code) }
      let error
      try {
        if (process.platform === 'win32') {
          await execute('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', script], { cwd: workdir, env, timeout: 30_000 })
        } else {
          await execute('/bin/sh', ['-c', script], { cwd: workdir, env, timeout: 30_000 })
        }
      } catch (caught) { error = caught }
      assert.equal(Boolean(error), Boolean(scenario.downloadFailure || scenario.code), error?.stderr)
      assert.equal(await readFile(join(workdir, 'sentinel.txt'), 'utf8'), 'preserve workspace')
      assert.deepEqual(await readdir(temporary), [], 'temporary release directory was not cleaned')
      if (scenario.downloadFailure) {
        await assert.rejects(readFile(capture), { code: 'ENOENT' })
      } else {
        const result = JSON.parse(await readFile(capture, 'utf8'))
        const url = new URL(options.serverURL)
        url.username = options.accessToken
        const expected = ['--server-url', url.toString().replace(/\/$/, ''), '--node-name', options.nodeName]
        if (scenario.distribution === 'cyber-scan') expected.unshift('agent')
        assert.deepEqual(result.args, expected, 'shell quoting changed the connection arguments')
        assert.equal(await realpath(result.cwd), await realpath(workdir), 'bootstrap changed the node workspace')
        assert.equal(await realpath(dirname(dirname(result.binary))), await realpath(temporary), 'node did not run in its private download directory')
      }
    })
  }
  assert.equal(requests.length, 5)
})
