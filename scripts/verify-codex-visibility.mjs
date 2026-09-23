#!/usr/bin/env node

// Exercise Codex's real model/list picker contract using isolated catalog copies.
// This script never changes the source catalog or the user's CODEX_HOME.
import { spawn } from 'node:child_process';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';

const [, , sourcePath, slug, codexBinary = 'codex'] = process.argv;
if (!sourcePath || !slug) {
  console.error('usage: node scripts/verify-codex-visibility.mjs CATALOG_JSON MODEL_SLUG [CODEX_BINARY]');
  process.exit(2);
}

async function listModels(catalogPath, home, includeHidden) {
  const child = spawn(codexBinary, ['app-server', '-c', `model_catalog_json=${JSON.stringify(catalogPath)}`], {
    env: { ...process.env, CODEX_HOME: home }, stdio: ['pipe', 'pipe', 'pipe'],
  });
  let stderr = '';
  child.stderr.on('data', (chunk) => { stderr = (stderr + chunk.toString()).slice(-2048); });
  const lines = createInterface({ input: child.stdout });
  let settled = false;
  const result = await new Promise((resolve, reject) => {
    const finish = (error, value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      child.kill('SIGTERM');
      if (error) reject(error);
      else resolve(value);
    };
    const timer = setTimeout(() => finish(new Error('Codex model/list timed out')), 15000);
    child.on('error', (error) => finish(error));
    child.stdin.on('error', (error) => finish(error));
    child.on('exit', (code) => finish(new Error(`Codex app-server exited ${code}: ${stderr}`)));
    lines.on('line', (line) => {
      let message;
      try { message = JSON.parse(line); } catch { return; }
      if (message.id === 1) {
        if (message.error) return finish(new Error(`Codex initialize failed: ${JSON.stringify(message.error)}`));
        child.stdin.write(`${JSON.stringify({ method: 'initialized', params: {} })}\n`);
        child.stdin.write(`${JSON.stringify({ id: 2, method: 'model/list', params: { limit: 1000, includeHidden } })}\n`);
      }
      if (message.id === 2) {
        if (message.error) return finish(new Error(`Codex model/list failed: ${JSON.stringify(message.error)}`));
        finish(null, message.result?.data);
      }
    });
    child.stdin.write(`${JSON.stringify({ id: 1, method: 'initialize', params: { clientInfo: { name: 'bridge-visibility-check', version: '0.1.0' }, capabilities: {} } })}\n`);
  });
  if (!Array.isArray(result)) throw new Error('Codex model/list did not return a model array');
  return result;
}

const temporary = await mkdtemp(join(tmpdir(), 'bridge-codex-visibility-'));
try {
  const catalog = JSON.parse(await readFile(sourcePath, 'utf8'));
  const selected = catalog.models?.find((model) => model.slug === slug);
  if (!selected) throw new Error(`model not found in source catalog: ${slug}`);
  const catalogPath = join(temporary, 'catalog.json');
  const home = join(temporary, 'codex-home');
  await mkdir(home, { mode: 0o700 });
  selected.visibility = 'list';
  await writeFile(catalogPath, JSON.stringify(catalog), { mode: 0o600 });
  const visible = await listModels(catalogPath, home, false);
  if (!visible.some((model) => model.id === slug)) throw new Error('enabled model did not appear in model/list');

  selected.visibility = 'hide';
  await writeFile(catalogPath, JSON.stringify(catalog), { mode: 0o600 });
  const hidden = await listModels(catalogPath, home, false);
  if (hidden.some((model) => model.id === slug)) throw new Error('disabled model still appears in picker-visible model/list');
  const full = await listModels(catalogPath, home, true);
  if (!full.some((model) => model.id === slug && model.hidden === true)) throw new Error('disabled model was not reported as hidden by includeHidden');

  console.log(JSON.stringify({ model: slug, enabledVisible: true, disabledVisible: false, includeHiddenReportsHidden: true }));
} finally {
  await rm(temporary, { recursive: true, force: true });
}
