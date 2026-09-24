#!/usr/bin/env node

// Run a real Claude Code CLI against an isolated local Anthropic mock. No live
// CPA credential or upstream inference is used; the user's settings are untouched.
import { execFileSync, spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const claudeBinary = process.argv[2] || 'claude';
const bridgeBinary = process.argv[3] || join(dirname(fileURLToPath(import.meta.url)), '..', 'bin', 'bridge-go');
const model = 'claude-sonnet-4-6';
const fakeKey = 'bridge-isolated-test-key';
const temporary = await mkdtemp(join(tmpdir(), 'bridge-claude-proxy-'));
let messagesSeen = 0;
let authenticated = false;
let modelMatched = false;
const pathsSeen = [];
const server = createServer(async (request, response) => {
  const path = new URL(request.url, 'http://127.0.0.1').pathname;
  if (pathsSeen.length < 12) pathsSeen.push(`${request.method} ${path}`);
  if (path === '/v1/messages/count_tokens') {
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ input_tokens: 1 }));
    return;
  }
  if (path !== '/v1/messages' || request.method !== 'POST') {
    response.writeHead(404, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ type: 'error', error: { type: 'not_found_error', message: 'unknown test route' } }));
    return;
  }
  messagesSeen++;
  authenticated ||= request.headers['x-api-key'] === fakeKey || request.headers.authorization === `Bearer ${fakeKey}`;
  let body = '';
  for await (const chunk of request) body += chunk;
  const payload = JSON.parse(body);
  modelMatched ||= payload.model === model;
  const message = {
    id: 'msg_bridge_test', type: 'message', role: 'assistant', model,
    content: [{ type: 'text', text: 'OK.' }], stop_reason: 'end_turn', stop_sequence: null,
    usage: { input_tokens: 1, output_tokens: 1 },
  };
  if (!payload.stream) {
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(JSON.stringify(message));
    return;
  }
  response.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
  const event = (name, data) => response.write(`event: ${name}\ndata: ${JSON.stringify(data)}\n\n`);
  event('message_start', { type: 'message_start', message: { ...message, content: [], stop_reason: null, usage: { input_tokens: 1, output_tokens: 0 } } });
  event('content_block_start', { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } });
  event('content_block_delta', { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'OK.' } });
  event('content_block_stop', { type: 'content_block_stop', index: 0 });
  event('message_delta', { type: 'message_delta', delta: { stop_reason: 'end_turn', stop_sequence: null }, usage: { output_tokens: 1 } });
  event('message_stop', { type: 'message_stop' });
  response.end();
});

try {
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const baseURL = `http://127.0.0.1:${server.address().port}`;
  const manifestPath = join(temporary, 'bridge.toml');
  const catalogPath = join(temporary, 'catalog.json');
  const settingsPath = join(temporary, 'claude', 'settings.json');
  await writeFile(catalogPath, JSON.stringify({ models: [{ slug: model, visibility: 'list' }, { slug: 'hidden-model', visibility: 'hide' }] }), { mode: 0o600 });
  await writeFile(manifestPath, [
    '[profiles.cpa]',
    `endpoint = ${JSON.stringify(`${baseURL}/v1`)}`,
    `model_catalog_json = ${JSON.stringify(catalogPath)}`,
    '[platforms]',
    `claude_settings_json = ${JSON.stringify(settingsPath)}`,
    '',
  ].join('\n'), { mode: 0o600 });
  const initReport = JSON.parse(execFileSync(bridgeBinary, [
    '--manifest', manifestPath, 'platforms', 'init-claude', '--base-url', baseURL,
    '--confirm-anthropic-compatible', '--confirm-external-auth', '--write', '--json',
  ], { encoding: 'utf8', env: { ...process.env, HOME: temporary } }));
  const settings = JSON.parse(await readFile(settingsPath, 'utf8'));
  const settingsMode = (await stat(settingsPath)).mode & 0o777;
  if (initReport.action !== 'created' || settingsMode !== 0o600 || settings.env?.ANTHROPIC_BASE_URL !== baseURL || !settings.enforceAvailableModels || JSON.stringify(settings.availableModels) !== JSON.stringify([model])) {
    throw new Error('bridge did not create the expected private Claude settings');
  }
  const output = await new Promise((resolve, reject) => {
    const started = Date.now();
    const environment = {
      ...process.env, HOME: temporary, XDG_CONFIG_HOME: temporary,
      CLAUDE_CONFIG_DIR: temporary, DISABLE_TELEMETRY: '1',
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
    };
    for (const key of Object.keys(environment)) {
      if (/^(ANTHROPIC|CLAUDE_CODE).*(_KEY|_TOKEN|_SECRET)$/i.test(key) || /^CLAUDE_CODE_USE_(BEDROCK|VERTEX|FOUNDRY)$/.test(key)) {
        delete environment[key];
      }
    }
    delete environment.ANTHROPIC_BASE_URL; // The CLI must read the bridge-created --settings file.
    environment.ANTHROPIC_API_KEY = fakeKey;
    const child = spawn(claudeBinary, [
      '--bare', '--restricted', '--no-session-persistence', '--strict-mcp-config',
      '--tools', '', '--model', model, '--settings', settingsPath,
      '--max-budget-usd', '0.01', '-p', '--output-format', 'json', 'Reply with exactly OK.',
    ], {
      cwd: temporary,
      env: environment,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    const timer = setTimeout(() => child.kill('SIGTERM'), 45000);
    child.stdout.on('data', (chunk) => { stdout = (stdout + chunk.toString()).slice(-1024 * 1024); });
    child.stderr.on('data', (chunk) => { stderr = (stderr + chunk.toString()).slice(-4096); });
    child.on('error', (error) => { clearTimeout(timer); reject(error); });
    child.on('exit', (code, signal) => {
      clearTimeout(timer);
      if (code !== 0) reject(new Error(`Claude CLI exited code=${code} signal=${signal} elapsed_ms=${Date.now() - started} messages=${messagesSeen} paths=${JSON.stringify(pathsSeen)} stdout_bytes=${stdout.length}; stderr: ${stderr.slice(-600)}`));
      else resolve(stdout);
    });
  });
  const result = JSON.parse(output);
  if (result.is_error || result.result?.trim() !== 'OK.' || messagesSeen < 1 || !authenticated || !modelMatched) {
    throw new Error(`Claude proxy verification failed: result=${result.result?.slice(0, 80)}, messages=${messagesSeen}, authenticated=${authenticated}, modelMatched=${modelMatched}`);
  }
  console.log(JSON.stringify({ bridgeSettingsCreated: true, cliResponded: true, localMessages: messagesSeen, authenticatedWithTestKey: true, modelMatched: true, userSettingsUntouched: true }));
} finally {
  server.close();
  await rm(temporary, { recursive: true, force: true });
}
