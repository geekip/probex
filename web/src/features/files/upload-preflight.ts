import { FileError, fileJSON, remoteJoin, type FileEntry } from './api';
import type { PickedFile } from '@/features/transfers/store.svelte';
import { t } from '@/shared/settings.svelte';

export type UploadSelection = {
  target: string;
  files: PickedFile[];
  directories: string[];
};
export type UploadConflict = {
  relative: string;
  kind: 'file' | 'directory' | 'blocked';
};
export type UploadPlan = {
  target: string;
  owner: string;
  files: PickedFile[];
  directories: string[][];
  conflicts: UploadConflict[];
};

const batchSize = 6;

async function stat(
  nodeID: string,
  path: string,
  owner: string,
  signal: AbortSignal,
) {
  try {
    return await fileJSON<FileEntry>(
      `nodes/${nodeID}/stat?${new URLSearchParams({ path })}`,
      { signal },
      owner,
    );
  } catch (error) {
    if (error instanceof FileError && error.code === 'not_found') return null;
    throw error;
  }
}

export async function checkUpload(
  nodeID: string,
  selection: UploadSelection,
  windows: boolean,
  owner: string,
  signal: AbortSignal,
): Promise<UploadPlan> {
  const children = new Map<string, Map<string, PickedFile | null>>();
  function add(relative: string, file: PickedFile | null) {
    if (windows && /[\\:]/.test(relative))
      throw new Error(`${relative}: ${t('invalidFileName')}`);
    const slash = relative.lastIndexOf('/');
    const parent = slash < 0 ? '' : relative.slice(0, slash);
    const siblings =
      children.get(parent) ?? new Map<string, PickedFile | null>();
    if (siblings.has(relative)) {
      if (file || siblings.get(relative))
        throw new Error(`${relative}: ${t('fileNameExists')}`);
      return;
    }
    siblings.set(relative, file);
    children.set(parent, siblings);
    if (parent) add(parent, null);
  }
  for (const file of selection.files) add(file.relative, file);
  for (const directory of selection.directories) add(directory, null);

  const plan: UploadPlan = {
    target: selection.target,
    owner,
    files: [],
    directories: [],
    conflicts: [],
  };
  const candidates = (parent: string, check: boolean) =>
    Array.from(children.get(parent) ?? [], ([relative, file]) => ({
      relative,
      file,
      check,
    }));
  let level = candidates('', true);
  while (level.length) {
    const next: typeof level = [];
    const missing: string[] = [];
    for (let offset = 0; offset < level.length; offset += batchSize) {
      const batch = level.slice(offset, offset + batchSize);
      const existing = await Promise.all(
        batch.map((item) =>
          item.check
            ? stat(
                nodeID,
                remoteJoin(selection.target, item.relative, windows),
                owner,
                signal,
              )
            : null,
        ),
      );
      for (const [index, { relative, file }] of batch.entries()) {
        const entry = existing[index];
        if (entry && entry.type !== (file ? 'file' : 'directory')) {
          plan.conflicts.push({ relative, kind: 'blocked' });
        } else if (file) {
          if (entry) plan.conflicts.push({ relative, kind: 'file' });
          plan.files.push({ ...file, overwrite: !!entry });
        } else {
          if (!entry) missing.push(relative);
          else if (!relative.includes('/') || !children.has(relative))
            plan.conflicts.push({ relative, kind: 'directory' });
          next.push(...candidates(relative, !!entry));
        }
      }
    }
    if (missing.length) plan.directories.push(missing);
    level = next;
  }
  return plan;
}

export async function createUploadDirectories(
  nodeID: string,
  plan: UploadPlan,
  windows: boolean,
  signal: AbortSignal,
) {
  for (const level of plan.directories) {
    for (let offset = 0; offset < level.length; offset += batchSize) {
      const results = await Promise.allSettled(
        level.slice(offset, offset + batchSize).map(async (relative) => {
          const path = remoteJoin(plan.target, relative, windows);
          try {
            await fileJSON(
              `nodes/${nodeID}/directories`,
              { method: 'POST', body: JSON.stringify({ path }), signal },
              plan.owner,
            );
          } catch (error) {
            if (
              !(error instanceof FileError) ||
              error.code !== 'exists' ||
              (await stat(nodeID, path, plan.owner, signal))?.type !==
                'directory'
            )
              throw error;
          }
        }),
      );
      const failed = results.find((result) => result.status === 'rejected');
      if (failed) throw failed.reason;
    }
  }
}
