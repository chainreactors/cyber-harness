// End-to-end smoke test for the agent-core wasm module (RFC #189, 方案 A).
//
// Instantiates agent.wasm under Node with stubbed host seams that mirror the
// CyberHub Copilot host contract (src/offscreen/wasm-agent.ts): runAgent +
// __aiscanLLM + __aiscanTool(name,args,ctx) returning {content,isError,
// terminate}. Drives one full tool-using turn plus a cancel, to prove the three
// JS<->WASM seams work — not just that the module compiles.
//
//   node smoke.mjs <path/to/agent.wasm> <path/to/wasm_exec.js>
//   (or set AISCAN_WASM / WASM_EXEC)
//
// Exit code 0 = all assertions pass.
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import process from 'node:process';

const wasmPath = process.env.AISCAN_WASM || process.argv[2];
const wasmExecPath = process.env.WASM_EXEC || process.argv[3];
if (!wasmPath || !wasmExecPath) {
  console.error('usage: node smoke.mjs <agent.wasm> <wasm_exec.js> (or AISCAN_WASM / WASM_EXEC)');
  process.exit(2);
}

let passes = 0;
let failures = 0;
function check(cond, msg) {
  if (cond) {
    passes++;
    console.log(`  ok   ${msg}`);
  } else {
    failures++;
    console.error(`  FAIL ${msg}`);
  }
}
const timeout = (ms, why) =>
  new Promise((_, rej) => setTimeout(() => rej(new Error(`timeout: ${why}`)), ms));

// Go's wasm_exec.js is a plain script that installs globalThis.Go.
vm.runInThisContext(readFileSync(wasmExecPath, 'utf8'), { filename: wasmExecPath });

// ---- host seams (stubs mirroring wasm-agent.ts) ----------------------------
const llmRequests = [];
const llmContexts = [];
const toolCalls = [];
let llmCall = 0;

globalThis.__aiscanLLM = async (reqJSON, ctxJSON) => {
  const req = JSON.parse(reqJSON);
  llmRequests.push(req);
  llmContexts.push(JSON.parse(ctxJSON || '{}'));
  llmCall++;
  if (llmCall === 1) {
    // Turn 1: instruct the brain to call the echo tool.
    return JSON.stringify({
      id: 'wasm',
      choices: [{
        message: {
          role: 'assistant',
          content: '',
          tool_calls: [{
            id: 'call_1',
            type: 'function',
            function: { name: 'echo', arguments: JSON.stringify({ text: 'hello' }) },
          }],
        },
        finish_reason: 'tool_calls',
      }],
    });
  }
  // Turn 2: no tool calls -> the loop completes.
  const echoed = toolCalls[0]?.result ?? '';
  return JSON.stringify({
    id: 'wasm',
    choices: [{ message: { role: 'assistant', content: `done: ${echoed}` }, finish_reason: 'stop' }],
  });
};

// The extension's __aiscanTool takes (name, argsJSON, ctxJSON) and returns a
// JSON string {content, isError, terminate}.
globalThis.__aiscanTool = async (name, argsJSON, ctxJSON) => {
  const result = `echoed:${JSON.parse(argsJSON).text}`;
  toolCalls.push({ name, args: argsJSON, ctx: ctxJSON, result });
  return JSON.stringify({ content: result, isError: false, terminate: false });
};

// ---- instantiate -----------------------------------------------------------
const go = new globalThis.Go();
const { instance } = await WebAssembly.instantiate(readFileSync(wasmPath), go.importObject);
go.run(instance); // do NOT await: main() blocks on select{} to stay resident

for (let i = 0; i < 400 && !globalThis.aiscanAgentReady; i++) {
  await new Promise((r) => setTimeout(r, 5));
}
check(globalThis.aiscanAgentReady === true, 'aiscanAgentReady is true after load');
check(typeof globalThis.runAgent === 'function', 'runAgent exported (host-facing name)');
check(typeof globalThis.aiscanCancelAgent === 'function', 'aiscanCancelAgent exported');

// ---- phase 1: happy path (LLM -> tool -> LLM -> done) ----------------------
const events = [];
const payload = {
  task: 'please echo hello',
  systemPrompt: 'You are a test agent.',
  model: 'test-model',
  maxTurns: 5,
  messages: [],
  tools: [{
    name: 'echo',
    description: 'Echo the given text',
    parameters: { type: 'object', properties: { text: { type: 'string' } }, required: ['text'] },
  }],
  context: { tabId: 42, url: 'https://example.com', runId: 'r1' },
};

const resultJSON = await Promise.race([
  globalThis.runAgent(JSON.stringify(payload), (evJSON) => events.push(JSON.parse(evJSON))),
  timeout(10000, 'happy-path run did not settle'),
]);
const result = JSON.parse(resultJSON);

check(llmCall === 2, `LLM called twice (got ${llmCall})`);
check(toolCalls.length === 1 && toolCalls[0].name === 'echo', 'echo tool dispatched once via __aiscanTool');
check(JSON.parse(toolCalls[0].args).text === 'hello', 'tool received the arguments from the LLM');
check(JSON.parse(toolCalls[0].ctx || '{}').runId === 'r1', 'tool received the run context (tabId/url/runId)');
check(
  (llmRequests[0].tools || []).some((t) => t.function?.name === 'echo'),
  'tool schema was advertised to the LLM',
);
check(llmContexts[0]?.runId === 'r1', 'LLM received the run context (runId)');
check(llmRequests[0].messages?.[0]?.role === 'system', 'system prompt reached the provider');
check(result.stop === 'completed', `run stop=completed (got ${result.stop})`);
check(result.turns === 2, `run took 2 turns (got ${result.turns})`);
check(result.output === 'done: echoed:hello', `final output threaded tool result (got ${JSON.stringify(result.output)})`);
check(Array.isArray(result.messages) && result.messages.length >= 4, `transcript has >=4 messages (got ${result.messages?.length})`);

const evTypes = new Set(events.map((e) => e.type));
for (const t of ['agent_start', 'llm_request', 'tool_execution_start', 'tool_execution_end', 'agent_end']) {
  check(evTypes.has(t), `event stream emitted ${t}`);
}

// ---- phase 2: multi-turn continuation (transcript hydration) ---------------
llmCall = 0;
toolCalls.length = 0;
globalThis.__aiscanLLM = async (reqJSON, ctxJSON) => {
  llmRequests.push(JSON.parse(reqJSON));
  llmContexts.push(JSON.parse(ctxJSON || '{}'));
  return JSON.stringify({
    id: 'wasm',
    choices: [{ message: { role: 'assistant', content: 'second turn ok' }, finish_reason: 'stop' }],
  });
};
const cont = JSON.parse(await Promise.race([
  globalThis.runAgent(JSON.stringify({
    ...payload,
    task: 'and now continue',
    messages: result.messages, // hydrate prior transcript
    context: { tabId: 42, url: 'https://example.com', runId: 'r2' },
  }), () => {}),
  timeout(10000, 'continuation run did not settle'),
]));
const lastReq = llmRequests[llmRequests.length - 1];
const userMsgs = (lastReq.messages || []).filter((m) => m.role === 'user').length;
check(cont.stop === 'completed' && cont.output === 'second turn ok', 'continuation completed');
check(userMsgs >= 2, `prior transcript hydrated into the 2nd run (user msgs=${userMsgs})`);

// ---- phase 3: cancellation over the JS/WASM boundary -----------------------
globalThis.__aiscanLLM = () => new Promise(() => {}); // never resolves
const hangPromise = globalThis.runAgent(
  JSON.stringify({ task: 'hang', model: 'm', maxTurns: 3, messages: [], tools: [], context: { runId: 'cancel-1' } }),
  () => {},
);
await new Promise((r) => setTimeout(r, 60));
const cancelled = globalThis.aiscanCancelAgent('cancel-1');
check(cancelled === true, 'aiscanCancelAgent returns true for a live run');
const cancelResult = JSON.parse(await Promise.race([hangPromise, timeout(5000, 'cancel run did not settle')]));
check(cancelResult.stop === 'error' || cancelResult.error, `cancelled run ended (stop=${cancelResult.stop})`);
check(globalThis.aiscanCancelAgent('no-such-run') === false, 'cancel of unknown run returns false');

// ---- summary ---------------------------------------------------------------
console.log(`\n${failures === 0 ? 'PASS' : 'FAIL'}: ${passes} passed, ${failures} failed`);
process.exit(failures === 0 ? 0 : 1);
