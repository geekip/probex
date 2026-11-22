import type { Command, DeviceInfo, Node } from '../src/api';

const MiB = 1024 ** 2,
  GiB = 1024 ** 3;
const ago = (seconds: number) =>
  new Date(Date.now() - seconds * 1000).toISOString();
export const encode = (text: string) => Buffer.from(text).toString('base64');
export type MockCommand = Command & { node_id: string; finished_at?: string };
const profiles: DeviceInfo[] = [
  {
    hostname: 'OpenWrt',
    os: 'linux',
    platform: 'OpenWrt 24.10.2',
    kernel: '6.6.93',
    arch: 'arm64',
    cpus: 4,
    cpu_model: 'ARM Cortex-A53',
    uptime_seconds: 928412,
    memory_total: 512 * MiB,
    disk_total: 128 * MiB,
  },
  {
    hostname: 'Padavan',
    os: 'linux',
    platform: 'Padavan 3.4.3.9',
    kernel: '3.4.113',
    arch: 'mipsle',
    cpus: 2,
    cpu_model: 'MediaTek MT7621',
    uptime_seconds: 1984331,
    memory_total: 128 * MiB,
    disk_total: 16 * MiB,
  },
  {
    hostname: 'MacBook-Pro',
    os: 'darwin',
    platform: 'macOS 15.7',
    kernel: '24.6.0',
    arch: 'arm64',
    cpus: 10,
    cpu_model: 'Apple M2 Pro',
    uptime_seconds: 184321,
    memory_total: 32 * GiB,
    disk_total: 1024 * GiB,
  },
  {
    hostname: 'debian-worker',
    os: 'linux',
    platform: 'Debian GNU/Linux 13',
    kernel: '6.12.57-amd64',
    arch: 'amd64',
    cpus: 8,
    cpu_model: 'AMD Ryzen 7 5700G',
    uptime_seconds: 6284331,
    memory_total: 16 * GiB,
    disk_total: 512 * GiB,
  },
  {
    hostname: 'WIN-SERVER',
    os: 'windows',
    platform: 'Windows Server 2019',
    kernel: '10.0.17763',
    arch: 'amd64',
    cpus: 8,
    cpu_model: 'Intel Xeon E5-2680 v4',
    uptime_seconds: 345678,
    memory_total: 16 * GiB,
    disk_total: 256 * GiB,
  },
  {
    hostname: 'raspberrypi',
    os: 'linux',
    platform: 'Raspberry Pi OS 12',
    kernel: '6.12.47+rpt-rpi-2712',
    arch: 'arm64',
    cpus: 4,
    cpu_model: 'ARM Cortex-A76',
    uptime_seconds: 58312,
    memory_total: 8 * GiB,
    disk_total: 64 * GiB,
  },
];
const specs: [
  string,
  number,
  'online' | 'offline' | 'pending' | 'review' | 'rejected' | 'expired',
][] = [
  ['openwrt-main', 0, 'online'],
  ['padavan-bedroom', 1, 'online'],
  ['macbook-pro', 2, 'online'],
  ['ubuntu-build-01', 3, 'online'],
  ['windows-server-2019', 4, 'online'],
  ['raspberry-pi-5', 5, 'online'],
  ['openwrt-office-offline', 0, 'offline'],
  ['padavan-edge', 1, 'online'],
  ['mac-mini-offline', 2, 'offline'],
  ['storage-nas-01', 3, 'online'],
  ['windows-build-queued', 4, 'online'],
  ['raspberry-pi-lab', 5, 'online'],
  [
    'openwrt-shanghai-remote-network-gateway-with-a-very-long-name',
    0,
    'online',
  ],
  ['padavan-retired-offline', 1, 'offline'],
  ['设计工作站-上海', 2, 'online'],
  ['debian-docker-01', 3, 'online'],
  ['windows-desktop-busy', 4, 'online'],
  ['raspberry-pi-offline', 5, 'offline'],
  ['openwrt-low-memory', 0, 'online'],
  ['metrics-unavailable', 1, 'online'],
  ['linux-stale-report', 3, 'offline'],
  ['windows-backup-offline', 4, 'offline'],
  ['new-router-awaiting-enrollment', 0, 'pending'],
  ['new-windows-node', 4, 'pending'],
  ['new-router-awaiting-review', 0, 'review'],
  ['router-registration-rejected', 1, 'rejected'],
  ['join-key-expired', 0, 'expired'],
];

export function commandResult(command: string, node: Node) {
  let stdout = `Mock command on ${node.name}\n$ ${command}\nCompleted successfully.\n`;
  let stderr = '',
    exit_code = 0,
    status: Command['status'] = 'succeeded',
    error = '',
    truncated = false;
  if (command === 'pwd')
    stdout =
      node.info.os === 'windows' ? 'C:\\Users\\Administrator\r\n' : '/root\n';
  if (command.includes('uname'))
    stdout = `${node.info.os} ${node.info.hostname} ${node.info.kernel} ${node.info.arch}\n`;
  if (command === 'hostname') stdout = node.info.hostname + '\n';
  if (command === 'whoami')
    stdout = node.info.os === 'windows' ? 'Administrator\r\n' : 'root\n';
  if (command.startsWith('echo '))
    stdout = command.slice(5).replace(/^['"]|['"]$/g, '') + '\n';
  if (command === 'mock:help')
    stdout =
      'Mock scenarios: mock:success, mock:error, mock:stderr, mock:timeout, mock:unknown, mock:truncate, mock:empty, mock:unicode, mock:slow\n';
  if (
    command === 'mock:error' ||
    command === 'false' ||
    /exit (?:\/b )?7/.test(command)
  ) {
    stdout = 'Starting task…\n';
    stderr = 'Permission denied: /var/log/probex-demo.log\n';
    exit_code = 7;
    status = 'failed';
  }
  if (command === 'mock:stderr') {
    stdout = 'Task completed with a warning.\n';
    stderr = 'warning: optional configuration was not found\n';
  }
  if (command === 'mock:timeout') {
    stdout = 'Waiting for response…\n';
    status = 'timed_out';
    error = 'command timed out';
    exit_code = 137;
  }
  if (command === 'mock:unknown') {
    stdout = 'Task started.\n';
    status = 'unknown';
    error = 'connection lost before result';
  }
  if (command === 'mock:empty') stdout = '';
  if (command === 'mock:unicode')
    stdout =
      '部署完成 ✓\n节点：上海网关 / 東京 / München\n{"status":"ok","message":"你好，Probex 👋"}\n';
  if (command === 'mock:truncate') {
    stdout = Array.from(
      { length: 180 },
      (_, i) =>
        `[2026-11-20 10:32:${String(i % 60).padStart(2, '0')}] INFO worker-${i % 8}: completed job ${12000 + i}\n`,
    ).join('');
    truncated = true;
  }
  return {
    stdout: encode(stdout),
    stderr: encode(stderr),
    exit_code: status === 'unknown' ? undefined : exit_code,
    status,
    error,
    truncated,
  };
}

export function fixtures() {
  const nodes: Node[] = specs.map(([name, profile, state], i) => {
    const info = {
      ...profiles[profile],
      collected_at: ago(i % 25),
      hostname: name,
      cpu_percent: 8.2 + i * 2.7,
      memory_available: Math.round(profiles[profile].memory_total! * 0.62),
      disk_free: Math.round(profiles[profile].disk_total! * 0.48),
      bytes_sent: (i + 1) * 830 * MiB,
      bytes_recv: (i + 1) * 3.6 * GiB,
    };
    if (i === 1 || i === 18)
      Object.assign(info, {
        cpu_percent: 97.4,
        memory_available: 8 * MiB,
        disk_free: MiB,
      });
    if (i === 19)
      Object.assign(info, {
        cpu_percent: undefined,
        memory_total: undefined,
        memory_available: undefined,
        disk_total: undefined,
        disk_free: undefined,
        bytes_sent: undefined,
        bytes_recv: undefined,
        errors: {
          memory: 'permission denied: /proc/meminfo',
          disk: 'statfs /: input/output error',
          cpu: 'CPU counters unavailable',
        },
      });
    return {
      id: String(i + 1),
      name,
      online: state === 'online',
      join_mode: state === 'review' || state === 'rejected' ? 'request' : 'key',
      admission:
        state === 'pending'
          ? 'invited'
          : state === 'online' || state === 'offline'
            ? 'approved'
            : state,
      status: state,
      created_at: ago(86400 * 3 + i * 3600),
      joined_at:
        state === 'online' || state === 'offline'
          ? ago(86400 * 2 + i * 3600)
          : undefined,
      key_expires_at:
        state === 'pending'
          ? new Date(Date.now() + 86400_000).toISOString()
          : state === 'expired'
            ? ago(3600)
            : undefined,
      info:
        state === 'pending' || state === 'expired'
          ? {
              hostname: '',
              os: '',
              platform: '',
              kernel: '',
              arch: '',
              cpus: 0,
              cpu_model: '',
              uptime_seconds: 0,
            }
          : info,
      last_seen:
        state === 'pending' || state === 'expired'
          ? '0001-01-01T00:00:00Z'
          : ago(state === 'online' ? i % 25 : 3600 * (i + 1)),
    };
  });
  const commands: MockCommand[] = nodes
    .filter((n) => n.admission === 'approved')
    .flatMap((node) =>
      Array.from({ length: 28 }, (_, i) => {
        const command = [
          'uname -a',
          'mock:error',
          'mock:timeout',
          'mock:unknown',
          'mock:truncate',
          'mock:unicode',
          'mock:stderr',
          'mock:empty',
          'df -h',
          'free -m',
          'cat /etc/os-release',
          'echo Probex',
        ][i % 12];
        return {
          id: `${node.id.slice(-8)}-${i}`,
          node_id: node.id,
          command,
          timeout_seconds: 60,
          created_at: ago(i * 3600 + 120),
          finished_at: ago(i * 3600 + 118),
          ...commandResult(command, node),
        };
      }),
    );
  for (const [index, status] of [
    [10, 'queued'],
    [16, 'running'],
  ] as const) {
    const latest = commands.find((c) => c.node_id === nodes[index].id)!;
    Object.assign(latest, {
      command: 'mock:slow',
      status,
      stdout: encode('Running maintenance task…\n'),
      exit_code: undefined,
      finished_at: undefined,
    });
  }
  return { nodes, commands };
}
