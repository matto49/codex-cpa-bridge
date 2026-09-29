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
const model = process.argv[4] || 'claude-sonnet-4-6';
const checkHidden = process.env.BRIDGE_CLAUDE_TEST_HIDDEN === '1';
const checkPicker = process.env.BRIDGE_CLAUDE_TEST_PICKER === '1';
const fallbackModel = model === 'claude-sonnet-4-6' ? 'claude-opus-4-6-thinking' : 'claude-sonnet-4-6';
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
  const origin = `http://127.0.0.1:${server.address().port}`;
  const baseURL = origin + (process.env.BRIDGE_CLAUDE_TEST_V1 === '1' ? '/v1' : '');
  const manifestPath = join(temporary, 'bridge.toml');
  const catalogPath = join(temporary, 'catalog.json');
  const settingsPath = join(temporary, 'claude', 'settings.json');
  await writeFile(join(temporary, '.claude.json'), JSON.stringify({ hasCompletedOnboarding: true, lastOnboardingVersion: '2.0.24' }), { mode: 0o600 });
  const catalogModels = [{ slug: model, display_name: 'Bridge test target', visibility: 'list' }];
  if (checkHidden) catalogModels.push({ slug: fallbackModel, visibility: 'list' });
  catalogModels.push({ slug: 'hidden-model', visibility: 'hide' });
  await writeFile(catalogPath, JSON.stringify({ models: catalogModels }), { mode: 0o600 });
  await writeFile(manifestPath, [
    '[profiles.cpa]',
    `endpoint = ${JSON.stringify(`${origin}/v1`)}`,
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
  const expectedModels = checkHidden ? [model, fallbackModel] : [model];
  if (initReport.action !== 'created' || initReport.base_url !== origin || settingsMode !== 0o600 || settings.env?.ANTHROPIC_BASE_URL !== origin || !settings.enforceAvailableModels || !Array.isArray(settings.availableModels) || settings.availableModels.length !== expectedModels.length || expectedModels.some((id) => !settings.availableModels.includes(id)) || settings.modelPicker?.replaceBuiltInOptions !== true || expectedModels.some((id) => !settings.modelPicker.options?.some((option) => option.model === id)) || settings.modelPicker.options.length !== expectedModels.length) {
    throw new Error('bridge did not create the expected private Claude settings');
  }
  const isolatedEnvironment = () => {
    return {
      PATH: process.env.PATH || '/usr/bin:/bin',
      LANG: process.env.LANG || 'en_US.UTF-8',
      TERM: process.env.TERM || 'xterm-256color',
      HOME: temporary, XDG_CONFIG_HOME: temporary, CLAUDE_CONFIG_DIR: temporary,
      ANTHROPIC_AUTH_TOKEN: fakeKey,
      DISABLE_TELEMETRY: '1',
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
    };
  };
  const runPicker = () => new Promise((resolve, reject) => {
    const helper = join(dirname(fileURLToPath(import.meta.url)), 'claude-picker-pty.py');
    const child = spawn('python3', [helper, claudeBinary,
      '--bare', '--restricted', '--strict-mcp-config',
      '--tools', '', '--settings', settingsPath,
    ], { cwd: temporary, env: isolatedEnvironment(), stdio: ['ignore', 'pipe', 'pipe'] });
    let output = '';
    let errorOutput = '';
    const kill = setTimeout(() => child.kill('SIGTERM'), 18000);
    child.stdout.on('data', (chunk) => { output = (output + chunk.toString()).slice(-1024 * 1024); });
    child.stderr.on('data', (chunk) => { errorOutput = (errorOutput + chunk.toString()).slice(-4096); });
    child.on('error', (error) => { clearTimeout(kill); reject(error); });
    child.on('exit', (code, signal) => {
      clearTimeout(kill);
      if (code !== 0) { reject(new Error(`Claude picker PTY failed code=${code} signal=${signal}: ${errorOutput}`)); return; }
      resolve(output.replace(/\x1b\][^\x07]*(?:\x07|\x1b\\)/g, '').replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, ''));
    });
  });
  const runClaude = () => new Promise((resolve, reject) => {
    const started = Date.now();
    const child = spawn(claudeBinary, [
      '--bare', '--restricted', '--no-session-persistence', '--strict-mcp-config',
      '--tools', '', '--model', model, '--settings', settingsPath,
      '--max-budget-usd', '0.01', '-p', '--output-format', 'json', 'Reply with exactly OK.',
    ], {
      cwd: temporary,
      env: isolatedEnvironment(),
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
      resolve({ code, signal, elapsedMs: Date.now() - started, stdout, stderr });
    });
  });
  const pickerBefore = checkPicker ? await runPicker() : '';
  if (checkPicker && process.env.BRIDGE_CLAUDE_TEST_PICKER_DEBUG === '1') process.stderr.write(pickerBefore.slice(-5000));
  const firstRun = await runClaude();
  if (firstRun.code !== 0) {
    throw new Error(`Claude CLI exited code=${firstRun.code} signal=${firstRun.signal} elapsed_ms=${firstRun.elapsedMs} messages=${messagesSeen} paths=${JSON.stringify(pathsSeen)} stdout_bytes=${firstRun.stdout.length}; stderr: ${firstRun.stderr.slice(-600)}`);
  }
  const result = JSON.parse(firstRun.stdout);
  if (result.is_error || result.result?.trim() !== 'OK.' || messagesSeen < 1 || !authenticated || !modelMatched) {
    throw new Error(`Claude proxy verification failed: result=${result.result?.slice(0, 80)}, messages=${messagesSeen}, authenticated=${authenticated}, modelMatched=${modelMatched}`);
  }
  const report = { model, bridgeSettingsCreated: true, cliResponded: true, localMessages: messagesSeen, authenticatedWithTestKey: true, modelMatched: true, userSettingsUntouched: true };
  if (checkPicker) {
    const compactPicker = pickerBefore.replace(/\s+/g, '');
    report.visibleModelInPicker = compactPicker.includes('Selectmodel') && compactPicker.includes('Bridgetesttarget');
    if (!report.visibleModelInPicker) throw new Error('visible non-Claude CPA model was not present in the interactive /model picker');
  }
  if (checkHidden) {
    execFileSync(bridgeBinary, ['--manifest', manifestPath, 'models', 'set', '--slug', model, '--visibility', 'hide'], { encoding: 'utf8', env: { ...process.env, HOME: temporary } });
    let syncOutput;
    try {
      syncOutput = execFileSync(bridgeBinary, ['--manifest', manifestPath, 'platforms', 'sync', '--json'], { encoding: 'utf8', env: { ...process.env, HOME: temporary } });
    } catch (error) {
      syncOutput = error.stdout?.toString(); // Other isolated platforms may be blocked.
      if (!syncOutput) throw error;
    }
    const syncReport = JSON.parse(syncOutput);
    const claudeResult = syncReport.items.find((item) => item.id === 'claude');
    const hiddenSettings = JSON.parse(await readFile(settingsPath, 'utf8'));
    if (claudeResult?.result !== 'updated' || hiddenSettings.availableModels.includes(model) || !hiddenSettings.availableModels.includes(fallbackModel) || hiddenSettings.modelPicker.options.some((option) => option.model === model) || !hiddenSettings.modelPicker.options.some((option) => option.model === fallbackModel)) {
      throw new Error('bridge did not remove the hidden model from private Claude settings');
    }
    const beforeHiddenRun = messagesSeen;
    const hiddenRun = await runClaude();
    report.hiddenModelRemovedFromSettings = true;
    report.explicitHiddenModelReachedMock = messagesSeen > beforeHiddenRun;
    report.explicitHiddenModelExitCode = hiddenRun.code;
    if (checkPicker) {
      const pickerAfter = await runPicker();
      if (process.env.BRIDGE_CLAUDE_TEST_PICKER_DEBUG === '1') process.stderr.write(pickerAfter.slice(-5000));
      const compactPicker = pickerAfter.replace(/\s+/g, '');
      report.hiddenModelAbsentFromPicker = compactPicker.includes('Selectmodel') && !compactPicker.includes('Bridgetesttarget');
      if (!report.hiddenModelAbsentFromPicker) throw new Error('hidden CPA model remained in the interactive /model picker');
    }
  }
  console.log(JSON.stringify(report));
} finally {
  server.close();
  await rm(temporary, { recursive: true, force: true });
}
