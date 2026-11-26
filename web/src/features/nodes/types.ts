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
  join_mode: 'key' | 'request' | 'local';
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
