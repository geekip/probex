import { t, type Message } from './settings.svelte';

export interface DeviceInfo {
  collected_at?: string;
  hostname: string;
  os: string;
  platform: string;
  kernel: string;
  arch: string;
  cpus: number;
  cpu_model: string;
  cpu_percent?: number;
  uptime_seconds: number;
  memory_total?: number;
  memory_available?: number;
  disk_total?: number;
  disk_free?: number;
  bytes_sent?: number;
  bytes_recv?: number;
  errors?: Record<string, string>;
}
export interface Node {
  id: string;
  name: string;
  online: boolean;
  join_mode: 'key' | 'request';
  admission: 'invited' | 'review' | 'approved' | 'rejected' | 'expired';
  status: 'online' | 'offline' | 'pending' | 'review' | 'rejected' | 'expired';
  created_at: string;
  joined_at?: string;
  key_expires_at?: string;
  info: DeviceInfo;
  last_seen: string;
}
export interface Enrollment {
  node: Node;
  key: string;
  expires_at: string;
}
export type CommandStatus =
  'queued' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'unknown';
export interface Command {
  id: string;
  command: string;
  status: CommandStatus;
  created_at: string;
  timeout_seconds: number;
  stdout?: string;
  stderr?: string;
  exit_code?: number;
  error?: string;
  truncated?: boolean;
}
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
export let unauthorized: () => void = () => {};
export function onUnauthorized(handler: () => void) {
  unauthorized = handler;
}
export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const response = await fetch(`/api/${path}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...options.headers },
  });
  const value = await response.json();
  if (response.status === 401) unauthorized();
  if (!response.ok) {
    const messages: Record<string, Message> = {
      'invalid token': 'loginError',
      'node name already exists': 'nameTaken',
      'invalid node name': 'invalidName',
      'node admission state does not allow this operation': 'admissionError',
      'node offline or busy': 'busy',
    };
    throw new ApiError(
      response.status,
      messages[value.error]
        ? t(messages[value.error])
        : value.error || `HTTP ${response.status}`,
    );
  }
  return value;
}
export function output(value?: string) {
  return value
    ? new TextDecoder().decode(
        Uint8Array.from(atob(value), (c) => c.charCodeAt(0)),
      )
    : '';
}
