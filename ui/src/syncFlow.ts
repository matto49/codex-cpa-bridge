import type { PlatformSyncReport, RemoteSyncReport } from "./api";

export type SyncOutcome = {
  local?: PlatformSyncReport;
  remote?: RemoteSyncReport;
  localError?: string;
  remoteError?: string;
};

// Local and remote targets are independent. A failed local adapter must not
// suppress a configured remote sync (or hide the successful target's report).
export async function syncTargets(
  local: () => Promise<PlatformSyncReport>,
  remote?: () => Promise<RemoteSyncReport>,
  onUpdate?: (outcome: SyncOutcome) => void,
): Promise<SyncOutcome> {
  const outcome: SyncOutcome = {};
  const localTask = Promise.resolve().then(local).then(
    (value) => { outcome.local = value; onUpdate?.({ ...outcome }); },
    (error) => { outcome.localError = String(error); onUpdate?.({ ...outcome }); },
  );
  const remoteTask = remote ? Promise.resolve().then(remote).then(
    (value) => { outcome.remote = value; onUpdate?.({ ...outcome }); },
    (error) => { outcome.remoteError = String(error); onUpdate?.({ ...outcome }); },
  ) : Promise.resolve();
  await Promise.all([localTask, remoteTask]);
  return outcome;
}

export function syncSummary(outcome: SyncOutcome, catalogSaved: boolean): string {
  const parts = catalogSaved ? ["Catalog visibility saved."] : [];
  if (outcome.local) parts.push(`Local: ${outcome.local.changed} updated, ${outcome.local.failed} blocked or failed.`);
  else if (outcome.localError) parts.push("Local sync failed; review and retry it.");
  if (outcome.remote) parts.push(`Remote: ${outcome.remote.detail}`);
  else if (outcome.remoteError) parts.push("Remote sync failed; review and retry it.");
  return parts.join(" ");
}
