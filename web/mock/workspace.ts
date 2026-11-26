import { randomBytes, createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { posix, win32 } from 'node:path';
import type { IncomingMessage, ServerResponse } from 'node:http';
import type { WebSocket } from 'ws';
import type { Node } from '@/features/nodes/types';
import type { FileEntry } from '@/features/files/api';

type Item = {
  entry: FileEntry;
  bytes: Buffer;
  link?: { target: string; kind: 'symlink' | 'shortcut' };
  hardID?: string;
};
type Transfer = {
  id: string;
  node_id: string;
  owner: string;
  path: string;
  size: number;
  offset: number;
  state: string;
  error?: string;
  bytes: Buffer;
  overwrite?: boolean;
  response?: ServerResponse;
  timer?: ReturnType<typeof setTimeout>;
};
const id = () => randomBytes(12).toString('hex');
const version = (bytes: Buffer) =>
  '"' + createHash('sha256').update(bytes).digest('hex') + '"';
const json = (res: ServerResponse, status: number, value: unknown) => {
  res.writeHead(status, {
    'Content-Type': 'application/json',
    'Cache-Control': 'no-store',
  });
  res.end(JSON.stringify(value));
};
const problem = (
  res: ServerResponse,
  status: number,
  error: string,
  code = 'invalid_request',
) => json(res, status, { error, code });
const media: Record<string, { bytes: Buffer; mime: string }> =
  Object.fromEntries(
    [
      ['image.png', 'image/png'],
      ['audio.wav', 'audio/wav'],
      ['video.webm', 'video/webm'],
      ['document.pdf', 'application/pdf'],
    ].map(([name, mime]) => [
      name,
      {
        bytes: readFileSync(new URL('./fixtures/' + name, import.meta.url)),
        mime,
      },
    ]),
  );

// These are in-memory UI fixtures. They never access the host filesystem or network.
export function workspace(nodes: () => Node[]) {
  const trees = new Map<string, Map<string, Item>>();
  const owners = new Map<string, { socket: WebSocket; session?: string }>();
  const uploads = new Map<string, Transfer>(),
    downloads = new Map<string, Transfer>();
  const forwards = new Map<
    string,
    {
      id: string;
      node_id: string;
      name: string;
      protocol: string;
      listen_addr: string;
      target_host: string;
      target_port: number;
      created_at: string;
    }
  >();
  function paths(node: Node) {
    const path = node.info.os === 'windows' ? win32 : posix;
    const home = path === win32 ? 'C:\\Users\\Administrator' : '/root';
    return {
      path,
      home,
      absolute: (value: string) => path.resolve(home, value || home),
    };
  }
  function tree(node: Node) {
    let result = trees.get(node.id);
    if (result) return result;
    result = new Map();
    trees.set(node.id, result);
    const { path, home } = paths(node);
    const add = (
      name: string,
      bytes: Buffer | string = '',
      directory = false,
      size?: number,
    ) => {
      const full = path.resolve(home, name),
        data = typeof bytes === 'string' ? Buffer.from(bytes) : bytes;
      result!.set(full, {
        entry: {
          name: path.basename(full) || full,
          path: full,
          type: directory ? 'directory' : 'file',
          size: directory ? undefined : (size ?? data.length),
          mtime: '2026-11-20T12:00:00Z',
          mode: directory ? 'drwxr-xr-x' : '-rw-r--r--',
          owner: node.info.os === 'windows' ? undefined : 'root',
          group: node.info.os === 'windows' ? undefined : 'root',
          uid: node.info.os === 'windows' ? undefined : 0,
          gid: node.info.os === 'windows' ? undefined : 0,
        },
        bytes: data,
      });
    };
    for (let parent = home; ; parent = path.dirname(parent)) {
      add(parent, '', true);
      if (path.dirname(parent) === parent) break;
    }
    for (const folder of ['logs', 'config', 'nested', 'nested/deeper', 'many'])
      add(folder, '', true);
    add(
      'README.md',
      '# Mock workspace\r\n\n中文文件编辑\rOriginal newlines stay intact.\r\n',
    );
    add('config/settings.json', '{\n  "mock": true,\n  "enabled": true\n}\n');
    add('nested/deeper/hello.txt', 'Lazy directory fixture\n');
    add(
      '连接示例.txt',
      '连接预览示例\n软连接与硬连接均可打开，硬连接共享编辑内容。\n',
    );
    const addLink = (
      name: string,
      target: string,
      kind: 'symlink' | 'shortcut' = 'symlink',
    ) => {
      const full = path.resolve(home, name);
      result!.set(full, {
        entry: {
          name,
          path: full,
          type: kind === 'shortcut' ? 'file' : 'symlink',
          link_kind: kind,
          mtime: '2026-11-24T12:00:00Z',
        },
        bytes: Buffer.alloc(0),
        link: {
          target: kind === 'shortcut' ? path.resolve(home, target) : target,
          kind,
        },
      });
    };
    addLink('文件软连接.txt', '连接示例.txt');
    addLink('文件夹软连接', 'config');
    addLink('失效连接.txt', '不存在的文件.txt');
    const shared = result.get(path.resolve(home, '连接示例.txt'))!;
    shared.hardID = id();
    const hardPath = path.resolve(home, '文件硬连接.txt');
    result.set(hardPath, {
      entry: { ...shared.entry, name: '文件硬连接.txt', path: hardPath },
      bytes: shared.bytes,
      hardID: shared.hardID,
    });
    if (node.info.os === 'windows') {
      addLink('文件快捷方式.lnk', '连接示例.txt', 'shortcut');
      addLink('文件夹快捷方式.lnk', 'config', 'shortcut');
    }
    add('.hidden', 'hidden fixture\n');
    add('empty.txt');
    add('readonly.txt', 'read only fixture\n', false, (2 << 20) + 1);
    add('oversized.txt', '', false, (10 << 20) + 1);
    add('binary.bin', Buffer.from([0, 255, 1]));
    add('unsafe.html', '<script>window.__PROBEX_FILE_EXECUTED=true</script>');
    add(
      'image.svg',
      '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="80"><rect width="100" height="80" fill="#16845e"/><script>window.__PROBEX_FILE_EXECUTED=true</script></svg>',
    );
    for (const [name, fixture] of Object.entries(media))
      add(name, fixture.bytes);
    for (let i = 0; i < 245; i++)
      add(
        'many/sample-' + String(i).padStart(3, '0') + '.txt',
        'sample ' + i + '\n',
      );
    return result;
  }
  function bytes(item: Item) {
    if (item.bytes.length === item.entry.size) return item.bytes;
    const content = Buffer.alloc(item.entry.size!, 'x');
    item.bytes.copy(content);
    return content;
  }
  function emit(task: Transfer, upload: boolean) {
    const {
      bytes: _,
      response: __,
      timer: ___,
      owner: ____,
      overwrite: _____,
      ...metadata
    } = task;
    const socket = owners.get(task.owner)?.socket;
    if (socket?.readyState === 1)
      socket.send(
        JSON.stringify({
          type: upload ? 'upload' : 'download',
          [upload ? 'upload' : 'download']: metadata,
        }),
      );
    return metadata;
  }
  function drop(task: Transfer, upload: boolean, reason: string) {
    clearTimeout(task.timer);
    task.state = 'failed';
    task.error = reason;
    task.response?.destroy();
    emit(task, upload);
    if (upload) uploads.delete(task.id);
  }
  function closeOwner(owner: string) {
    for (const task of uploads.values())
      if (task.owner === owner) drop(task, true, 'file session ended');
    for (const task of downloads.values())
      if (task.owner === owner && task.state !== 'completed')
        drop(task, false, 'file session ended');
    owners.delete(owner);
  }
  function stream(task: Transfer) {
    if (task.state !== 'running') return;
    const response = task.response!;
    if (task.offset === task.size) {
      task.state = 'completed';
      response.end();
      emit(task, false);
      return;
    }
    const end = Math.min(task.offset + 32768, task.size);
    response.write(task.bytes.subarray(task.offset, end));
    task.offset = end;
    emit(task, false);
    task.timer = setTimeout(() => stream(task), 40);
  }
  return {
    open(socket: WebSocket, session?: string) {
      const owner = id();
      owners.set(owner, { socket, session });
      socket.send(JSON.stringify({ type: 'session', id: owner }));
      socket.once('close', () => closeOwner(owner));
    },
    removeNode(nodeID: string) {
      trees.delete(nodeID);
      for (const [key, mapping] of forwards)
        if (mapping.node_id === nodeID) forwards.delete(key);
      for (const task of uploads.values())
        if (task.node_id === nodeID) drop(task, true, 'node removed');
      for (const task of downloads.values())
        if (task.node_id === nodeID && task.state !== 'completed')
          drop(task, false, 'node removed');
    },
    offline() {
      for (const task of uploads.values()) drop(task, true, 'node offline');
      for (const task of downloads.values())
        if (task.state !== 'completed') drop(task, false, 'node offline');
    },
    reset() {
      for (const owner of owners.values())
        owner.socket.close(1000, 'mock reset');
      for (const owner of [...owners.keys()]) closeOwner(owner);
      trees.clear();
      forwards.clear();
      uploads.clear();
      downloads.clear();
    },
    handle(
      req: IncomingMessage,
      res: ServerResponse,
      url: URL,
      body: Record<string, unknown>,
      raw: Buffer,
      session?: string,
    ): boolean {
      const parts = url.pathname.slice(5).split('/'),
        method = req.method;
      if (parts[0] === 'forwards') {
        const withStatus = (
          mapping: typeof forwards extends Map<string, infer T> ? T : never,
        ) => ({
          ...mapping,
          node_name: nodes().find((n) => n.id === mapping.node_id)?.name,
          status: !nodes().find((n) => n.id === mapping.node_id)?.online
            ? 'node_offline'
            : mapping.target_port === 6661
              ? 'target_error'
              : 'ready',
          error:
            mapping.target_port === 6661
              ? 'Mock: target connection refused'
              : '',
        });
        if (method === 'GET') {
          json(
            res,
            200,
            [...forwards.values()]
              .filter(
                (f) =>
                  !url.searchParams.has('node_id') ||
                  f.node_id === url.searchParams.get('node_id'),
              )
              .map(withStatus),
          );
          return true;
        }
        if (method === 'DELETE') {
          if (!forwards.delete(parts[1]))
            problem(res, 404, 'mapping not found', 'not_found');
          else json(res, 200, { ok: true });
          return true;
        }
        if (method === 'POST' || method === 'PUT') {
          const existing =
            method === 'PUT' ? forwards.get(parts[1]) : undefined;
          if (method === 'PUT' && !existing) {
            problem(res, 404, 'mapping not found', 'not_found');
            return true;
          }
          if (existing && body.node_id && body.node_id !== existing.node_id) {
            problem(res, 400, 'cannot change forward node');
            return true;
          }
          const node = nodes().find(
            (n) => n.id === (existing ? existing.node_id : body.node_id),
          );
          if (!node || node.admission !== 'approved') {
            problem(res, 409, 'node not approved');
            return true;
          }
          if (
            !['tcp', 'udp', 'tcp+udp'].includes(String(body.protocol)) ||
            !/^.+:\d+$/.test(String(body.listen_addr)) ||
            !body.target_host ||
            !Number.isInteger(body.target_port) ||
            Number(body.target_port) < 1 ||
            Number(body.target_port) > 65535
          ) {
            problem(res, 400, 'invalid mapping');
            return true;
          }
          if (
            [...forwards.values()].some(
              (f) =>
                f.id !== existing?.id &&
                f.protocol
                  .split('+')
                  .some((p) => String(body.protocol).split('+').includes(p)) &&
                f.listen_addr === body.listen_addr,
            )
          ) {
            problem(
              res,
              409,
              'forward listen address already configured',
              'forward_port_unavailable',
            );
            return true;
          }
          if (String(body.listen_addr).endsWith(':6660')) {
            problem(
              res,
              409,
              'Mock: address already in use',
              'forward_port_unavailable',
            );
            return true;
          }
          const mapping = {
            id: existing ? existing.id : id(),
            node_id: node.id,
            name: String(body.name || body.listen_addr).trim(),
            protocol: String(body.protocol),
            listen_addr: String(body.listen_addr),
            target_host: String(body.target_host),
            target_port: Number(body.target_port),
            created_at: existing
              ? existing.created_at
              : new Date().toISOString(),
          };
          forwards.set(mapping.id, mapping);
          json(res, existing ? 200 : 201, withStatus(mapping));
          return true;
        }
        problem(res, 405, 'method not allowed');
        return true;
      }
      const nodeRoute =
        parts[0] === 'nodes' &&
        [
          'files',
          'directories',
          'file',
          'stat',
          'resolve',
          'links',
          'text',
          'preview',
          'uploads',
          'downloads',
        ].includes(parts[2]);
      if (!nodeRoute && !['uploads', 'downloads'].includes(parts[0]))
        return false;
      const owner = String(
          req.headers['x-probex-file-session'] ||
            url.searchParams.get('file_session') ||
            '',
        ),
        identity = owners.get(owner);
      if (!identity || identity.session !== session) {
        problem(res, 403, 'file session required', 'owner_required');
        return true;
      }
      const node = nodeRoute
        ? nodes().find((n) => n.id === parts[1])
        : undefined;
      if (nodeRoute && (!node || !node.online)) {
        problem(res, 409, 'node offline', 'node_unavailable');
        return true;
      }
      if (!nodeRoute) {
        const upload = parts[0] === 'uploads',
          list = upload ? uploads : downloads,
          task = list.get(parts[1]);
        if (!task || task.owner !== owner) {
          problem(res, 404, 'transfer not found', 'not_found');
          return true;
        }
        if (method === 'DELETE') {
          drop(task, upload, 'cancelled');
          list.delete(task.id);
          json(res, 200, { ok: true });
          return true;
        }
        if (method === 'HEAD') {
          res.writeHead(200, {
            'Upload-Offset': task.offset,
            'Upload-Length': task.size,
            'Upload-State': task.state,
          });
          res.end();
          return true;
        }
        if (upload && method === 'PATCH') {
          if (Number(req.headers['upload-offset']) !== task.offset) {
            problem(res, 409, 'offset mismatch', 'offset_mismatch');
            return true;
          }
          if (raw.length > 8 << 20 || task.offset + raw.length > task.size) {
            problem(res, 413, 'upload too large');
            return true;
          }
          task.bytes = Buffer.concat([task.bytes, raw]);
          task.offset += raw.length;
          setTimeout(() => {
            if (uploads.has(task.id)) json(res, 200, emit(task, true));
            else problem(res, 404, 'upload cancelled', 'not_found');
          }, 150);
          return true;
        }
        if (upload && method === 'POST' && parts[2] === 'commit') {
          if (task.offset !== task.size) {
            problem(res, 409, 'upload incomplete');
            return true;
          }
          const n = nodes().find((n) => n.id === task.node_id)!,
            entries = tree(n),
            { path } = paths(n);
          if (entries.has(task.path) && !task.overwrite) {
            problem(res, 409, 'destination exists', 'exists');
            return true;
          }
          entries.set(task.path, {
            entry: {
              name: path.basename(task.path),
              path: task.path,
              type: 'file',
              size: task.size,
              mtime: new Date().toISOString(),
              mode: '-rw-r--r--',
            },
            bytes: task.bytes,
          });
          task.state = 'completed';
          json(res, 200, emit(task, true));
          return true;
        }
        if (
          !upload &&
          method === 'POST' &&
          ['pause', 'resume'].includes(parts[2])
        ) {
          if (task.state === 'failed' || task.state === 'completed') {
            problem(res, 409, 'download cannot resume');
            return true;
          }
          clearTimeout(task.timer);
          task.state =
            parts[2] === 'pause'
              ? 'paused'
              : task.response
                ? 'running'
                : 'queued';
          json(res, 200, emit(task, false));
          if (task.state === 'running') stream(task);
          return true;
        }
        if (!upload && method === 'GET') {
          if (task.response || task.state !== 'queued') {
            problem(res, 409, 'download already claimed');
            return true;
          }
          task.response = res;
          task.state = 'running';
          res.writeHead(200, {
            'Content-Type': 'application/octet-stream',
            'Content-Length': task.size,
            'Content-Disposition': `attachment; filename*=UTF-8''${encodeURIComponent(posix.basename(task.path.replaceAll('\\', '/')))}`,
          });
          res.once('close', () => {
            if (task.state !== 'completed')
              drop(task, false, 'download connection lost; start again');
          });
          stream(task);
          return true;
        }
        problem(res, 405, 'method not allowed');
        return true;
      }
      const n = node!,
        { path, absolute, home } = paths(n),
        entries = tree(n),
        operation = parts[2],
        full = absolute(
          operation === 'files' && method === 'GET'
            ? url.searchParams.get('path') ||
                (url.searchParams.get('initial') === 'true'
                  ? home
                  : path.parse(home).root)
            : String(url.searchParams.get('path') ?? body.path ?? home),
        ),
        item = entries.get(full);
      const resolve = (source: string): string | null => {
        const seen = new Set<string>();
        let current = source;
        while (entries.get(current)?.link) {
          if (seen.has(current)) return null;
          seen.add(current);
          current = path.resolve(
            path.dirname(current),
            entries.get(current)!.link!.target,
          );
        }
        return entries.has(current) ? current : null;
      };
      if (operation === 'resolve' && method === 'GET') {
        const resolved = resolve(full);
        if (!resolved)
          problem(res, 404, 'link target missing or cyclic', 'not_found');
        else json(res, 200, entries.get(resolved)!.entry);
        return true;
      }
      if (operation === 'links') {
        const hard =
          !!item?.hardID &&
          [...entries.values()].filter((v) => v.hardID === item.hardID).length >
            1;
        if (method === 'GET' && item) {
          json(res, 200, {
            path: full,
            name: path.basename(full),
            target: item.link?.target || '',
            kind: item.link?.kind || (hard ? 'hard' : ''),
            hard,
          });
          return true;
        }
        if (method === 'POST' || method === 'PUT') {
          const destination = absolute(String(body.destination || full));
          if (method === 'PUT' && !item?.link && !hard) {
            problem(res, 400, 'path is not a link');
            return true;
          }
          if (
            (method === 'POST' || destination !== full) &&
            entries.has(destination)
          ) {
            problem(res, 409, 'destination exists', 'exists');
            return true;
          }
          if (
            entries.get(path.dirname(destination))?.entry.type !== 'directory'
          ) {
            problem(res, 404, 'parent not found', 'not_found');
            return true;
          }
          const target = String(body.target || (body.hard && hard ? full : ''));
          if (!target) {
            problem(res, 400, 'target path is required');
            return true;
          }
          const kind =
            n.info.os === 'windows' &&
            destination.toLowerCase().endsWith('.lnk')
              ? 'shortcut'
              : 'symlink';
          let created: Item;
          if (body.hard) {
            const key = resolve(
                path.resolve(path.dirname(destination), target),
              ),
              source = key ? entries.get(key) : undefined;
            if (!source || source.entry.type !== 'file') {
              problem(res, 400, 'hard links require a regular file');
              return true;
            }
            source.hardID ||= id();
            created = {
              entry: {
                ...source.entry,
                name: path.basename(destination),
                path: destination,
              },
              bytes: source.bytes,
              hardID: source.hardID,
            };
          } else
            created = {
              entry: {
                name: path.basename(destination),
                path: destination,
                type: kind === 'shortcut' ? 'file' : 'symlink',
                link_kind: kind,
                mtime: new Date().toISOString(),
              },
              bytes: Buffer.alloc(0),
              link: {
                target:
                  kind === 'shortcut'
                    ? path.resolve(path.dirname(destination), target)
                    : target,
                kind,
              },
            };
          if (method === 'PUT') entries.delete(full);
          entries.set(destination, created);
          json(res, method === 'PUT' ? 200 : 201, { path: destination });
          return true;
        }
        problem(
          res,
          item ? 405 : 404,
          item ? 'method not allowed' : 'path not found',
        );
        return true;
      }
      if (
        operation === 'files' &&
        method === 'GET' &&
        n.info.os === 'windows' &&
        !url.searchParams.get('path') &&
        url.searchParams.get('initial') !== 'true'
      ) {
        const root = path.parse(home).root;
        json(res, 200, {
          path: '',
          entries: [{ name: root, path: root, type: 'directory' }],
          next_cursor: '',
          file_count: 0,
          directory_count: 1,
        });
        return true;
      }
      if (operation === 'uploads' && method === 'POST') {
        if (body.relative === 'mock-no-space.bin') {
          problem(res, 507, 'Mock: no space left on device', 'no_space');
          return true;
        }
        const destination = absolute(
          path.join(String(body.directory), String(body.relative)),
        );
        if (entries.has(destination) && !body.overwrite) {
          problem(res, 409, 'destination exists', 'exists');
          return true;
        }
        if (
          entries.get(path.dirname(destination))?.entry.type !== 'directory'
        ) {
          problem(res, 404, 'parent not found', 'not_found');
          return true;
        }
        const task: Transfer = {
          id: id(),
          node_id: n.id,
          owner,
          path: destination,
          size: Number(body.size),
          offset: 0,
          state: 'receiving',
          bytes: Buffer.alloc(0),
          overwrite: !!body.overwrite,
        };
        uploads.set(task.id, task);
        json(res, 201, emit(task, true));
        return true;
      }
      if (
        (operation === 'files' || operation === 'directories') &&
        method === 'POST' &&
        !parts[3]
      ) {
        if (item) {
          problem(res, 409, 'destination exists', 'exists');
          return true;
        }
        if (entries.get(path.dirname(full))?.entry.type !== 'directory') {
          problem(res, 404, 'parent not found', 'not_found');
          return true;
        }
        entries.set(full, {
          entry: {
            name: path.basename(full),
            path: full,
            type: operation === 'directories' ? 'directory' : 'file',
            size: operation === 'files' ? 0 : undefined,
            mtime: new Date().toISOString(),
            mode: operation === 'directories' ? 'drwxr-xr-x' : '-rw-r--r--',
          },
          bytes: Buffer.alloc(0),
        });
        json(res, 201, { path: full });
        return true;
      }
      if (!item) {
        problem(res, 404, 'path not found', 'not_found');
        return true;
      }
      if (
        operation === 'files' &&
        ['rename', 'copy'].includes(parts[3]) &&
        method === 'POST'
      ) {
        const destination = absolute(String(body.destination));
        if (entries.has(destination)) {
          problem(res, 409, 'destination exists', 'exists');
          return true;
        }
        for (const [key, value] of [...entries])
          if (key === full || key.startsWith(full + path.sep)) {
            const to = destination + key.slice(full.length);
            if (parts[3] === 'rename') entries.delete(key);
            entries.set(to, {
              entry: { ...value.entry, path: to, name: path.basename(to) },
              bytes: Buffer.from(value.bytes),
              link: value.link ? { ...value.link } : undefined,
              hardID: parts[3] === 'rename' ? value.hardID : undefined,
            });
          }
        json(res, parts[3] === 'copy' ? 201 : 200, { path: destination });
        return true;
      }
      if (operation === 'file' && method === 'DELETE') {
        for (const key of entries.keys())
          if (key === full || key.startsWith(full + path.sep))
            entries.delete(key);
        json(res, 200, { ok: true });
        return true;
      }
      if (operation === 'stat') {
        json(res, 200, item.entry);
        return true;
      }
      if (operation === 'files' && method === 'GET') {
        if (item.entry.type !== 'directory') {
          problem(res, 400, 'not a directory');
          return true;
        }
        const hardCounts = new Map<string, number>();
        for (const item of entries.values())
          if (item.hardID)
            hardCounts.set(item.hardID, (hardCounts.get(item.hardID) || 0) + 1);
        let list = [...entries.values()]
          .map((v): FileEntry => {
            const target = v.link ? resolve(v.entry.path) : null;
            const targetType = target
              ? entries.get(target)!.entry.type
              : undefined;
            return {
              ...v.entry,
              link_target_type:
                targetType === 'file' || targetType === 'directory'
                  ? targetType
                  : undefined,
              link_error:
                v.link && !target ? 'link target missing or cyclic' : undefined,
              link_kind:
                v.hardID && hardCounts.get(v.hardID)! > 1
                  ? 'hard'
                  : v.entry.link_kind,
            };
          })
          .filter(
            (e) =>
              e.path !== full &&
              path.dirname(e.path) === full &&
              (url.searchParams.get('hidden') === 'true' ||
                !e.name.startsWith('.')) &&
              e.name
                .toLowerCase()
                .includes((url.searchParams.get('query') || '').toLowerCase()),
          );
        const sort = url.searchParams.get('sort') || 'name',
          desc = url.searchParams.get('order') === 'desc';
        const directory = (e: FileEntry) =>
          e.type === 'directory' || e.link_target_type === 'directory';
        const compare = (a: string | number, b: string | number) =>
          a < b ? -1 : a > b ? 1 : 0;
        const value = (e: FileEntry): string | number =>
          sort === 'name'
            ? e.name.toLowerCase()
            : sort === 'size'
              ? (e.size ?? 0)
              : sort === 'mtime'
                ? (e.mtime ?? '')
                : sort === 'type'
                  ? directory(e)
                    ? 'directory'
                    : e.type !== 'file'
                      ? e.type
                      : path.extname(e.name).toLowerCase()
                  : sort === 'mode'
                    ? (e.mode ?? '')
                    : (e.owner ?? e.uid ?? '') + '/' + (e.group ?? e.gid ?? '');
        list.sort((a, b) => {
          if (directory(a) !== directory(b)) return directory(a) ? -1 : 1;
          const c =
            compare(value(a), value(b)) ||
            compare(a.name.toLowerCase(), b.name.toLowerCase()) ||
            compare(a.name, b.name);
          return desc ? -c : c;
        });
        const file_count = list.filter((e) => !directory(e)).length,
          directory_count = list.length - file_count;
        const cursor = url.searchParams.get('cursor');
        if (cursor)
          list = list.slice(list.findIndex((e) => e.name === cursor) + 1);
        const limit = Number(url.searchParams.get('limit') || 200),
          next = list.length > limit ? list[limit - 1].name : '';
        list = list.slice(0, limit);
        const fields = new Set([
          'name',
          'path',
          'type',
          'link_kind',
          'link_target_type',
          'link_error',
          ...(url.searchParams.get('fields') || '').split(','),
        ]);
        json(res, 200, {
          path: full,
          entries: list.map((e) =>
            Object.fromEntries(
              Object.entries(e).filter(([key]) => fields.has(key)),
            ),
          ),
          next_cursor: next,
          file_count,
          directory_count,
        });
        return true;
      }
      if (item.entry.type !== 'file') {
        problem(res, 400, 'not a regular file');
        return true;
      }
      if (operation === 'downloads' && method === 'POST') {
        const task: Transfer = {
          id: id(),
          node_id: n.id,
          owner,
          path: full,
          size: item.entry.size!,
          offset: 0,
          state: 'queued',
          bytes: bytes(item),
        };
        downloads.set(task.id, task);
        json(res, 201, emit(task, false));
        return true;
      }
      if (operation === 'text') {
        if (method === 'PUT') {
          if (item.entry.size! > 2 << 20) {
            problem(res, 413, 'file is read only', 'too_large');
            return true;
          }
          if (body.encoding !== 'utf-8') {
            problem(
              res,
              422,
              'Mock saves UTF-8 only; validate GBK with a real Agent',
              'invalid_text',
            );
            return true;
          }
          const content = Buffer.from(
            (body.bom ? '\ufeff' : '') + String(body.text),
          );
          if (content.length > 2 << 20) {
            problem(res, 413, 'encoded text exceeds 2 MiB', 'too_large');
            return true;
          }
          if (!body.overwrite && body.version !== version(bytes(item))) {
            problem(res, 409, 'file changed', 'version_conflict');
            return true;
          }
          for (const shared of entries.values())
            if (
              shared === item ||
              (item.hardID && shared.hardID === item.hardID)
            ) {
              shared.bytes = content;
              shared.entry.size = content.length;
              shared.entry.mtime = new Date().toISOString();
            }
        }
        const download_only = item.entry.size! > 10 << 20,
          content = download_only ? Buffer.alloc(0) : bytes(item),
          bom = content.subarray(0, 3).equals(Buffer.from([239, 187, 191]));
        if (content.includes(0) || content.includes(255)) {
          problem(res, 422, 'not valid text', 'invalid_text');
          return true;
        }
        json(res, 200, {
          path: full,
          size: item.entry.size,
          text: content.subarray(bom ? 3 : 0).toString(),
          encoding: 'utf-8',
          bom,
          version: download_only ? '' : version(content),
          readonly: item.entry.size! > 2 << 20,
          download_only,
        });
        return true;
      }
      if (operation === 'preview' || operation === 'file') {
        const mime =
          (
            {
              '.png': 'image/png',
              '.jpg': 'image/jpeg',
              '.jpeg': 'image/jpeg',
              '.gif': 'image/gif',
              '.webp': 'image/webp',
              '.svg': 'image/svg+xml',
              '.wav': 'audio/wav',
              '.mp3': 'audio/mpeg',
              '.webm': 'video/webm',
              '.mp4': 'video/mp4',
              '.pdf': 'application/pdf',
            } as Record<string, string>
          )[path.extname(full).toLowerCase()] || '';
        if (operation === 'preview' && !mime) {
          problem(res, 415, 'unsupported preview type');
          return true;
        }
        const content = bytes(item),
          range = req.headers.range?.match(/^bytes=(\d+)-(\d*)$/),
          start = range ? Number(range[1]) : 0,
          end = range?.[2]
            ? Math.min(Number(range[2]), content.length - 1)
            : content.length - 1;
        if (start >= content.length && range) {
          res.writeHead(416, { 'Content-Range': 'bytes */' + content.length });
          res.end();
          return true;
        }
        const headers: Record<string, string | number> = {
          'Content-Type': mime || 'application/octet-stream',
          'Content-Length': Math.max(0, end - start + 1),
          'Accept-Ranges': 'bytes',
          'X-Content-Type-Options': 'nosniff',
          'Content-Security-Policy':
            "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:",
        };
        if (range)
          headers['Content-Range'] = `bytes ${start}-${end}/${content.length}`;
        res.writeHead(range ? 206 : 200, headers);
        res.end(
          method === 'HEAD' ? undefined : content.subarray(start, end + 1),
        );
        return true;
      }
      problem(res, 405, 'method not allowed');
      return true;
    },
  };
}
