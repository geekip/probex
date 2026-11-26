<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
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
  import PendingLabel from '@/shared/components/PendingLabel.svelte';
  import FileNameDialog from '@/features/files/FileNameDialog.svelte';
  import FileLinkDialog from './FileLinkDialog.svelte';
  import UploadPicker from './UploadPicker.svelte';
  import UploadConflictDialog from './UploadConflictDialog.svelte';
  import {
    checkUpload,
    createUploadDirectories,
    type UploadPlan,
    type UploadSelection,
  } from './upload-preflight';
  import FileList from './FileList.svelte';
  import { compareFileEntries, isDirectory } from './entry-sort';
  import { selectRows } from './selection';
  import FileActions from './FileActions.svelte';
  import { clipboard } from './clipboard.svelte';
  import { droppedFiles } from '@/features/transfers/picker';
  import { t } from '@/shared/settings.svelte';
  let {
    node,
    active,
    loadingFiles,
    manager,
    onerror,
    onrequestconfirm,
    onopen,
    onrenamed,
    onremoved,
  }: {
    node: Node;
    active: boolean;
    loadingFiles: boolean;
    manager: Transfers;
    onerror: (message: string) => void;
    onrequestconfirm: (request: Confirmation) => void;
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
    operating = $state(false),
    reconnecting = $state(false);
  let busy = $state(false),
    error = $state(''),
    retryMode = $state<'more' | 'refresh' | null>(null),
    contextEntry = $state<FileEntry | null>(null);
  let dialog = $state<{
    kind: 'rename' | 'newFile' | 'newDirectory';
    target: string;
    entry?: FileEntry;
  } | null>(null);
  let linkDialog = $state<{ source?: string; directory: string } | null>(null);
  let controller: AbortController | undefined;
  let opened = false;
  let pendingDirectoryLink = '';
  let uploadPicker: UploadPicker;
  let uploadController: AbortController | undefined;
  let uploadPlan = $state.raw<UploadPlan | null>(null);
  onDestroy(() => uploadController?.abort());
  const searchText = $derived(query.toLowerCase());
  const matches = $derived(
    searchText
      ? entries.filter((entry) => entry.name.toLowerCase().includes(searchText))
      : entries,
  );
  const visibleEntries = $derived(
    [...matches].sort((a, b) => compareFileEntries(a, b, sort, order)),
  );
  const visibleDirectories = $derived(
    searchText ? matches.filter(isDirectory).length : directories,
  );
  const visibleFiles = $derived(
    searchText ? matches.length - visibleDirectories : files,
  );
  const selected = $derived(
    visibleEntries.filter((entry) => selection.includes(entry.path)),
  );
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
  const columnLabels = {
    size: 'fileSize',
    mtime: 'modifiedTime',
    type: 'fileType',
    mode: 'fileMode',
    owner: 'fileOwner',
  } as const;
  async function load(
    destination: string,
    mode: 'push' | 'refresh' | 'more' | 'history' = 'push',
    nextIndex = index,
    resolveLink = false,
  ) {
    controller?.abort();
    const request = new AbortController();
    controller = request;
    busy = true;
    error = '';
    serviceFailed = false;
    retryMode = null;
    const initial = index < 0 && !destination;
    if (resolveLink) pendingDirectoryLink = destination;
    else if (destination !== path) pendingDirectoryLink = '';
    const resolving = !!destination && destination === pendingDirectoryLink;
    if (mode === 'push' || mode === 'history') {
      if (destination !== path) {
        entries = [];
        cursor = '';
        files = directories = 0;
        selection = [];
        contextEntry = null;
        anchor = '';
      }
      path = address = destination;
      if (mode === 'history') index = nextIndex;
      else if (!initial && history[index] !== destination) {
        history = [...history.slice(0, index + 1), destination];
        index = history.length - 1;
      }
    }
    let next = mode === 'more' ? cursor : '';
    const known = new Set(next ? entries.map((entry) => entry.path) : []);
    try {
      if (resolving) {
        const owner = await manager.session();
        const entry = await fileJSON<FileEntry>(
          `nodes/${node.id}/resolve?${new URLSearchParams({ path: destination })}`,
          { signal: request.signal },
          owner,
        );
        if (request.signal.aborted) return;
        if (entry.type !== 'directory') throw new Error(t('notDirectory'));
        pendingDirectoryLink = '';
        destination = entry.path;
        path = address = destination;
        history[index] = destination;
      }
      const owner = await manager.session();
      do {
        const params = new URLSearchParams({
          path: destination,
          hidden: String(hidden),
          fields: 'size,mtime,mode,owner,group',
          limit: '200',
          cursor: next,
        });
        if (initial && !next) params.set('initial', 'true');
        const page = await fileJSON<FileListing>(
          `nodes/${node.id}/files?${params}`,
          { signal: request.signal },
          owner,
        );
        if (request.signal.aborted) return;
        const added = page.entries.filter((entry) => {
          if (known.has(entry.path)) return false;
          known.add(entry.path);
          return true;
        });
        if (next) entries = [...entries, ...added];
        else {
          entries = added;
          destination = path = address = page.path;
          if (index < 0) index = 0;
          history[index] = page.path;
          anchor = '';
        }
        next = cursor = page.next_cursor;
        files = page.file_count;
        directories = page.directory_count;
      } while (next);
      selection = selection.filter((entry) => known.has(entry));
    } catch (failure) {
      if (!request.signal.aborted) {
        error = (failure as Error).message;
        serviceFailed =
          failure instanceof TypeError ||
          !manager.owner ||
          (failure instanceof FileError &&
            ['node_unavailable', 'node_offline'].includes(failure.code));
        if (next)
          retryMode =
            failure instanceof FileError && failure.code === 'invalid_cursor'
              ? 'refresh'
              : 'more';
      }
    } finally {
      if (controller === request) busy = false;
    }
  }
  function select(entry: FileEntry, event: MouseEvent) {
    if (event.shiftKey && anchor) {
      const a = visibleEntries.findIndex((e) => e.path === anchor),
        b = visibleEntries.indexOf(entry);
      selection = visibleEntries
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
  function open(entry: FileEntry) {
    if (entry.type === 'directory' || entry.link_target_type === 'directory') {
      void load(entry.path, 'push', index, entry.type !== 'directory');
    } else onopen(entry);
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
  }
  function openLinkDialog(edit: boolean) {
    const source = edit ? contextItems[0].path : undefined;
    linkDialog = {
      source,
      directory: source ? parentPath(source, windows) : path,
    };
  }
  async function loadLink(source: string, signal: AbortSignal) {
    const link = await fileJSON<FileLink>(
      `nodes/${node.id}/links?${new URLSearchParams({ path: source })}`,
      { signal },
      await manager.session(),
    );
    if (!signal.aborted && link.hard)
      entries = entries.map((entry) =>
        entry.path === source && !entry.link_kind
          ? { ...entry, link_kind: 'hard' }
          : entry,
      );
    return link;
  }
  async function saveLink(
    name: string,
    target: string,
    hard: boolean,
    link: FileLink | null,
  ) {
    if (
      !name ||
      name === '.' ||
      name === '..' ||
      name.includes('/') ||
      (windows && name.includes('\\'))
    )
      throw new Error(t('invalidFileName'));
    if (
      link?.kind === 'shortcut' &&
      !hard &&
      !name.toLowerCase().endsWith('.lnk')
    )
      name += '.lnk';
    const destination = remoteJoin(linkDialog!.directory, name, windows);
    const result = await fileJSON<{ path: string }>(
      `nodes/${node.id}/links`,
      {
        method: link ? 'PUT' : 'POST',
        body: JSON.stringify({
          path: link?.path || destination,
          destination,
          target,
          hard,
        }),
      },
      await manager.session(),
    );
    if (link && link.path !== result.path) onrenamed(link.path, result.path);
  }
  function refresh() {
    void load(path, 'refresh');
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
        if (failures.length) onerror(failures.join('\n'));
      },
    });
  }
  function download() {
    const regular = contextItems.filter((e) => e.type === 'file'),
      unsupported = contextItems.filter((e) => e.type !== 'file');
    if (unsupported.length)
      onrequestconfirm({
        title: 'download',
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
  }
  function setClipboard(cut: boolean) {
    clipboard.nodeID = node.id;
    clipboard.cut = cut;
    clipboard.entries = contextItems.map((entry) => ({ ...entry }));
  }
  async function transfer(items: FileEntry[], target: FileEntry, cut: boolean) {
    if (operating) return;
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
    if (failures.length) onerror(failures.join('\n'));
  }
  function sorting(field: string) {
    order = sort === field && order === 'asc' ? 'desc' : 'asc';
    sort = field;
    localStorage.setItem('probex.files-sort', sort);
    localStorage.setItem('probex.files-order', order);
  }
  async function uploadOperation(
    action: (signal: AbortSignal) => Promise<void>,
  ) {
    if (operating) return;
    const request = new AbortController();
    uploadController = request;
    operating = true;
    try {
      await action(request.signal);
    } catch (failure) {
      if (!request.signal.aborted) onerror((failure as Error).message);
      request.abort();
    } finally {
      operating = false;
      uploadController = undefined;
    }
  }
  async function startUpload(
    plan: UploadPlan,
    replace: boolean,
    signal: AbortSignal,
  ) {
    await createUploadDirectories(node.id, plan, windows, signal);
    signal.throwIfAborted();
    const files = replace
      ? plan.files
      : plan.files.filter((file) => !file.overwrite);
    if (files.length) await manager.enqueue(node, plan.target, files);
  }
  function prepareUpload(
    selection: () => UploadSelection | Promise<UploadSelection>,
  ) {
    if (uploadPlan) return;
    return uploadOperation(async (signal) => {
      const picked = await selection();
      if (!picked.files.length && !picked.directories.length) return;
      if (!picked.target) throw new Error(t('chooseDirectory'));
      const plan = await checkUpload(
        node.id,
        picked,
        windows,
        await manager.session(),
        signal,
      );
      signal.throwIfAborted();
      if (plan.conflicts.length) uploadPlan = plan;
      else await startUpload(plan, false, signal);
    });
  }
  function upload(items: DataTransferItem[], target: FileEntry) {
    return prepareUpload(async () => {
      const [selected, destination] = await Promise.all([
        droppedFiles(items),
        resolveDirectory(target),
      ]);
      return { ...selected, target: destination };
    });
  }
  async function reconnect() {
    if (reconnecting) return;
    reconnecting = true;
    try {
      await manager.connect();
      await load(path, 'refresh');
    } catch (failure) {
      error = (failure as Error).message;
      serviceFailed = true;
    } finally {
      reconnecting = false;
    }
  }
  $effect(() => {
    if (!active) return;
    if (!opened) {
      opened = true;
      untrack(() => void load(path));
    }
    return () => {
      if (busy) opened = false;
      controller?.abort();
    };
  });
</script>

<div class="file-browser">
  <div class="file-navigation">
    <button
      class="icon-button"
      aria-label={t('backDirectory')}
      title={t('backDirectory')}
      disabled={index <= 0 || !node.online}
      onclick={() => load(history[index - 1], 'history', index - 1)}
    >
      <ArrowLeft size={15} />
    </button><button
      class="icon-button"
      aria-label={t('forwardDirectory')}
      title={t('forwardDirectory')}
      disabled={index >= history.length - 1 || !node.online}
      onclick={() => load(history[index + 1], 'history', index + 1)}
    >
      <ArrowRight size={15} />
    </button><button
      class="icon-button"
      aria-label={t('refresh')}
      title={t('refresh')}
      disabled={busy || !node.online}
      onclick={refresh}
    >
      <RotateCw size={15} />
    </button>
    <form
      onsubmit={(e) => {
        e.preventDefault();
        if (!node.online) return;
        void load(address);
      }}
    >
      <input
        aria-label={t('directoryAddress')}
        bind:value={address}
        placeholder={windows ? t('drives') : '/'}
        disabled={!node.online}
      />
    </form>
    <SearchPopover
      label={t('searchFiles')}
      bind:value={query}
      onquery={() => {
        selection = [];
        anchor = '';
        contextEntry = null;
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
              localStorage.setItem('probex.files-hidden', String(hidden));
              refresh();
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
                localStorage.setItem(
                  'probex.file-columns',
                  JSON.stringify(columns),
                );
              }}
            >
              <span class="check-slot">
                {#if columns.includes(key)}<Check size={14} />{/if}
              </span>{t(label)}
            </DropdownMenu.CheckboxItem>{/each}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
    {#if busy || loadingFiles || reconnecting}
      <div
        class="file-navigation-progress"
        role="progressbar"
        aria-label={t('loading')}
      >
        <span></span>
      </div>
    {/if}
  </div>
  {#if serviceFailed || !node.online}<div
      class="file-service-error"
      role="alert"
    >
      <CircleAlert size={22} /><strong>{t('fileServiceFailed')}</strong>
      <button
        class="quiet-button"
        disabled={busy || reconnecting}
        onclick={reconnect}
      >
        {#if reconnecting}<PendingLabel />{:else}{t('reconnect')}{/if}
      </button>
    </div>{/if}
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
          entries={visibleEntries}
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
        {#if !visibleEntries.length && !busy && !error}<div class="file-empty">
            {t('emptyDirectory')}
          </div>{/if}
      </div>
    </ContextMenu.Trigger>
    <FileActions
      count={contextItems.length}
      online={node.online && !operating}
      directory={!!path}
      canPaste={clipboard.nodeID === node.id && clipboard.entries.length > 0}
      canEditLink={contextItems.length === 1 &&
        (!!contextItems[0].link_kind ||
          (windows && contextItems[0].type === 'file'))}
      onlink={openLinkDialog}
      onopen={() => open(contextItems[0])}
      oncut={() => setClipboard(true)}
      oncopy={() => setClipboard(false)}
      onpaste={() =>
        void transfer([...clipboard.entries], target, clipboard.cut)}
      onnew={(kind) => (dialog = { kind, target: path })}
      onupload={(kind) => uploadPicker.choose(path, kind)}
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
  {#if error}<div class="file-error" role="alert">
      <CircleAlert size={14} aria-hidden="true" />
      <span>{t('directoryLoadFailed')} · {error}</span>
      {#if retryMode}
        <button class="quiet-button" onclick={() => load(path, retryMode!)}>
          {t('retryLoad')}
        </button>
      {/if}
    </div>{/if}
  <footer class="file-counts">
    {t('totalEntries')}
    {visibleDirectories + visibleFiles} · {t('totalDirectories')}
    {visibleDirectories} · {t('totalFiles')}
    {visibleFiles}
    {#if cursor}<span role="status">
        {t('directoryEntriesLoaded')}
        {entries.length} / {directories + files}
      </span>{/if}
    {#if operating}<span class="file-operation-status" role="status">
        <PendingLabel />
      </span>{/if}
    {#if selected.length}<span>
        {t('selectedFiles')}
        {selected.length}
      </span>{/if}
  </footer>
</div>
<UploadPicker
  bind:this={uploadPicker}
  onselect={(target, files) =>
    void prepareUpload(() => ({ target, files, directories: [] }))}
/>
{#if uploadPlan}
  <UploadConflictDialog
    plan={uploadPlan}
    onclose={() => (uploadPlan = null)}
    onchoose={(replace) => {
      const plan = uploadPlan!;
      uploadPlan = null;
      void uploadOperation((signal) => startUpload(plan, replace, signal));
    }}
  />
{/if}
{#if dialog}<FileNameDialog
    title={dialog.kind}
    initial={dialog.entry?.name || ''}
    target={dialog.target}
    onsave={save}
    onclose={() => (dialog = null)}
  />{/if}
{#if linkDialog}<FileLinkDialog
    source={linkDialog.source}
    directory={linkDialog.directory}
    {windows}
    onload={loadLink}
    onsave={saveLink}
    onclose={() => (linkDialog = null)}
  />{/if}
