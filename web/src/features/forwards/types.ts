export type ForwardProtocol = 'tcp' | 'udp' | 'tcp+udp';
export type Forward = {
  id: string;
  name: string;
  node_id: string;
  node_name: string;
  protocol: ForwardProtocol;
  listen_addr: string;
  target_host: string;
  target_port: number;
  status:
    'ready' | 'stopped' | 'target_error' | 'node_offline' | 'listen_error';
  error: string;
};
export function targetAddress(
  mapping: Pick<Forward, 'target_host' | 'target_port'>,
) {
  return `${mapping.target_host.includes(':') ? `[${mapping.target_host}]` : mapping.target_host}:${mapping.target_port}`;
}
export function parseAddress(value: string) {
  const match = /^(?:\[([^\]\s]+)\]|([^:\s]+)):(\d+)$/.exec(value.trim());
  if (!match || Number(match[3]) < 1 || Number(match[3]) > 65535) return null;
  return { host: match[1] || match[2], port: Number(match[3]) };
}
