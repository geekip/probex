import type { FileEntry } from './api';

export function isDirectory(entry: FileEntry) {
  return entry.type === 'directory' || entry.link_target_type === 'directory';
}

function sortValue(entry: FileEntry, field: string) {
  switch (field) {
    case 'mtime':
      return entry.mtime ? Date.parse(entry.mtime) : -Infinity;
    case 'size':
      return entry.size ?? 0;
    case 'type': {
      if (isDirectory(entry)) return 'directory';
      if (entry.type !== 'file') return entry.type;
      const dot = entry.name.lastIndexOf('.');
      return dot < 0 ? '' : entry.name.slice(dot).toLowerCase();
    }
    case 'mode':
      return entry.mode ?? '';
    case 'owner':
      return `${entry.owner ?? entry.uid ?? ''}/${entry.group ?? entry.gid ?? ''}`;
    default:
      return entry.name.toLowerCase();
  }
}

function compare(a: string | number, b: string | number) {
  return a < b ? -1 : a > b ? 1 : 0;
}

export function compareFileEntries(
  a: FileEntry,
  b: FileEntry,
  field: string,
  order: 'asc' | 'desc',
) {
  if (isDirectory(a) !== isDirectory(b)) return isDirectory(a) ? -1 : 1;
  const difference =
    compare(sortValue(a, field), sortValue(b, field)) ||
    compare(a.name.toLowerCase(), b.name.toLowerCase()) ||
    compare(a.name, b.name);
  return order === 'asc' ? difference : -difference;
}
