import { ApiError, unauthorized } from '@/shared/api';
export type FileEntry = {
  name: string;
  path: string;
  type: 'file' | 'directory' | 'symlink' | 'special';
  link_kind?: 'symlink' | 'shortcut' | 'hard';
  link_target_type?: 'file' | 'directory';
  link_error?: string;
  size?: number;
  mtime?: string;
  mode?: string;
  uid?: number;
  gid?: number;
  owner?: string;
  group?: string;
};
export function directoryTarget(entry?: FileEntry | null) {
  return (
    !!entry &&
    (entry.type === 'directory' ||
      entry.link_target_type === 'directory' ||
      !!entry.link_error)
  );
}
export type FileLink = {
  path: string;
  name: string;
  target: string;
  kind: '' | 'symlink' | 'shortcut' | 'hard';
  hard: boolean;
};
export type FileListing = {
  path: string;
  entries: FileEntry[];
  next_cursor: string;
  file_count: number;
  directory_count: number;
};
export class FileError extends ApiError {
  declare code: string;

  constructor(status: number, message: string, code: string) {
    super(status, message, code);
  }
}
export async function fileRequest(
  path: string,
  options: RequestInit = {},
  owner = '',
): Promise<Response> {
  const headers = new Headers(options.headers);
  if (owner) headers.set('X-Probex-File-Session', owner);
  const response = await fetch(`/api/${path}`, { ...options, headers });
  if (response.status === 401) unauthorized();
  if (!response.ok) {
    const error = await response.json();
    throw new FileError(
      response.status,
      error.error || `HTTP ${response.status}`,
      error.code || 'request_failed',
    );
  }
  return response;
}
export async function fileJSON<T>(
  path: string,
  options: RequestInit = {},
  owner = '',
): Promise<T> {
  const headers = new Headers(options.headers);
  if (typeof options.body === 'string')
    headers.set('Content-Type', 'application/json');
  return (await fileRequest(path, { ...options, headers }, owner)).json();
}
export function remoteJoin(
  directory: string,
  relative: string,
  windows: boolean,
) {
  return windows
    ? directory.replace(/[\\/]+$/, '') + '\\' + relative.replaceAll('/', '\\')
    : directory.replace(/\/+$/, '') + '/' + relative;
}
export function parentPath(path: string, windows: boolean) {
  const value = path.replace(windows ? /[\\/]+$/ : /\/+$/, '');
  if (windows && /^[A-Za-z]:$/.test(value)) return '';
  const index = windows
    ? Math.max(value.lastIndexOf('\\'), value.lastIndexOf('/'))
    : value.lastIndexOf('/');
  if (index < 0) return '';
  if (windows && index === 2 && value[1] === ':') return value.slice(0, 3);
  return value.slice(0, index) || '/';
}
