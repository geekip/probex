<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import {
    X,
    RotateCw,
    Search,
    Server,
    ArrowRight,
    KeyRound,
    CircleDot,
    Plus,
    FileBraces,
  } from '@lucide/svelte';
  import { api, ApiError, onUnauthorized } from '@/shared/api';
  import type { Node } from '@/features/nodes/types';
  import {
    preferences,
    dark,
    language,
    t,
    date,
  } from '@/shared/settings.svelte';
  import SettingsMenu from './SettingsMenu.svelte';
  import WorkspaceTabs from './WorkspaceTabs.svelte';
  import SearchPopover from '@/shared/components/SearchPopover.svelte';
  import ResizableColumns from './ResizableColumns.svelte';
  import Stats from '@/features/nodes/Stats.svelte';
  import NodePanel from '@/features/nodes/NodePanel.svelte';
  import TransferMenu from '@/features/transfers/TransferMenu.svelte';
  import UploadPicker from '@/features/transfers/UploadPicker.svelte';
  import { clipboard } from '@/features/files/clipboard.svelte';
  import FileWindow, {
    type OpenedFile,
  } from '@/features/files/editor/FileWindow.svelte';
  import { createTransfers } from '@/features/transfers/store.svelte';
  import type { FileEntry } from '@/features/files/api';
  import { fileKind } from '@/features/files/editor/file-kind';
  import Terminal from '@/features/terminal/TerminalWorkspace.svelte';
  import JoinDialog from '@/features/nodes/JoinDialog.svelte';
  import ConfirmDialog, {
    type Confirmation,
  } from '@/shared/components/ConfirmDialog.svelte';
  import NodeMenu from '@/features/nodes/NodeMenu.svelte';
  import RenameDialog from '@/features/nodes/RenameDialog.svelte';
  import NodeIcon from '@/features/nodes/NodeIcon.svelte';

  const transfers = createTransfers((message) => notify(message));
  let uploadPicker: UploadPicker;
  let openedFiles = $state<OpenedFile[]>([]),
    fileWindowVisible = $state(false),
    fileSequence = 0;
  let selectedFile = $state(0);
  let requestedFile = 0;
  function selectFile(file: OpenedFile) {
    requestedFile = file.id;
    if (file.kind !== 'text' || file.ready) {
      selectedFile = file.id;
      fileWindowVisible = true;
    }
  }
  function fileWithin(file: OpenedFile, directory: string) {
    const windows = file.node.info.os === 'windows';
    const path = windows ? file.entry.path.toLowerCase() : file.entry.path;
    const parent = windows ? directory.toLowerCase() : directory;
    return path === parent || path.startsWith(parent + (windows ? '\\' : '/'));
  }
  function openFile(node: Node, entry: FileEntry) {
    const existing = openedFiles.find(
      (file) =>
        file.node.id === node.id &&
        (node.info.os === 'windows'
          ? file.entry.path.toLowerCase() === entry.path.toLowerCase()
          : file.entry.path === entry.path),
    );
    if (existing) {
      selectFile(existing);
      return;
    }
    const file: OpenedFile = {
      id: ++fileSequence,
      node,
      entry: { ...entry },
      kind: fileKind(entry.name),
      ready: false,
      dirty: false,
      saving: false,
    };
    openedFiles = [...openedFiles, file];
    selectFile(file);
  }
  function discardFiles(files: OpenedFile[], action: () => void) {
    const dirty = files.filter((file) => file.dirty);
    if (dirty.length)
      requestConfirmation({
        title: 'closeFiles',
        description: 'confirmDiscardEdits',
        confirmLabel: 'discardEdits',
        detail: dirty
          .map((file) => file.node.name + ' · ' + file.entry.path)
          .join('\n'),
        danger: true,
        action,
      });
    else action();
  }
  function renamedFile(id: string, path: string, destination: string) {
    for (const file of openedFiles)
      if (file.node.id === id && fileWithin(file, path)) {
        file.entry.path = destination + file.entry.path.slice(path.length);
        file.entry.name = file.entry.path
          .split(file.node.info.os === 'windows' ? '\\' : '/')
          .at(-1)!;
      }
  }
  function removedFiles(id: string, paths: string[]) {
    openedFiles = openedFiles.filter((file) => {
      if (file.node.id !== id || !paths.some((path) => fileWithin(file, path)))
        return true;
      if (file.dirty) {
        file.missing = true;
        return true;
      }
      return false;
    });
  }
  $effect(() => {
    if (loggedIn) {
      untrack(() => {
        void transfers.connect().catch((error) => notify(error.message));
      });
      return () => transfers.close();
    }
  });
  type Tab = { id: string; name: string };
  let loggedIn = $state(false);
  let checking = $state(true);
  let token = $state('');
  let signingIn = $state(false);
  let nodes = $state<Node[]>([]);
  let tabs = $state<Tab[]>([]);
  let active = $state('');
  let joining = $state(false);
  let joinNode = $state<Node | undefined>();
  let renaming = $state<Node | null>(null);
  let notice = $state('');
  let confirmation = $state<Confirmation | null>(null);
  let search = $state('');
  let sidebarWidth = $state(
    Number(localStorage.getItem('probex.sidebar-width')) || 260,
  );
  let infoWidth = $state(
    Number(localStorage.getItem('probex.info-width')) || 360,
  );
  let viewportWidth = $state(innerWidth);
  let terminals = new Map<string, Terminal>();
  let refreshing = false;
  let dismissTimer: ReturnType<typeof setTimeout>;
  const online = $derived(
    nodes.filter((node) => node.status === 'online').length,
  );
  const offline = $derived(
    nodes.filter((node) => node.status === 'offline').length,
  );
  const visible = $derived(
    nodes.filter((node) =>
      `${node.name} ${node.info.hostname} ${node.info.os} ${node.info.arch}`
        .toLowerCase()
        .includes(search.toLowerCase()),
    ),
  );
  function notify(message: string) {
    notice = message;
    clearTimeout(dismissTimer);
    dismissTimer = setTimeout(() => (notice = ''), 5000);
  }
  function reset(keepEditor = false) {
    loggedIn = false;
    tabs = [];
    active = '';
    joining = false;
    joinNode = undefined;
    renaming = null;
    nodes = [];
    confirmation = null;
    search = '';
    clipboard.entries = [];
    if (!keepEditor) {
      openedFiles = [];
      selectedFile = 0;
      requestedFile = 0;
      fileWindowVisible = false;
    }
    notice = '';
  }
  function addNode() {
    joinNode = undefined;
    joining = true;
  }
  function renameNode(node: Node) {
    renaming = node;
  }
  function regenerateNode(node: Node) {
    joinNode = node;
    joining = true;
  }
  function reviewNode(node: Node, action: 'approve' | 'reject') {
    requestConfirmation({
      title: action,
      description: action === 'approve' ? 'confirmApprove' : 'confirmReject',
      confirmLabel: action,
      detail: node.name,
      danger: action === 'reject',
      action: async () => {
        await api(`nodes/${node.id}/${action}`, { method: 'POST', body: '{}' });
        await refresh();
      },
    });
  }
  function removeNode(node: Node) {
    if (node.join_mode === 'local') return;
    requestConfirmation({
      title: 'delete',
      description: openedFiles.some(
        (file) => file.node.id === node.id && file.dirty,
      )
        ? 'confirmDeleteNodeEdits'
        : 'confirmDelete',
      confirmLabel: 'delete',
      detail: [
        node.name,
        ...openedFiles
          .filter((file) => file.node.id === node.id && file.dirty)
          .map((file) => file.entry.path),
      ].join('\n'),
      danger: true,
      action: async () => {
        await api(`nodes/${node.id}`, { method: 'DELETE' });
        transfers.removeNode(node.id);
        openedFiles = openedFiles.filter((file) => file.node.id !== node.id);
        closeTab(node.id);
        await refresh();
      },
    });
  }
  async function refresh() {
    if (!loggedIn || refreshing) return;
    refreshing = true;
    try {
      nodes = await api<Node[]>('nodes');
      for (const id of new Set(transfers.tasks.map((task) => task.nodeID)))
        if (!nodes.some((node) => node.id === id)) transfers.removeNode(id);
      for (const task of transfers.tasks) {
        const node = nodes.find((node) => node.id === task.nodeID);
        if (node) task.nodeName = node.name;
      }
      for (const file of openedFiles) {
        const node = nodes.find((node) => node.id === file.node.id);
        if (node) {
          file.node = node;
          file.nodeMissing = false;
        } else {
          file.nodeMissing = true;
          file.node.online = false;
        }
      }
      for (const tab of [...tabs]) {
        const node = nodes.find((node) => node.id === tab.id);
        if (node) tab.name = node.name;
        else {
          transfers.removeNode(tab.id);
          closeTab(tab.id);
        }
      }
    } catch (error) {
      if (loggedIn) notify((error as Error).message);
    } finally {
      refreshing = false;
    }
  }
  async function login(event: SubmitEvent) {
    event.preventDefault();
    signingIn = true;
    try {
      await api('login', { method: 'POST', body: JSON.stringify({ token }) });
      token = '';
      loggedIn = true;
      notice = '';
      await refresh();
    } catch (error) {
      notify(
        error instanceof ApiError && error.status === 401
          ? t('loginError')
          : (error as Error).message,
      );
    } finally {
      signingIn = false;
    }
  }
  function requestConfirmation(request: Confirmation) {
    confirmation = request;
  }
  function logout() {
    requestConfirmation({
      title: 'logout',
      description: openedFiles.some((file) => file.dirty)
        ? 'confirmLogoutEdits'
        : 'confirmLogout',
      detail: openedFiles
        .filter((file) => file.dirty)
        .map((file) => file.node.name + ' · ' + file.entry.path)
        .join('\n'),
      confirmLabel: 'logout',
      action: async () => {
        await api('logout', { method: 'POST', body: '{}' });
        reset();
      },
    });
  }
  function openNode(node: Node) {
    if (!tabs.some((tab) => tab.id === node.id))
      tabs = [...tabs, { id: node.id, name: node.name }];
    active = node.id;
  }
  function closeTab(id: string) {
    const index = tabs.findIndex((tab) => tab.id === id);
    tabs = tabs.filter((tab) => tab.id !== id);
    if (active === id)
      active = tabs[Math.min(index, tabs.length - 1)]?.id || '';
    terminals.delete(id);
  }
  function requestCloseTabs(ids: string[]) {
    const close = () => {
      for (const id of ids) closeTab(id);
    };
    if (ids.some((id) => terminals.get(id)?.hasSession())) {
      requestConfirmation({
        title: 'closeTabs',
        description: 'confirmCloseTabs',
        confirmLabel: 'closeTabs',
        detail: tabs
          .filter((tab) => ids.includes(tab.id))
          .map((tab) => tab.name)
          .join(', '),
        action: close,
      });
    } else close();
  }
  $effect(() => {
    document.documentElement.dataset.theme = dark() ? 'dark' : 'light';
    document.documentElement.lang = language() === 'zh' ? 'zh-CN' : 'en';
    localStorage.setItem('probex.theme', preferences.theme);
    localStorage.setItem('probex.language', preferences.language);
  });
  onMount(() => {
    onUnauthorized(() => {
      if (loggedIn) {
        reset(true);
        notify(t('sessionExpired'));
      }
    });
    void api('session')
      .then(async () => {
        loggedIn = true;
        await refresh();
      })
      .catch((error) => {
        if (!(error instanceof ApiError && error.status === 401))
          notify((error as Error).message);
      })
      .finally(() => (checking = false));
    const timer = setInterval(() => void refresh(), 3000);
    const media = matchMedia('(prefers-color-scheme: dark)');
    const themeChange = () => (preferences.dark = media.matches);
    const languageChange = () =>
      (preferences.browserLanguage = navigator.language);
    media.addEventListener('change', themeChange);
    window.addEventListener('languagechange', languageChange);
    return () => {
      clearInterval(timer);
      clearTimeout(dismissTimer);
      media.removeEventListener('change', themeChange);
      window.removeEventListener('languagechange', languageChange);
    };
  });
</script>

<svelte:window bind:innerWidth={viewportWidth} />

{#snippet workspaceTools(labeled = false)}
  <div class="workspace-tools" class:tools-labeled={labeled}>
    <button
      class="icon-button workspace-tool editor-trigger"
      aria-label={t('fileWindow')}
      title={t('fileWindow')}
      onclick={() => (fileWindowVisible = true)}
    >
      <FileBraces size={16} />{#if labeled}<span>
          {t('editor')}
        </span>{/if}{#if openedFiles.some((file) => file.dirty)}<span
          class="workspace-count"
        >
          {openedFiles.filter((file) => file.dirty).length}
        </span>{/if}
    </button><TransferMenu
      manager={transfers}
      {labeled}
      onrequestconfirm={requestConfirmation}
    />
  </div>
{/snippet}

{#if notice}<div class="toast" role="status">
    <CircleDot size={16} /><span>{notice}</span><button
      class="icon-button"
      aria-label={t('close')}
      onclick={() => (notice = '')}
    >
      <X size={14} />
    </button>
  </div>{/if}

{#if checking}<main class="checking">
    <span class="loading-dot"></span><span>Probex</span>
  </main>
{:else if !loggedIn}
  <main class="login-screen">
    <div class="login-card">
      <div class="login-heading">
        <h1>Probex</h1>
        <SettingsMenu {loggedIn} onlogout={logout} />
      </div>
      <form onsubmit={login}>
        <div class="token-input">
          <KeyRound size={16} /><input
            id="token"
            aria-label={t('token')}
            type="password"
            bind:value={token}
            placeholder="••••••••••••"
            required
            autocomplete="current-password"
          />
        </div>
        <button class="primary-button" disabled={signingIn}>
          {t('login')}<ArrowRight size={16} />
        </button>
      </form>
      <p class="login-help">{t('loginHelp')}</p>
    </div>
    <span class="login-footer">PROBEX / INFRASTRUCTURE</span>
  </main>
{:else if tabs.length === 0}
  <main class="overview">
    <div class="overview-heading">
      <div class="overview-brand">
        <h1 class="wordmark">Probex</h1>
      </div>
      <SettingsMenu {loggedIn} onlogout={logout} />
    </div>
    <Stats total={nodes.length} {online} {offline} />
    <section class="nodes-card">
      <div class="nodes-card-heading">
        <label class="search-input">
          <Search size={15} /><input
            aria-label={t('searchNodes')}
            placeholder={t('searchNodes')}
            bind:value={search}
          />
        </label>
        {@render workspaceTools(true)}
        <div class="nodes-heading-actions">
          <button
            class="quiet-button refresh-button"
            aria-label={t('refresh')}
            title={t('refresh')}
            onclick={refresh}
          >
            <RotateCw size={15} /><span class="refresh-label">
              {t('refresh')}
            </span>
          </button><button class="primary-button" onclick={addNode}>
            <Plus size={14} />{t('addNode')}
          </button>
        </div>
      </div>
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>{t('name')}</th><th>{t('status')}</th><th>
                {t('system')}
              </th><th>{t('reported')}</th><th class="node-actions-cell">
                {t('actions')}
              </th>
            </tr>
          </thead><tbody>
            {#each visible as node (node.id)}<tr data-node-id={node.id}>
                <td>
                  <button
                    class="node-link"
                    title={node.name}
                    onclick={() => openNode(node)}
                  >
                    <span class="device-icon">
                      <NodeIcon os={node.info.os} size={17} fallback="server" />
                    </span><span class="node-label">
                      <span
                        class="node-name"
                        class:muted={node.status === 'offline'}
                      >
                        {node.name}
                      </span><small>
                        {node.info.hostname || '—'}
                      </small>
                    </span>
                  </button>
                </td><td>
                  <span
                    class="node-badge"
                    class:online={node.online}
                    data-status={node.status}
                  >
                    <span
                      class="status-dot"
                      class:online={node.online}
                      data-status={node.status}
                    ></span>{t(node.status)}
                  </span>
                </td><td class="system-cell">
                  <div>{node.info.platform?.trim() || node.info.os || '—'}</div>
                  <div>{node.info.arch || '—'}</div>
                </td><td class="report-cell">{date(node.last_seen, true)}</td>
                <td class="node-actions-cell">
                  <NodeMenu
                    {node}
                    onrename={renameNode}
                    onenrollment={regenerateNode}
                    onremove={removeNode}
                    onreview={reviewNode}
                  />
                </td>
              </tr>{/each}
          </tbody>
        </table>
      </div>
      {#if !visible.length}<div class="empty-state">
          <Server size={28} />
          <h3>{t(nodes.length ? 'noMatches' : 'noNodes')}</h3>
          <p>{t('noNodesHint')}</p>
        </div>{/if}
      <div class="table-footer">
        <span class="status-dot online"></span>{t('online')}
        {online} <span>·</span>
        {t('total')}
        {nodes.length}
      </div>
    </section>
  </main>
{:else}
  <main class="terminal-workspace">
    <ResizableColumns
      class="workspace-columns"
      bind:size={sidebarWidth}
      min={150}
      max={Math.min(480, viewportWidth * 0.45)}
      side="start"
      storageKey="probex.sidebar-width"
      label={t('resize')}
      resizerClass="sidebar-resize"
    >
      {#snippet first()}<aside class="sidebar">
          <div class="brand-area">
            <span class="wordmark">Probex</span>
            <SettingsMenu {loggedIn} onlogout={logout} />
          </div>
          <div class="sidebar-heading">
            <SearchPopover
              label={t('searchNodes')}
              bind:value={search}
              align="start"
              closeOnEnter
            />
            {@render workspaceTools()}
            <button
              class="icon-button"
              aria-label={t('refresh')}
              title={t('refresh')}
              onclick={refresh}
            >
              <RotateCw size={15} />
            </button><button
              class="icon-button"
              aria-label={t('addNode')}
              title={t('addNode')}
              onclick={addNode}
            >
              <Plus size={15} />
            </button>
          </div>
          <Stats total={nodes.length} {online} {offline} compact />
          <div class="sidebar-nodes">
            {#each visible as node (node.id)}<NodeMenu
                {node}
                context
                selected={active === node.id}
                onrename={renameNode}
                onenrollment={regenerateNode}
                onremove={removeNode}
                onreview={reviewNode}
              >
                <button
                  class="sidebar-node-name"
                  onclick={() => openNode(node)}
                >
                  <span
                    class="status-dot"
                    class:online={node.online}
                    data-status={node.status}
                  ></span><span class:muted={node.status === 'offline'}>
                    {node.name}
                  </span><span class="sr-only">
                    {t(node.status)}
                  </span>
                </button>
              </NodeMenu>{/each}
          </div>
        </aside>
      {/snippet}
      {#snippet second()}
        <section class="workspace-content" aria-label={t('workspace')}>
          <header class="workspace-tabs">
            <WorkspaceTabs
              bind:tabs
              {nodes}
              bind:active
              onclose={requestCloseTabs}
              onreload={(id) => terminals.get(id)?.reload()}
            />
          </header>
          <div class="terminal-panels">
            {#each tabs as tab (tab.id)}
              {@const node = nodes.find((node) => node.id === tab.id)!}
              <div
                class="terminal-panel"
                role="tabpanel"
                id={`panel-${tab.id}`}
                aria-labelledby={`tab-${tab.id}`}
                hidden={active !== tab.id}
              >
                <ResizableColumns
                  class="node-workspace"
                  bind:size={infoWidth}
                  min={240}
                  max={720}
                  minRemaining={200}
                  side="end"
                  storageKey="probex.info-width"
                  label={t('resizeInfo')}
                  resizerClass="info-resize"
                  active={active === tab.id}
                  stacked={viewportWidth <= 900}
                >
                  {#snippet first()}<div class="workspace-terminal">
                      <Terminal
                        nodeID={tab.id}
                        name={tab.name}
                        active={active === tab.id}
                        online={node.online}
                        onerror={notify}
                        onrequestconfirm={requestConfirmation}
                        bind:this={
                          () => terminals.get(tab.id),
                          (value) => {
                            if (value) terminals.set(tab.id, value);
                          }
                        }
                      />
                    </div>
                  {/snippet}
                  {#snippet second()}
                    <NodePanel
                      {node}
                      active={active === tab.id}
                      historyPaused={confirmation?.title === 'delete' ||
                        confirmation?.title === 'clearHistory'}
                      manager={transfers}
                      onupload={(target, kind) =>
                        uploadPicker.choose(node, target, kind)}
                      onopen={(entry) => openFile(node, entry)}
                      onrenamed={(path, destination) =>
                        renamedFile(node.id, path, destination)}
                      onremoved={(paths) => removedFiles(node.id, paths)}
                      onerror={notify}
                      onrename={() => renameNode(node)}
                      onreview={reviewNode}
                      onremove={() => removeNode(node)}
                      onenrollment={() => regenerateNode(node)}
                      onrequestconfirm={requestConfirmation}
                    />
                  {/snippet}
                </ResizableColumns>
              </div>{/each}
          </div>
        </section>
      {/snippet}
    </ResizableColumns>
  </main>
{/if}

{#if joining}<JoinDialog
    node={joinNode}
    onclose={() => {
      joining = false;
      joinNode = undefined;
    }}
    oncreated={() => void refresh()}
    onerror={notify}
  />{/if}

{#if renaming}<RenameDialog
    node={renaming}
    onclose={() => (renaming = null)}
    onrenamed={(updated) => {
      nodes = nodes.map((node) => (node.id === updated.id ? updated : node));
      tabs = tabs.map((tab) =>
        tab.id === updated.id ? { ...tab, name: updated.name } : tab,
      );
    }}
  />{/if}

{#if confirmation}<ConfirmDialog
    request={confirmation}
    onclose={() => (confirmation = null)}
  />{/if}

<UploadPicker bind:this={uploadPicker} manager={transfers} />
{#if loggedIn || openedFiles.length}<FileWindow
    bind:files={openedFiles}
    visible={loggedIn && fileWindowVisible}
    manager={transfers}
    onminimize={() => {
      requestedFile = 0;
      fileWindowVisible = false;
    }}
    selected={selectedFile}
    onselect={(id) => selectFile(openedFiles.find((file) => file.id === id)!)}
    onready={(file) => {
      if (
        requestedFile === file.id &&
        openedFiles.some((open) => open.id === file.id)
      )
        selectFile(file);
    }}
    onrequestconfirm={requestConfirmation}
    onerror={notify}
    onremove={(id) =>
      discardFiles(
        openedFiles.filter((file) => file.id === id),
        () => (openedFiles = openedFiles.filter((file) => file.id !== id)),
      )}
    onclose={() =>
      discardFiles(openedFiles, () => {
        openedFiles = [];
        requestedFile = 0;
        fileWindowVisible = false;
      })}
  />{/if}
