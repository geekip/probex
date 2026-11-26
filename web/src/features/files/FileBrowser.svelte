<script lang="ts">
  import { untrack } from 'svelte';
  import { ContextMenu, DropdownMenu } from 'bits-ui';
  import {
    ArrowLeft,
    ArrowRight,
    RotateCw,
    MoreHorizontal,
    Check,
    CircleAlert,
  } from '@lucide/svelte';
  import copyText from 'clipboard-copy';
  import type { Node } from '@/features/nodes/types';
  import {
    FileError,
    directoryTarget,
    fileJSON,
    parentPath,
    remoteJoin,
    type FileEntry,
    type FileListing,
    type FileLink,
  } from '@/features/files/api';
  import type { Transfers } from '@/features/transfers/store.svelte';
  import type { Confirmation } from '@/shared/components/ConfirmDialog.svelte';
  import SearchPopover from '@/shared/components/SearchPopover.svelte';
  import FileNameDialog from '@/features/files/FileNameDialog.svelte';
  import FileLinkDialog from './FileLinkDialog.svelte';
  import FileList from './FileList.svelte';
  import { selectRows } from './selection';
  import FileActions from './FileActions.svelte';
  import { clipboard } from './clipboard.svelte';
  import { droppedFiles } from '@/features/transfers/picker';
  import { t } from '@/shared/settings.svelte';
  let {
    node,
    active,
    manager,
    onerror,
    onrequestconfirm,
    onupload,
    onopen,
    onrenamed,
    onremoved,
  }: {
    node: Node;
    active: boolean;
    manager: Transfers;
    onerror: (message: string) => void;
    onrequestconfirm: (request: Confirmation) => void;
    onupload: (directory: string, kind?: 'files' | 'folder') => void;
    onopen: (entry: FileEntry) => void;
    onrenamed: (path: string, destination: string) => void;
    onremoved: (paths: string[]) => void;
  } = $props();
  const windows = $derived(node.info.os === 'windows');
  let path = $state(''),
    address = $state(''),
    entries = $state<FileEntry[]>([]),
    cursor = $state(''),
    files = $state(0),
    directories = $state(0);
  let history = $state<string[]>([]),
    index = $state(-1),
    selection = $state<string[]>([]),
    anchor = $state('');
  let hidden = $state(localStorage.getItem('probex.files-hidden') !== 'false');
  let columns = $state<string[]>(
    JSON.parse(
      localStorage.getItem('probex.file-columns') ||
        '["mtime","type","size","mode","owner"]',
    ),
  );
  let query = $state(''),
    sort = $state(localStorage.getItem('probex.files-sort') || 'name');
  let order = $state<'asc' | 'desc'>(
    localStorage.getItem('probex.files-order') === 'desc' ? 'desc' : 'asc',
  );
  let serviceFailed = $state(false),
    operating = $state(false);
  let busy = $state(false),
    error = $state(''),
    nextFailed = $state(false),
    contextEntry = $state<FileEntry | null>(null);
  let dialog = $state<{
    kind: 'rename' | 'newFile' | 'newDirectory';
    target: string;
    entry?: FileEntry;
  } | null>(null);
  let sentinel = $state<HTMLDivElement>();
  let contextLink = $state<FileLink | null>(null),
    linkDialog = $state<{ link: FileLink | null; directory: string } | null>(
      null,
    );
  let controller: AbortController | undefined,
    timer: ReturnType<typeof setTimeout>;
  const selected = $derived(
    entries.filter((entry) => selection.includes(entry.path)),
  );
  const uploadRevision = $derived(manager.revision(node.id));
  let seenUploads = 0;
  const contextItems = $derived(
    contextEntry
      ? selection.includes(contextEntry.path)
        ? selected
        : [contextEntry]
      : selected,
  );
  const target = $derived(
    directoryTarget(contextEntry)
      ? contextEntry!
      : { name: '', path, type: 'directory' as const },
  );
  const fields = $derived(
    [
      ...columns.filter((column) => column !== 'type'),
      ...(columns.includes('owner') ? ['group'] : []),
    ].join(','),
  );
  const columnLabels = {
    size: 'fileSize',
    mtime: 'modifiedTime',
    type: 'fileType',
    mode: 'fileMode',
    owner: 'fileOwner',
  } as const;
  async function load(
    destination: string,
    mode: 'push' | 'refresh' | 'filter' | 'more' | 'history' = 'push',
    nextIndex = index,
  ) {
    controller?.abort();
    const request = new AbortController();
    controller = request;
    busy = true;
    error = '';
    serviceFailed = false;
    nextFailed = false;
    const keep = mode === 'refresh' ? new Set(selection) : new Set<string>(),
      previousCount = mode === 'refresh' ? entries.length : 0;
    if (mode === 'filter') {
      selection = [];
      anchor = '';
    }
    try {
      const owner = await manager.session();
      let next = mode === 'more' ? cursor : '',
        collected: FileEntry[] = [];
      let page: FileListing;
      do {
        const params = new URLSearchParams({
          path: destination,
          hidden: String(hidden),
          query,
          fields,
          limit: '200',
          cursor: next,
          sort,
          order,
        });
        if (index < 0 && !destination) params.set('initial', 'true');
        page = await fileJSON<FileListing>(
          `nodes/${node.id}/files?${params}`,
          { signal: request.signal },
          owner,
        );
        collected.push(...page.entries);
        next = page.next_cursor;
      } while (mode === 'refresh' && next && collected.length < previousCount);
      if (request.signal.aborted) return;
      if (mode === 'more') {
        const known = new Set(entries.map((e) => e.path));
        entries = [...entries, ...collected.filter((e) => !known.has(e.path))];
      } else {
        entries = collected;
        selection = entries.filter((e) => keep.has(e.path)).map((e) => e.path);
        anchor = '';
        if (mode === 'push' && (index < 0 || history[index] !== page.path)) {
          history = [...history.slice(0, index + 1), page.path];
          index = history.length - 1;
        }
        if (mode === 'history') index = nextIndex;
      }
      path = page.path;
      address = page.path;
      cursor = next;
      files = page.file_count;
      directories = page.directory_count;
    } catch (failure) {
      if (!request.signal.aborted) {
        error = (failure as Error).message;
        serviceFailed =
          failure instanceof TypeError ||
          !manager.owner ||
          (failure instanceof FileError &&
            ['node_unavailable', 'node_offline'].includes(failure.code));
        if (mode === 'more') nextFailed = true;
      }
    } finally {
      if (controller === request) busy = false;
    }
  }
  function select(entry: FileEntry, event: MouseEvent) {
    if (event.shiftKey && anchor) {
      const a = entries.findIndex((e) => e.path === anchor),
        b = entries.indexOf(entry);
      selection = entries
        .slice(Math.min(a, b), Math.max(a, b) + 1)
        .map((e) => e.path);
    } else if (event.metaKey || event.ctrlKey) {
      selection = selection.includes(entry.path)
        ? selection.filter((path) => path !== entry.path)
        : [...selection, entry.path];
      anchor = entry.path;
    } else {
      selection = [entry.path];
      anchor = entry.path;
    }
  }
  async function resolveEntry(entry: FileEntry) {
    return entry.type === 'directory'
      ? entry
      : fileJSON<FileEntry>(
          `nodes/${node.id}/resolve?${new URLSearchParams({ path: entry.path })}`,
          {},
          await manager.session(),
        );
  }
  async function resolveDirectory(entry: FileEntry) {
    const resolved = await resolveEntry(entry);
    if (resolved.type !== 'directory') throw new Error(t('notDirectory'));
    return resolved.path;
  }
  async function open(entry: FileEntry) {
    try {
      const resolved = await resolveEntry(entry);
      if (resolved.type === 'directory') void load(resolved.path);
      else onopen(resolved);
    } catch (failure) {
      onerror((failure as Error).message);
    }
  }
  function context(event: MouseEvent) {
    const row = (event.target as Element).closest<HTMLElement>(
      '[data-file-path]:not([data-parent])',
    );
    contextEntry = row
      ? entries.find((e) => e.path === row.dataset.filePath) || {
          path: row.dataset.filePath!,
          name: row.dataset.fileName!,
          type: 'directory',
        }
      : null;
    if (!contextEntry) selection = [];
    if (contextEntry && !selection.includes(contextEntry.path))
      selection = entries.some((e) => e.path === contextEntry!.path)
        ? [contextEntry.path]
        : [];
    contextLink = null;
    if (
      contextItems.length === 1 &&
      contextItems[0].type !== 'directory' &&
      node.online
    )
      void inspectContextLink(contextItems[0].path);
  }
  async function inspectContextLink(source: string) {
    try {
      const result = await fileJSON<FileLink>(
        `nodes/${node.id}/links?${new URLSearchParams({ path: source })}`,
        {},
        await manager.session(),
      );
      if (contextItems.length === 1 && contextItems[0].path === source) {
        contextLink = result;
        if (result.hard)
          entries = entries.map((entry) =>
            entry.path === source
              ? { ...entry, link_kind: 'hard', link_target_type: 'file' }
              : entry,
          );
      }
    } catch (failure) {
      onerror((failure as Error).message);
    }
  }
  async function saveLink(name: string, target: string, hard: boolean) {
    if (
      !name ||
      name === '.' ||
      name === '..' ||
      name.includes('/') ||
      (windows && name.includes('\\'))
    )
      throw new Error(t('invalidFileName'));
    const request = linkDialog!;
    if (
      request.link?.kind === 'shortcut' &&
      !hard &&
      !name.toLowerCase().endsWith('.lnk')
    )
      name += '.lnk';
    const destination = remoteJoin(request.directory, name, windows);
    const result = await fileJSON<{ path: string }>(
      `nodes/${node.id}/links`,
      {
        method: request.link ? 'PUT' : 'POST',
        body: JSON.stringify({
          path: request.link?.path || destination,
          destination,
          target,
          hard,
        }),
      },
      await manager.session(),
    );
    if (request.link && request.link.path !== result.path)
      onrenamed(request.link.path, result.path);
    refresh();
  }
  function refresh() {
    void load(path, 'refresh');
  }
  function preference() {
    localStorage.setItem('probex.files-hidden', String(hidden));
    localStorage.setItem('probex.file-columns', JSON.stringify(columns));
    void load(path, 'filter');
  }
  async function copyPaths() {
    try {
      await copyText(
        (contextItems.length ? contextItems.map((e) => e.path) : [path]).join(
          '\n',
        ),
      );
    } catch (failure) {
      onerror((failure as Error).message);
    }
  }
  function removeFiles() {
    const items = [...contextItems];
    if (!items.length) return;
    onrequestconfirm({
      title: 'deleteFiles',
      description: 'confirmDeleteFiles',
      confirmLabel: 'deleteFiles',
      detail: items.map((e) => e.path).join('\n'),
      danger: true,
      action: async () => {
        const removed: string[] = [],
          failures: string[] = [];
        for (const entry of items)
          try {
            await fileJSON(
              `nodes/${node.id}/file?${new URLSearchParams({ path: entry.path, recursive: String(entry.type === 'directory') })}`,
              { method: 'DELETE' },
              await manager.session(),
            );
            removed.push(entry.path);
          } catch (failure) {
            failures.push(`${entry.name}: ${(failure as Error).message}`);
          }
        onremoved(removed);
        refresh();
        if (failures.length) onerror(failures.join('\n'));
      },
    });
  }
  function download() {
    const regular = contextItems.filter((e) => e.type === 'file'),
      unsupported = contextItems.filter((e) => e.type !== 'file');
    if (unsupported.length)
      onrequestconfirm({
        title: 'downloadFiles',
        description: 'directoryDownloadHint',
        confirmLabel: 'close',
        detail: unsupported.map((e) => e.path).join('\n'),
        action: () => {},
      });
    if (regular.length)
      void manager
        .download(node, regular)
        .catch((failure) => onerror(failure.message));
  }
  async function save(name: string) {
    if (
      !name ||
      name === '.' ||
      name === '..' ||
      name.includes('/') ||
      (windows && name.includes('\\'))
    )
      throw new Error(t('invalidFileName'));
    const request = dialog!;
    const destination = remoteJoin(request.target, name, windows);
    if (request.kind === 'rename') {
      const result = await fileJSON<{ path: string }>(
        `nodes/${node.id}/files/rename`,
        {
          method: 'POST',
          body: JSON.stringify({ path: request.entry!.path, destination }),
        },
        await manager.session(),
      );
      onrenamed(request.entry!.path, result.path);
    } else
      await fileJSON(
        `nodes/${node.id}/${request.kind === 'newFile' ? 'files' : 'directories'}`,
        { method: 'POST', body: JSON.stringify({ path: destination }) },
        await manager.session(),
      );
    refresh();
  }
  function setClipboard(cut: boolean) {
    clipboard.nodeID = node.id;
    clipboard.cut = cut;
    clipboard.entries = contextItems.map((entry) => ({ ...entry }));
  }
  async function transfer(items: FileEntry[], target: FileEntry, cut: boolean) {
    operating = true;
    let destination: string;
    try {
      destination = await resolveDirectory(target);
    } catch (failure) {
      operating = false;
      onerror((failure as Error).message);
      return;
    }
    const failures: string[] = [],
      moved: string[] = [];
    for (const entry of items) {
      const to = remoteJoin(destination, entry.name, windows);
      try {
        const result = await fileJSON<{ path: string }>(
          `nodes/${node.id}/files/${cut ? 'rename' : 'copy'}`,
          {
            method: 'POST',
            body: JSON.stringify({ path: entry.path, destination: to }),
          },
          await manager.session(),
        );
        if (cut) {
          onrenamed(entry.path, result.path);
          moved.push(entry.path);
        }
      } catch (failure) {
        failures.push(`${entry.name}: ${(failure as Error).message}`);
      }
    }
    if (cut && clipboard.nodeID === node.id)
      clipboard.entries = clipboard.entries.filter(
        (entry) => !moved.includes(entry.path),
      );
    operating = false;
    refresh();
    if (failures.length) onerror(failures.join('\n'));
  }
  function sorting(field: string) {
    order = sort === field && order === 'asc' ? 'desc' : 'asc';
    sort = field;
    localStorage.setItem('probex.files-sort', sort);
    localStorage.setItem('probex.files-order', order);
    void load(path, 'filter');
  }
  async function upload(items: DataTransferItem[], target: FileEntry) {
    try {
      const [{ files, directories }, destination] = await Promise.all([
        droppedFiles(items),
        resolveDirectory(target),
      ]);
      await manager.enqueue(node, destination, files, directories);
    } catch (failure) {
      onerror((failure as Error).message);
    }
  }
  async function reconnect() {
    try {
      await manager.connect();
      await load(path, 'refresh');
    } catch (failure) {
      error = (failure as Error).message;
      serviceFailed = true;
    }
  }
  $effect(() => {
    const uploaded = uploadRevision !== seenUploads;
    seenUploads = uploadRevision;
    if (active)
      untrack(() => {
        void load(path, uploaded && path ? 'refresh' : 'push');
      });
    return () => {
      controller?.abort();
      clearTimeout(timer);
    };
  });
  $effect(() => {
    if (!sentinel || !active) return;
    const observer = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting && cursor && !busy && !nextFailed)
        void load(path, 'more');
    });
    observer.observe(sentinel);
    return () => observer.disconnect();
  });
</script>

<div class="file-browser">
  <div class="file-navigation">
    <button
      class="icon-button"
      aria-label={t('backDirectory')}
      title={t('backDirectory')}
      disabled={busy || index <= 0 || !node.online}
      onclick={() => load(history[index - 1], 'history', index - 1)}
    >
      <ArrowLeft size={15} />
    </button><button
      class="icon-button"
      aria-label={t('forwardDirectory')}
      title={t('forwardDirectory')}
      disabled={busy || index >= history.length - 1 || !node.online}
      onclick={() => load(history[index + 1], 'history', index + 1)}
    >
      <ArrowRight size={15} />
    </button><button
      class="icon-button"
      aria-label={t('refreshFiles')}
      title={t('refreshFiles')}
      disabled={busy || !node.online}
      onclick={refresh}
    >
      <RotateCw size={15} />
    </button>
    <form
      onsubmit={(e) => {
        e.preventDefault();
        void load(address);
      }}
    >
      <input
        aria-label={t('directoryAddress')}
        bind:value={address}
        placeholder={windows ? t('drives') : '/'}
        disabled={busy || !node.online}
      />
    </form>
    <SearchPopover
      label={t('searchFiles')}
      bind:value={query}
      onquery={() => {
        clearTimeout(timer);
        timer = setTimeout(() => void load(path, 'filter'), 250);
      }}
    />
    <DropdownMenu.Root>
      <DropdownMenu.Trigger
        class="icon-button"
        aria-label={t('fileOptions')}
        title={t('fileOptions')}
      >
        <MoreHorizontal size={15} />
      </DropdownMenu.Trigger><DropdownMenu.Portal>
        <DropdownMenu.Content class="menu" align="end" sideOffset={6}>
          <DropdownMenu.CheckboxItem
            class="menu-item"
            checked={hidden}
            onCheckedChange={(value) => {
              hidden = value;
              preference();
            }}
          >
            <span class="check-slot">
              {#if hidden}<Check size={14} />{/if}
            </span>{t('showHidden')}
          </DropdownMenu.CheckboxItem><DropdownMenu.Separator
            class="menu-separator"
          />
          {#each Object.entries(columnLabels) as [key, label]}<DropdownMenu.CheckboxItem
              class="menu-item"
              checked={columns.includes(key)}
              onCheckedChange={(value) => {
                columns = value
                  ? [...columns, key]
                  : columns.filter((c) => c !== key);
                preference();
              }}
            >
              <span class="check-slot">
                {#if columns.includes(key)}<Check size={14} />{/if}
              </span>{t(label)}
            </DropdownMenu.CheckboxItem>{/each}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  </div>
  {#if serviceFailed || !node.online}<div
      class="file-service-error"
      role="alert"
    >
      <CircleAlert size={22} /><strong>{t('fileServiceFailed')}</strong>
      <button class="quiet-button" disabled={busy} onclick={reconnect}>
        {t('reconnectFiles')}
      </button>
      {#if error}<small>{error}</small>{/if}
    </div>{:else if error}<p class="file-error" role="alert">{error}</p>{/if}
  <ContextMenu.Root>
    <ContextMenu.Trigger class="files-context" oncontextmenu={context}>
      <div
        class="file-list-scroll"
        use:selectRows={{
          disabled: !node.online || operating,
          selection,
          onchange: (paths) => {
            selection = paths;
            anchor = paths.at(-1) || '';
          },
        }}
      >
        <FileList
          {entries}
          {path}
          {windows}
          nodeID={node.id}
          {columns}
          {selection}
          {sort}
          {order}
          disabled={!node.online || operating}
          onselect={select}
          onopen={open}
          onsort={sorting}
          onmove={(items, destination) =>
            void transfer(items, destination, true)}
          onupload={upload}
        />
        {#if !entries.length && !busy}<div class="file-empty">
            {t('emptyDirectory')}
          </div>{/if}
        <div class="files-sentinel" bind:this={sentinel}>
          {#if busy}<span>{t('loading')}</span>{:else if cursor}<button
              class="quiet-button"
              onclick={() => load(path, 'more')}
            >
              {t(nextFailed ? 'retryLoad' : 'loadMoreFiles')}
            </button>{/if}
        </div>
      </div>
    </ContextMenu.Trigger>
    <FileActions
      count={contextItems.length}
      online={node.online && !operating}
      directory={!!path}
      canPaste={clipboard.nodeID === node.id && clipboard.entries.length > 0}
      canEditLink={contextItems.length === 1 && !!contextLink?.kind}
      onlink={(edit) =>
        (linkDialog = {
          link: edit ? contextLink : null,
          directory: edit ? parentPath(contextItems[0].path, windows) : path,
        })}
      onopen={() => open(contextItems[0])}
      oncut={() => setClipboard(true)}
      oncopy={() => setClipboard(false)}
      onpaste={() =>
        void transfer([...clipboard.entries], target, clipboard.cut)}
      onnew={(kind) => (dialog = { kind, target: path })}
      onupload={(kind) => onupload(path, kind)}
      onrename={() =>
        (dialog = {
          kind: 'rename',
          target: parentPath(contextItems[0].path, windows),
          entry: contextItems[0],
        })}
      onpath={copyPaths}
      ondownload={download}
      ondelete={removeFiles}
    />
  </ContextMenu.Root>
  <footer class="file-counts">
    {t('totalEntries')}
    {directories + files} · {t('totalDirectories')}
    {directories} · {t('totalFiles')}
    {files}
    {#if selection.length}<span>
        {t('selectedFiles')}
        {selection.length}
      </span>{/if}
  </footer>
</div>
{#if dialog}<FileNameDialog
    title={dialog.kind}
    initial={dialog.entry?.name || ''}
    target={dialog.target}
    onsave={save}
    onclose={() => (dialog = null)}
  />{/if}
{#if linkDialog}<FileLinkDialog
    link={linkDialog.link}
    directory={linkDialog.directory}
    {windows}
    onsave={saveLink}
    onclose={() => (linkDialog = null)}
  />{/if}
