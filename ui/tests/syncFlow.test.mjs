import assert from 'node:assert/strict';
import test from 'node:test';
import { platformSyncSummary, syncSummary, syncTargets } from '../src/syncFlow.ts';

const localReport = { catalog_path: '/test/catalog.json', items: [], changed: 1, failed: 0 };
const remoteReport = {
  target: 'devbox', action: 'applied', model_policy: { changes: [], applied: true },
  platforms: localReport, remote_ready: true, detail: 'Remote model visibility synchronized',
};

test('remote sync continues and remains visible when local sync fails', async () => {
  const calls = [];
  const outcome = await syncTargets(
    async () => { calls.push('local'); throw new Error('local unavailable'); },
    async () => { calls.push('remote'); return remoteReport; },
  );
  assert.deepEqual(calls.sort(), ['local', 'remote']);
  assert.match(outcome.localError, /local unavailable/);
  assert.equal(outcome.remote, remoteReport);
  assert.match(syncSummary(outcome, true), /Catalog visibility saved/);
  assert.match(syncSummary(outcome, true), /Local sync failed/);
  assert.match(syncSummary(outcome, true), /Remote model visibility synchronized/);
});

test('local result remains visible when remote sync fails', async () => {
  const outcome = await syncTargets(
    async () => localReport,
    async () => { throw new Error('SSH unavailable'); },
  );
  assert.equal(outcome.local, localReport);
  assert.match(outcome.remoteError, /SSH unavailable/);
  assert.match(syncSummary(outcome, false), /Local: 1 updated/);
  assert.match(syncSummary(outcome, false), /Remote sync failed/);
});

test('no remote target produces no remote result or error', async () => {
  const outcome = await syncTargets(async () => localReport);
  assert.equal(outcome.local, localReport);
  assert.equal(outcome.remote, undefined);
  assert.equal(outcome.remoteError, undefined);
  assert.doesNotMatch(syncSummary(outcome, false), /Remote/);
});

test('local result is reported while a slower remote target is still pending', async () => {
  let finishRemote;
  const remotePending = new Promise((resolve) => { finishRemote = resolve; });
  const updates = [];
  const running = syncTargets(async () => localReport, () => remotePending, (partial) => updates.push(partial));
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(updates[0]?.local, localReport);
  assert.equal(updates[0]?.remote, undefined);
  finishRemote(remoteReport);
  const finished = await running;
  assert.equal(finished.remote, remoteReport);
  assert.equal(updates.at(-1)?.remote, remoteReport);
});

test('skipped platforms are reported separately from failures', () => {
  const report = {
    catalog_path: '/test/catalog.json', changed: 0, failed: 0,
    items: [
      { id: 'codex', action: 'noop', result: 'noop' },
      { id: 'claude', action: 'skipped', result: 'skipped' },
      { id: 'xbot', action: 'skipped', result: 'skipped' },
    ],
  };
  assert.equal(platformSyncSummary(report), '0 updated, 2 skipped, 0 blocked or failed');
  assert.match(syncSummary({ local: report }, false), /Local: 0 updated, 2 skipped, 0 blocked or failed/);
});

test('remote partial result includes its per-platform outcome in the notice', () => {
  const platforms = {
    catalog_path: '/test/remote-catalog.json', changed: 1, failed: 1,
    items: [
      { id: 'codex', action: 'noop', result: 'noop' },
      { id: 'claude', action: 'blocked', result: 'blocked' },
      { id: 'xbot', action: 'update', result: 'updated' },
    ],
  };
  const remote = { ...remoteReport, action: 'needs_attention', platforms, detail: 'Remote platform propagation needs attention' };
  assert.match(syncSummary({ remote }, true), /Remote \(devbox\): Remote platform propagation needs attention Platform results: 1 updated, 0 skipped, 1 blocked or failed/);
});
