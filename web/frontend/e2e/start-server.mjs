import { createServer } from 'node:http'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawn, spawnSync } from 'node:child_process'

const host = '127.0.0.1'
const webPort = Number(process.env.AISCAN_E2E_PORT || 38080)
const root = resolve(fileURLToPath(new URL('../../..', import.meta.url)))
const workDir = await mkdtemp(join(tmpdir(), 'aiscan-web-e2e-'))
const binary = join(workDir, process.platform === 'win32' ? 'aiscan-e2e.exe' : 'aiscan-e2e')

const externalLLM = {
  baseURL: process.env.AISCAN_E2E_LLM_BASE_URL?.trim() || '',
  apiKey: process.env.AISCAN_E2E_LLM_API_KEY?.trim() || '',
  model: process.env.AISCAN_E2E_LLM_MODEL?.trim() || '',
}
const externalLLMValues = Object.values(externalLLM).filter(Boolean).length
if (externalLLMValues > 0 && externalLLMValues < 3) {
  throw new Error('AISCAN_E2E_LLM_BASE_URL, AISCAN_E2E_LLM_API_KEY and AISCAN_E2E_LLM_MODEL must be set together')
}

let llmBaseURL = externalLLM.baseURL
let llmAPIKey = externalLLM.apiKey
let llmModel = externalLLM.model
let mockLLM = null

if (externalLLMValues === 0) {
  mockLLM = createServer(async (req, res) => {
    if (req.url === '/v1/models') {
      res.writeHead(200, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify({ data: [{ id: 'deepseek-chat', object: 'model' }] }))
      return
    }
    if (req.url !== '/v1/chat/completions' || req.method !== 'POST') {
      res.writeHead(404)
      res.end('not found')
      return
    }

    const chunks = []
    for await (const chunk of req) chunks.push(chunk)
    const payload = JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}')
    const delayedReply = JSON.stringify(payload.messages || []).includes('Reply with exactly one word: PONG')
    if (payload.stream) {
      res.writeHead(200, {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
      })
      if (delayedReply) {
        res.write('data: {"choices":[{"delta":{"role":"assistant","content":"P"},"index":0}]}\n\n')
        await new Promise((resolveDelay) => setTimeout(resolveDelay, 3000))
        res.write('data: {"choices":[{"delta":{"content":"ONG"},"index":0}]}\n\n')
      } else {
        res.write('data: {"choices":[{"delta":{"role":"assistant","content":"PONG"},"index":0}]}\n\n')
      }
      res.write('data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}\n\n')
      res.end('data: [DONE]\n\n')
      return
    }

    if (delayedReply) await new Promise((resolveDelay) => setTimeout(resolveDelay, 3000))
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({
      id: 'chatcmpl-e2e',
      choices: [{ message: { role: 'assistant', content: 'PONG' }, finish_reason: 'stop' }],
      usage: { prompt_tokens: 10, completion_tokens: 1, total_tokens: 11 },
    }))
  })

  await new Promise((resolveListen, reject) => {
    mockLLM.once('error', reject)
    mockLLM.listen(0, host, resolveListen)
  })
  const llmAddress = mockLLM.address()
  if (!llmAddress || typeof llmAddress === 'string') throw new Error('mock LLM did not expose a TCP address')
  llmBaseURL = `http://${host}:${llmAddress.port}/v1`
  llmAPIKey = 'test-key'
  llmModel = 'deepseek-chat'
}

const configPath = join(workDir, 'aiscan.yaml')
await writeFile(configPath, `llm:
  active_profile: e2e
  providers:
    - id: e2e
      name: E2E DeepSeek
      provider: openai
      base_url: ${JSON.stringify(llmBaseURL)}
      api_key: ${JSON.stringify(llmAPIKey)}
      model: ${JSON.stringify(llmModel)}
`, { mode: 0o600 })

const npmCommand = process.platform === 'win32' ? 'cmd.exe' : 'npm'
const npmArgs = process.platform === 'win32' ? ['/d', '/s', '/c', 'npm run build'] : ['run', 'build']
const frontendBuild = spawnSync(npmCommand, npmArgs, {
  cwd: resolve(root, 'web/frontend'),
  stdio: 'inherit',
})
if (frontendBuild.status !== 0) {
  if (frontendBuild.error) console.error(frontendBuild.error)
  mockLLM?.close()
  await rm(workDir, { recursive: true, force: true })
  process.exit(frontendBuild.status ?? 1)
}

const build = spawnSync('go', ['build', '-tags', 'full', '-o', binary, './cmd/aiscan'], {
  cwd: root,
  stdio: 'inherit',
})
if (build.status !== 0) {
  mockLLM?.close()
  await rm(workDir, { recursive: true, force: true })
  process.exit(build.status ?? 1)
}

const child = spawn(binary, [
  '--config', configPath,
  '--data-dir', join(workDir, 'data'),
  'web',
  '--addr', `${host}:${webPort}`,
  '--db', join(workDir, 'aiscan-web.db'),
  '--token', 'test-token',
], {
  cwd: root,
  stdio: 'inherit',
})

let shuttingDown = false
async function shutdown(code) {
  if (shuttingDown) return
  shuttingDown = true
  if (child.exitCode === null) child.kill()
  if (mockLLM) await new Promise((resolveClose) => mockLLM.close(resolveClose))
  await rm(workDir, { recursive: true, force: true })
  process.exit(code)
}

child.once('error', (error) => {
  console.error(error)
  void shutdown(1)
})
child.once('exit', (code, signal) => {
  if (!shuttingDown) {
    console.error(`AIScan E2E server exited early (code=${code}, signal=${signal})`)
    void shutdown(code ?? 1)
  }
})
process.once('SIGINT', () => void shutdown(130))
process.once('SIGTERM', () => void shutdown(143))
