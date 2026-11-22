import { randomBytes } from 'node:crypto';
import type { IncomingMessage, ServerResponse } from 'node:http';
import type { Duplex } from 'node:stream';
import type { Plugin } from 'vite';
import { WebSocketServer, type WebSocket } from 'ws';
import { fixtures, commandResult, encode, type MockCommand } from './data';
import { terminal } from './terminal';

export function mockPlugin(): Plugin {
  let data = fixtures();
  let nextNodeID = data.nodes.length + 1;
  let busy = new Map(
    data.commands
      .filter((c) => c.status === 'running' || c.status === 'queued')
      .map((c) => [c.node_id, c.id]),
  );
  let terminalSlots = new Set<string>();
  const sessions = new Set<string>();
  const nameTaken = (name: string, exceptID?: string) =>
    data.nodes.some(
      (n) =>
        n.name === name &&
        n.id !== exceptID &&
        ['invited', 'review', 'approved'].includes(n.admission),
    );
  const timers = new Set<ReturnType<typeof setTimeout>>();
  const sockets = new WebSocketServer({ noServer: true });
  const owners = new Map<WebSocket, { nodeID: string; session?: string }>();
  const sessionOf = (req: IncomingMessage) =>
    req.headers.cookie
      ?.split('; ')
      .find((c) => c.startsWith('probex_mock_session='))
      ?.split('=')[1];
  function authenticated(req: IncomingMessage) {
    const cookie = sessionOf(req);
    return (
      req.headers.authorization === 'Bearer mock' ||
      (!!cookie && sessions.has(cookie))
    );
  }
  function reset() {
    for (const timer of timers) clearTimeout(timer);
    timers.clear();
    for (const socket of sockets.clients) socket.close(1000, 'mock data reset');
    terminalSlots = new Set<string>();
    data = fixtures();
    nextNodeID = data.nodes.length + 1;
    busy = new Map(
      data.commands
        .filter((c) => c.status === 'running' || c.status === 'queued')
        .map((c) => [c.node_id, c.id]),
    );
  }
  function later(action: () => void, delay: number) {
    const timer = setTimeout(() => {
      timers.delete(timer);
      action();
    }, delay);
    timers.add(timer);
  }
  const json = (res: ServerResponse, status: number, value: unknown) => {
    res.writeHead(status, {
      'Content-Type': 'application/json; charset=utf-8',
      'Cache-Control': 'no-store',
    });
    res.end(JSON.stringify(value));
  };
  const problem = (res: ServerResponse, status: number, error: string) =>
    json(res, status, { error });
  return {
    name: 'probex-mock',
    apply: 'serve',
    transformIndexHtml: () => [
      {
        tag: 'script',
        attrs: { type: 'module', src: '/mock/controls.ts' },
        injectTo: 'head',
      },
    ],
    configureServer(server) {
      const upgrade = (req: IncomingMessage, socket: Duplex, head: Buffer) => {
        const match = req.url?.match(/^\/api\/nodes\/([^/]+)\/terminal$/);
        if (!match) return;
        const node = data.nodes.find((n) => n.id === match[1]);
        const status = !authenticated(req)
          ? 401
          : !node?.online || terminalSlots.has(node.id)
            ? 409
            : 101;
        if (status !== 101) {
          socket.end(
            `HTTP/1.1 ${status} Rejected\r\nConnection: close\r\n\r\n`,
          );
          return;
        }
        sockets.handleUpgrade(req, socket, head, (ws) => {
          owners.set(ws, { nodeID: node!.id, session: sessionOf(req) });
          ws.once('close', () => owners.delete(ws));
          const slots = terminalSlots;
          slots.add(node!.id);
          terminal(ws, node!, () => slots.delete(node!.id));
        });
      };
      server.httpServer!.on('upgrade', upgrade);
      server.httpServer!.once('close', () => {
        for (const timer of timers) clearTimeout(timer);
        for (const socket of sockets.clients) socket.terminate();
        sockets.close();
        server.httpServer!.off('upgrade', upgrade);
      });
      server.middlewares.use(async (req, res, next) => {
        const url = new URL(req.url!, 'http://mock.local');
        if (!url.pathname.startsWith('/api/')) {
          next();
          return;
        }
        try {
          let body: Record<string, unknown> = {};
          if (req.method === 'POST' || req.method === 'PATCH') {
            const chunks = [];
            for await (const chunk of req) chunks.push(chunk);
            const text = Buffer.concat(chunks).toString();
            if (text) body = JSON.parse(text);
          }
          const path = url.pathname.slice(5),
            method = req.method;
          if (path === 'login' && method === 'POST') {
            if (body.token !== 'mock') {
              problem(res, 401, 'invalid token');
              return;
            }
            const session = randomBytes(16).toString('hex');
            sessions.add(session);
            res.setHeader(
              'Set-Cookie',
              `probex_mock_session=${session}; Path=/; HttpOnly; SameSite=Strict`,
            );
            json(res, 200, { authenticated: true });
            return;
          }
          if (!authenticated(req)) {
            problem(res, 401, 'login required');
            return;
          }
          if (path === 'session') {
            json(res, 200, { authenticated: true });
            return;
          }
          if (path === 'logout' && method === 'POST') {
            const session = sessionOf(req);
            if (session) sessions.delete(session);
            res.setHeader(
              'Set-Cookie',
              'probex_mock_session=; Path=/; Max-Age=0',
            );
            for (const [socket, owner] of owners)
              if (owner.session === session)
                socket.close(1000, 'mock sign out');
            json(res, 200, { ok: true });
            return;
          }
          if (path === 'mock/scenario' && method === 'POST') {
            if (body.scenario === 'default') reset();
            else if (body.scenario === 'empty') {
              reset();
              data.nodes = [];
              data.commands = [];
              busy.clear();
            } else if (
              body.scenario === 'offline' ||
              body.scenario === 'online' ||
              body.scenario === 'enroll'
            ) {
              for (const socket of sockets.clients)
                socket.close(1000, 'mock scenario changed');
              const info = { ...fixtures().nodes[0].info };
              for (const node of data.nodes) {
                if (body.scenario === 'enroll') {
                  if (node.admission !== 'invited') continue;
                  node.admission = 'approved';
                  node.joined_at = new Date().toISOString();
                  node.key_expires_at = undefined;
                  node.info = { ...info, hostname: node.name };
                } else if (node.admission !== 'approved') continue;
                node.online = body.scenario !== 'offline';
                node.status = node.online ? 'online' : 'offline';
                if (node.online) node.last_seen = new Date().toISOString();
              }
            } else {
              problem(res, 400, 'unknown mock scenario');
              return;
            }
            json(res, 200, { ok: true });
            return;
          }
          for (const node of data.nodes) {
            if (
              node.admission === 'invited' &&
              node.key_expires_at &&
              Date.parse(node.key_expires_at) <= Date.now()
            ) {
              node.admission = 'expired';
              node.status = 'expired';
            }
          }
          if (path === 'nodes' && method === 'GET') {
            for (const node of data.nodes)
              if (node.online) node.last_seen = new Date().toISOString();
            json(res, 200, data.nodes);
            return;
          }
          const parts = path.split('/');
          if (parts[0] === 'commands' && method === 'GET') {
            const command = data.commands.find((c) => c.id === parts[1]);
            if (!command) problem(res, 404, 'not found');
            else json(res, 200, command);
            return;
          }
          let node = data.nodes.find((n) => n.id === parts[1]);
          const create = path === 'nodes' && method === 'POST';
          if (
            create ||
            (parts[0] === 'nodes' &&
              parts[2] === 'enrollment' &&
              method === 'POST')
          ) {
            if (create) {
              if (
                typeof body.name !== 'string' ||
                !/^[\p{L}\p{N}_.-]{1,64}$/u.test(body.name)
              ) {
                problem(res, 400, 'invalid node name');
                return;
              }
              if (nameTaken(body.name)) {
                problem(res, 409, 'node name already exists');
                return;
              }
              node = {
                id: String(nextNodeID++),
                name: body.name,
                online: false,
                admission: 'invited',
                join_mode: 'key',
                status: 'pending',
                created_at: new Date().toISOString(),
                info: {
                  hostname: '',
                  os: '',
                  platform: '',
                  kernel: '',
                  arch: '',
                  cpus: 0,
                  cpu_model: '',
                  uptime_seconds: 0,
                },
                last_seen: '0001-01-01T00:00:00Z',
              };
              data.nodes.unshift(node);
            }
            if (!node) {
              problem(res, 404, 'not found');
              return;
            }
            if (
              node.join_mode !== 'key' ||
              !['invited', 'expired'].includes(node.admission)
            ) {
              problem(
                res,
                409,
                'node admission state does not allow this operation',
              );
              return;
            }
            if (nameTaken(node.name, node.id)) {
              problem(res, 409, 'node name already exists');
              return;
            }
            node.admission = 'invited';
            node.status = 'pending';
            const expires_at = new Date(Date.now() + 86400_000).toISOString();
            node.key_expires_at = expires_at;
            const key =
              'px1_' +
              Buffer.from(
                JSON.stringify({
                  v: 1,
                  port: server.config.server.port,
                  node_id: node.id,
                  name: node.name,
                  token: randomBytes(32).toString('hex'),
                  expires_at,
                }),
              ).toString('base64url');
            json(res, create ? 201 : 200, { node, key, expires_at });
            return;
          }
          if (!node) {
            problem(res, 404, 'not found');
            return;
          }
          if (
            parts.length === 3 &&
            ['approve', 'reject'].includes(parts[2]) &&
            method === 'POST'
          ) {
            if (node.join_mode !== 'request' || node.admission !== 'review') {
              problem(
                res,
                409,
                'node admission state does not allow this operation',
              );
              return;
            }
            node.admission = parts[2] === 'approve' ? 'approved' : 'rejected';
            node.status = parts[2] === 'approve' ? 'offline' : 'rejected';
            node.online = false;
            if (parts[2] === 'approve')
              node.joined_at = new Date().toISOString();
            json(res, 200, node);
            return;
          }
          if (parts.length === 2 && method === 'GET') {
            json(res, 200, node);
            return;
          }
          if (parts.length === 2 && method === 'PATCH') {
            if (
              typeof body.name !== 'string' ||
              !/^[\p{L}\p{N}_.-]{1,64}$/u.test(body.name) ||
              Buffer.byteLength(body.name) > 64
            ) {
              problem(res, 400, 'invalid node name');
              return;
            }
            if (
              ['invited', 'review', 'approved'].includes(node.admission) &&
              nameTaken(body.name, node.id)
            ) {
              problem(res, 409, 'node name already exists');
              return;
            }
            node.name = body.name;
            data.nodes.sort((a, b) => a.name.localeCompare(b.name));
            json(res, 200, node);
            return;
          }
          if (parts.length === 2 && method === 'DELETE') {
            for (const [socket, owner] of owners)
              if (owner.nodeID === node.id)
                socket.close(1000, 'mock node removed');
            data.nodes = data.nodes.filter((n) => n.id !== node!.id);
            data.commands = data.commands.filter((c) => c.node_id !== node!.id);
            busy.delete(node.id);
            json(res, 200, { ok: true });
            return;
          }
          if (parts[2] === 'commands' && method === 'GET') {
            const offset = Number(url.searchParams.get('offset') || 0);
            json(
              res,
              200,
              data.commands
                .filter((c) => c.node_id === node!.id)
                .slice(offset, offset + 20)
                .map((c) => ({ ...c, stdout: null, stderr: null })),
            );
            return;
          }
          if (parts[2] === 'commands' && method === 'DELETE') {
            data.commands = data.commands.filter(
              (c) =>
                c.node_id !== node!.id ||
                c.status === 'queued' ||
                c.status === 'running',
            );
            json(
              res,
              200,
              data.commands
                .filter((c) => c.node_id === node!.id)
                .slice(0, 20)
                .map((c) => ({ ...c, stdout: null, stderr: null })),
            );
            return;
          }
          if (parts[2] === 'commands' && method === 'POST') {
            if (!node.online || busy.has(node.id)) {
              problem(res, 409, 'node offline or busy');
              return;
            }
            if (typeof body.command !== 'string' || !body.command.trim()) {
              problem(res, 400, 'command required');
              return;
            }
            const record: MockCommand = {
              id: randomBytes(16).toString('hex'),
              node_id: node.id,
              command: body.command,
              timeout_seconds: Number(body.timeout_seconds) || 60,
              status: 'queued',
              created_at: new Date().toISOString(),
              stdout: '',
              stderr: '',
            };
            data.commands.unshift(record);
            busy.set(node.id, record.id);
            later(() => {
              record.status = 'running';
              record.stdout = encode('Mock task started…\n');
            }, 650);
            later(
              () => {
                Object.assign(record, commandResult(record.command, node!), {
                  finished_at: new Date().toISOString(),
                });
                busy.delete(record.node_id);
              },
              record.command === 'mock:slow' ? 12000 : 1800,
            );
            json(res, 202, record);
            return;
          }
          problem(res, 404, 'not found');
        } catch (error) {
          server.config.logger.error(`Mock API: ${String(error)}`);
          problem(res, 500, 'mock request failed');
        }
      });
      server.config.logger.info(
        'Probex Mock: token=mock; 27 nodes; simulated API/terminals; data resets on restart.',
      );
    },
  };
}
