import { untrack } from 'svelte';
import type { Node } from '@/features/nodes/types';
import {
  FileError,
  fileRequest,
  fileJSON,
  type FileEntry,
} from '@/features/files/api';
import { t } from '@/shared/settings.svelte';
import { api } from '@/shared/api';
import {
  updateKey,
  type UpdateNotice,
  type UpdateScope,
} from '@/shared/updates';

export type Transfer = {
  id: number;
  nodeID: string;
  nodeName: string;
  windows: boolean;
  target: string;
  relative: string;
  size: number;
  offset: number;
  state: 'running' | 'queued' | 'paused' | 'failed' | 'completed';
  error: string;
  file?: File;
  serverID?: string;
  owner?: string;
  overwrite: boolean;
  busy: boolean;
  cancelled: boolean;
  operation?: Promise<void>;
  controller?: AbortController;
};
type Upload = {
  id: string;
  path: string;
  size: number;
  offset: number;
  state: string;
};
type Event = {
  type: string;
  id?: string;
  node_id?: string;
  upload?: Upload;
  error?: string;
};
export type PickedFile = { file: File; relative: string; overwrite?: boolean };
export function createTransfers(onerror: (message: string) => void) {
  let tasks = $state<Transfer[]>([]),
    owner = $state(''),
    connecting = $state(false),
    visible = $state(false);
  let sequence = 0,
    socket: WebSocket | undefined,
    ready: Promise<string> | undefined,
    closed = false;
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  let reconnectDelay = 1000;
  const listeners = new Map<
    string,
    {
      scope: UpdateScope;
      callbacks: Set<(event: UpdateNotice) => void>;
    }
  >();
  const updates = new Map<string, Event>();
  function complete(task: Transfer) {
    if (task.state === 'completed') return;
    task.state = 'completed';
    task.error = '';
    task.file = undefined;
  }
  function update(event: Event) {
    const data = event.upload;
    if (!data) return;
    updates.set(data.id, event);
    const task = tasks.find(
      (task) => task.serverID === data.id && task.owner === owner,
    );
    if (!task || task.cancelled) return;
    task.offset = Math.max(task.offset, data.offset);
    task.size = data.size;
    if (data.state === 'completed') complete(task);
    if (data.state === 'failed') {
      task.state = 'failed';
      task.error = event.error || t('transferFailed');
      task.controller?.abort();
    }
    pump();
  }
  function subscribe(
    scope: UpdateScope,
    callback: (event: UpdateNotice) => void,
  ) {
    const key = updateKey(scope);
    let listener = listeners.get(key);
    if (!listener) {
      listener = { scope, callbacks: new Set() };
      listeners.set(key, listener);
    }
    listener.callbacks.add(callback);
    if (untrack(() => owner) && socket?.readyState === WebSocket.OPEN)
      socket.send(JSON.stringify({ type: 'subscribe', ...scope }));
    return () => {
      listener.callbacks.delete(callback);
      if (listener.callbacks.size) return;
      listeners.delete(key);
      if (untrack(() => owner) && socket?.readyState === WebSocket.OPEN)
        socket.send(JSON.stringify({ type: 'unsubscribe', ...scope }));
    };
  }
  function reconnect() {
    if (closed || reconnectTimer !== undefined) return;
    reconnectTimer = setTimeout(async () => {
      reconnectTimer = undefined;
      if (closed || socket) return;
      try {
        await api('session');
        if (!closed) await connect();
      } catch {
        reconnect();
      }
    }, reconnectDelay);
    reconnectDelay = Math.min(reconnectDelay * 2, 30000);
  }
  function connect(): Promise<string> {
    if (
      socket &&
      (socket.readyState === WebSocket.OPEN ||
        socket.readyState === WebSocket.CONNECTING)
    )
      return ready!;
    closed = false;
    clearTimeout(reconnectTimer);
    reconnectTimer = undefined;
    connecting = true;
    ready = new Promise<string>((resolve, reject) => {
      const connection = new WebSocket(
        `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/file-sessions`,
      );
      socket = connection;
      connection.onmessage = (message) => {
        if (socket !== connection) return;
        const event = JSON.parse(message.data) as Event | UpdateNotice;
        if (event.type === 'session') {
          owner = event.id!;
          connecting = false;
          reconnectDelay = 1000;
          for (const { scope } of listeners.values())
            connection.send(JSON.stringify({ type: 'subscribe', ...scope }));
          resolve(owner);
        } else if ('topic' in event) {
          const listener = listeners.get(updateKey(event));
          if (listener)
            for (const callback of listener.callbacks) callback(event);
        } else update(event);
      };
      connection.onerror = () => reject(new Error(t('fileSessionError')));
      connection.onclose = () => {
        reject(new Error(t('fileSessionEnded')));
        if (socket !== connection) return;
        socket = undefined;
        owner = '';
        connecting = false;
        ready = undefined;
        if (!closed)
          for (const task of tasks)
            if (!['completed', 'failed'].includes(task.state)) {
              task.state = 'failed';
              task.error = t('fileSessionEnded');
              task.controller?.abort();
            }
        reconnect();
      };
    });
    return ready;
  }
  async function session() {
    if (!ready) throw new Error(t('fileSessionEnded'));
    return ready;
  }
  function close() {
    closed = true;
    clearTimeout(reconnectTimer);
    reconnectTimer = undefined;
    socket?.close();
    socket = undefined;
    ready = undefined;
    owner = '';
    for (const task of tasks) {
      task.cancelled = true;
      task.controller?.abort();
    }
    tasks = [];
    visible = false;
    updates.clear();
  }
  function make(
    node: Node,
    target: string,
    relative: string,
    size: number,
  ): Transfer {
    return {
      id: ++sequence,
      nodeID: node.id,
      nodeName: node.name,
      windows: node.info.os === 'windows',
      target,
      relative,
      size,
      offset: 0,
      state: 'queued',
      error: '',
      overwrite: false,
      busy: false,
      cancelled: false,
    };
  }
  async function upload(task: Transfer, signal: AbortSignal) {
    if (!task.serverID) {
      const created = await fileJSON<Upload>(
        `nodes/${task.nodeID}/uploads`,
        {
          method: 'POST',
          body: JSON.stringify({
            directory: task.target,
            relative: task.relative,
            size: task.size,
            overwrite: task.overwrite,
          }),
          signal,
        },
        task.owner,
      );
      task.serverID = created.id;
      task.offset = created.offset;
      const event = updates.get(created.id);
      if (event) update(event);
    } else {
      const response = await fileRequest(
        `uploads/${task.serverID}`,
        { method: 'HEAD', signal },
        task.owner,
      );
      task.offset = Number(response.headers.get('Upload-Offset'));
    }
    if (task.cancelled || task.state === 'paused' || task.state === 'failed')
      return;
    while (task.offset < task.size) {
      const end = Math.min(task.size, task.offset + 8 * 1024 * 1024);
      const result = await fileJSON<Upload>(
        `uploads/${task.serverID}`,
        {
          method: 'PATCH',
          body: task.file!.slice(task.offset, end),
          headers: { 'Upload-Offset': String(task.offset) },
          signal,
        },
        task.owner,
      );
      task.offset = result.offset;
    }
    const result = await fileJSON<Upload>(
      `uploads/${task.serverID}/commit`,
      { method: 'POST', signal },
      task.owner,
    );
    if (result.state !== 'completed') throw new Error(t('transferFailed'));
    task.offset = result.offset;
    complete(task);
  }
  async function run(task: Transfer) {
    const controller = new AbortController();
    task.controller = controller;
    try {
      task.owner = await session();
      if (task.cancelled || task.state === 'paused') return;
      await upload(task, controller.signal);
    } catch (error) {
      if (task.cancelled || task.state === 'paused') return;
      task.state = 'failed';
      task.error =
        error instanceof FileError && error.code === 'exists'
          ? t('fileNameExists')
          : (error as Error).message;
    } finally {
      task.busy = false;
      task.controller = undefined;
      task.operation = undefined;
      pump();
    }
  }
  function pump() {
    const occupied = new Set(
      tasks
        .filter((task) => task.busy || task.state === 'running')
        .map((task) => task.nodeID),
    );
    for (const task of tasks)
      if (
        !task.cancelled &&
        task.state === 'queued' &&
        !occupied.has(task.nodeID)
      ) {
        task.state = 'running';
        task.busy = true;
        occupied.add(task.nodeID);
        task.operation = run(task);
      }
  }
  async function enqueue(node: Node, target: string, files: PickedFile[]) {
    if (!target) {
      onerror(t('chooseDirectory'));
      return;
    }
    const added = files.map(({ file, relative, overwrite = false }) => ({
      ...make(node, target, relative, file.size),
      file,
      overwrite,
    }));
    tasks = [...tasks, ...added];
    visible = true;
    try {
      await connect();
      pump();
    } catch (error) {
      for (const task of added) {
        task.state = 'failed';
        task.error = (error as Error).message;
      }
    }
  }
  async function download(node: Node, entries: FileEntry[]) {
    const owner = await session();
    for (const entry of entries) {
      const link = document.createElement('a');
      link.href = `/api/nodes/${node.id}/file?${new URLSearchParams({ path: entry.path, file_session: owner })}`;
      link.download = entry.name;
      document.body.append(link);
      link.click();
      link.remove();
    }
  }
  async function pause(task: Transfer) {
    if (task.state === 'queued') {
      task.state = 'paused';
      return;
    }
    if (task.state !== 'running') return;
    task.state = 'paused';
    if (task.serverID) task.controller?.abort();
    pump();
  }
  async function resume(task: Transfer) {
    if (task.busy) await task.operation;
    task.state = 'queued';
    task.error = '';
    pump();
  }
  async function release(task: Transfer) {
    if (task.serverID && task.owner === owner) {
      try {
        await fileRequest(
          `uploads/${task.serverID}`,
          { method: 'DELETE' },
          task.owner,
        );
      } catch (error) {
        if (
          !(error instanceof FileError) ||
          !['not_found', 'cancelled'].includes(error.code)
        )
          throw error;
      }
    }
    if (task.serverID) updates.delete(task.serverID);
    task.serverID = undefined;
    task.offset = 0;
  }
  async function cancel(task: Transfer) {
    task.cancelled = true;
    if (task.serverID) task.controller?.abort();
    try {
      if (task.busy) await task.operation;
      await release(task);
      tasks = tasks.filter((t) => t !== task);
      pump();
    } catch (error) {
      task.cancelled = false;
      task.state = 'failed';
      task.error = (error as Error).message;
    }
  }
  async function restart(task: Transfer) {
    task.controller?.abort();
    try {
      await connect();
      await release(task);
      task.error = '';
      task.state = 'queued';
      task.cancelled = false;
      pump();
    } catch (error) {
      task.state = 'failed';
      task.error = (error as Error).message;
    }
  }
  async function removeCompleted() {
    await Promise.all(
      tasks.filter((task) => task.state === 'completed').map(cancel),
    );
  }
  async function removeAll() {
    const removing = [...tasks];
    for (const task of removing) task.cancelled = true;
    await Promise.all(removing.map(cancel));
  }
  function removeNode(id: string) {
    for (const task of tasks.filter((t) => t.nodeID === id)) {
      task.cancelled = true;
      task.controller?.abort();
    }
    tasks = tasks.filter((t) => t.nodeID !== id);
    pump();
  }
  return {
    get tasks() {
      return tasks;
    },
    get owner() {
      return owner;
    },
    get connecting() {
      return connecting;
    },
    get visible() {
      return visible;
    },
    set visible(value: boolean) {
      visible = value;
    },
    connect,
    subscribe,
    session,
    close,
    enqueue,
    download,
    pause,
    resume,
    cancel,
    removeCompleted,
    removeAll,
    restart,
    removeNode,
  };
}
export type Transfers = ReturnType<typeof createTransfers>;
