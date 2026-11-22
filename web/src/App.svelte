<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { ContextMenu, DropdownMenu } from 'bits-ui';
  import {
    MoreHorizontal,
    Menu,
    Check,
    ChevronRight,
    Sun,
    Moon,
    Monitor,
    Languages,
    LogOut,
    X,
    RotateCw,
    Search,
    Server,
    ArrowRight,
    KeyRound,
    CircleDot,
    Plus,
  } from '@lucide/svelte';
  import { api, ApiError, onUnauthorized, type Node } from './api';
  import {
    preferences,
    dark,
    language,
    t,
    date,
    type Preference,
    type Language,
  } from './settings.svelte';
  import Stats from './Stats.svelte';
  import NodeInfo from './NodeInfo.svelte';
  import Terminal from './Terminal.svelte';
  import JoinDialog from './JoinDialog.svelte';
  import ConfirmDialog, { type Confirmation } from './ConfirmDialog.svelte';
  import NodeMenu from './NodeMenu.svelte';
  import RenameDialog from './RenameDialog.svelte';
  import NodeIcon from './NodeIcon.svelte';

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
  let tabStrip = $state<HTMLDivElement>();
  let tabButtons = new Map<string, HTMLButtonElement>();
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
  function reset() {
    loggedIn = false;
    tabs = [];
    active = '';
    joining = false;
    joinNode = undefined;
    renaming = null;
    nodes = [];
    confirmation = null;
    search = '';
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
    requestConfirmation({
      title: 'delete',
      description: 'confirmDelete',
      confirmLabel: 'delete',
      detail: node.name,
      danger: true,
      action: async () => {
        await api(`nodes/${node.id}`, { method: 'DELETE' });
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
      for (const tab of [...tabs]) {
        const node = nodes.find((node) => node.id === tab.id);
        if (node) tab.name = node.name;
        else closeTab(tab.id);
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
      description: 'confirmLogout',
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
    tabButtons.delete(id);
  }
  function closeOthers(id: string) {
    requestCloseTabs(tabs.filter((tab) => tab.id !== id).map((tab) => tab.id));
  }
  function closeRight(id: string) {
    requestCloseTabs(
      tabs
        .slice(tabs.findIndex((tab) => tab.id === id) + 1)
        .map((tab) => tab.id),
    );
  }
  function closeAll() {
    requestCloseTabs(tabs.map((tab) => tab.id));
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
  function scrollTabs(event: WheelEvent) {
    if (!tabStrip || tabStrip.scrollWidth <= tabStrip.clientWidth) return;
    event.preventDefault();
    tabStrip.scrollLeft += event.deltaX || event.deltaY;
  }
  function tabKeys(event: KeyboardEvent, id: string) {
    const index = tabs.findIndex((tab) => tab.id === id);
    let next: string | undefined;
    if (event.key === 'ArrowRight') next = tabs[(index + 1) % tabs.length]?.id;
    if (event.key === 'ArrowLeft')
      next = tabs[(index - 1 + tabs.length) % tabs.length]?.id;
    if (event.key === 'Home') next = tabs[0]?.id;
    if (event.key === 'End') next = tabs.at(-1)?.id;
    if (next) {
      event.preventDefault();
      active = next;
      tabButtons.get(next)?.focus();
    }
  }
  function resizeSidebar(event: PointerEvent) {
    const handle = event.currentTarget as HTMLElement;
    handle.setPointerCapture(event.pointerId);
    const startX = event.clientX,
      startWidth = handle.previousElementSibling!.getBoundingClientRect().width;
    const move = (next: PointerEvent) =>
      (sidebarWidth = Math.round(
        Math.max(
          150,
          Math.min(
            Math.min(480, innerWidth * 0.45),
            startWidth + next.clientX - startX,
          ),
        ),
      ));
    const finish = () => {
      localStorage.setItem('probex.sidebar-width', String(sidebarWidth));
      handle.removeEventListener('pointermove', move);
      handle.removeEventListener('pointerup', finish);
      handle.removeEventListener('lostpointercapture', finish);
    };
    handle.addEventListener('pointermove', move);
    handle.addEventListener('pointerup', finish);
    handle.addEventListener('lostpointercapture', finish);
  }
  function resizeInfo(event: PointerEvent) {
    const handle = event.currentTarget as HTMLElement;
    handle.setPointerCapture(event.pointerId);
    const startX = event.clientX,
      startWidth = handle.nextElementSibling!.getBoundingClientRect().width;
    const maximum = Math.max(
      240,
      Math.min(720, handle.parentElement!.clientWidth - 200),
    );
    const move = (next: PointerEvent) =>
      (infoWidth = Math.round(
        Math.max(240, Math.min(maximum, startWidth + startX - next.clientX)),
      ));
    const finish = () => {
      localStorage.setItem('probex.info-width', String(infoWidth));
      handle.removeEventListener('pointermove', move);
      handle.removeEventListener('pointerup', finish);
      handle.removeEventListener('lostpointercapture', finish);
    };
    handle.addEventListener('pointermove', move);
    handle.addEventListener('pointerup', finish);
    handle.addEventListener('lostpointercapture', finish);
  }
  $effect(() => {
    document.documentElement.dataset.theme = dark() ? 'dark' : 'light';
    document.documentElement.lang = language() === 'zh' ? 'zh-CN' : 'en';
    localStorage.setItem('probex.theme', preferences.theme);
    localStorage.setItem('probex.language', preferences.language);
  });
  $effect(() => {
    if (active)
      void tick().then(() =>
        tabButtons
          .get(active)
          ?.scrollIntoView({ block: 'nearest', inline: 'nearest' }),
      );
  });
  $effect(() => {
    if (!tabStrip) return;
    const observer = new ResizeObserver(() =>
      tabButtons
        .get(active)
        ?.scrollIntoView({ block: 'nearest', inline: 'nearest' }),
    );
    observer.observe(tabStrip);
    return () => observer.disconnect();
  });
  onMount(() => {
    onUnauthorized(() => {
      if (loggedIn) {
        reset();
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

{#snippet workspaceMenu()}
  <DropdownMenu.Root>
    <DropdownMenu.Trigger
      class="icon-button workspace-menu"
      aria-label={t('settings')}
      title={t('settings')}
    >
      <Menu size={19} />
    </DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <DropdownMenu.Content class="menu" sideOffset={10} align="end">
        <DropdownMenu.Sub>
          <DropdownMenu.SubTrigger class="menu-item">
            <Sun size={15} />{t('theme')}<ChevronRight
              size={14}
              class="menu-arrow"
            />
          </DropdownMenu.SubTrigger>
          <DropdownMenu.Portal>
            <DropdownMenu.SubContent class="menu" sideOffset={6}>
              <DropdownMenu.RadioGroup
                value={preferences.theme}
                onValueChange={(value) =>
                  (preferences.theme = value as Preference)}
              >
                {#each ['auto', 'light', 'dark'] as value}<DropdownMenu.RadioItem
                    class="menu-item"
                    {value}
                  >
                    <span class="check-slot">
                      {#if preferences.theme === value}<Check size={14} />{/if}
                    </span>{#if value === 'auto'}<Monitor
                        size={15}
                      />{:else if value === 'light'}<Sun
                        size={15}
                      />{:else}<Moon size={15} />{/if}{t(value as Preference)}
                  </DropdownMenu.RadioItem>{/each}
              </DropdownMenu.RadioGroup>
            </DropdownMenu.SubContent>
          </DropdownMenu.Portal>
        </DropdownMenu.Sub>
        <DropdownMenu.Sub>
          <DropdownMenu.SubTrigger class="menu-item">
            <Languages size={15} />{t('language')}<ChevronRight
              size={14}
              class="menu-arrow"
            />
          </DropdownMenu.SubTrigger>
          <DropdownMenu.Portal>
            <DropdownMenu.SubContent class="menu" sideOffset={6}>
              <DropdownMenu.RadioGroup
                value={preferences.language}
                onValueChange={(value) =>
                  (preferences.language = value as Language)}
              >
                {#each [{ value: 'auto', label: t('auto') }, { value: 'zh', label: '中文' }, { value: 'en', label: 'English' }] as item}<DropdownMenu.RadioItem
                    class="menu-item"
                    value={item.value}
                  >
                    <span class="check-slot">
                      {#if preferences.language === item.value}<Check
                          size={14}
                        />{/if}
                    </span>{item.label}
                  </DropdownMenu.RadioItem>{/each}
              </DropdownMenu.RadioGroup>
            </DropdownMenu.SubContent>
          </DropdownMenu.Portal>
        </DropdownMenu.Sub>
        {#if loggedIn}<DropdownMenu.Separator
            class="menu-separator"
          /><DropdownMenu.Item class="menu-item" onSelect={logout}>
            <LogOut size={15} />{t('logout')}
          </DropdownMenu.Item>{/if}
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
{/snippet}

{#snippet workspaceTabs()}
  <div
    class="tabs-scroll"
    bind:this={tabStrip}
    onwheel={scrollTabs}
    role="tablist"
    aria-label={t('terminalHint')}
  >
    {#each tabs as tab (tab.id)}
      <ContextMenu.Root>
        <ContextMenu.Trigger
          class={`tab-wrap ${active === tab.id ? 'active' : ''}`}
        >
          <button
            class="terminal-tab"
            role="tab"
            id={`tab-${tab.id}`}
            aria-selected={active === tab.id}
            aria-controls={`panel-${tab.id}`}
            tabindex={active === tab.id ? 0 : -1}
            onclick={() => (active = tab.id)}
            onkeydown={(event) => tabKeys(event, tab.id)}
            bind:this={
              () => tabButtons.get(tab.id),
              (value) => {
                if (value) tabButtons.set(tab.id, value);
              }
            }
          >
            <NodeIcon os={nodes.find((node) => node.id === tab.id)?.info.os} />
            <span>{tab.name}</span>
          </button>
          <button
            class="tab-close"
            aria-label={`${t('close')} ${tab.name}`}
            onclick={() => requestCloseTabs([tab.id])}
          >
            <X size={13} />
          </button>
        </ContextMenu.Trigger><ContextMenu.Portal>
          <ContextMenu.Content class="menu">
            <ContextMenu.Item
              class="menu-item"
              onSelect={() => terminals.get(tab.id)?.reload()}
            >
              <RotateCw size={14} />{t('reload')}
            </ContextMenu.Item><ContextMenu.Separator class="menu-separator" />
            <ContextMenu.Item
              class="menu-item"
              onSelect={() => requestCloseTabs([tab.id])}
            >
              {t('closeCurrent')}
            </ContextMenu.Item>
            <ContextMenu.Item
              class="menu-item"
              disabled={tabs.at(-1)?.id === tab.id}
              onSelect={() => closeRight(tab.id)}
            >
              {t('closeRight')}
            </ContextMenu.Item>
            <ContextMenu.Item
              class="menu-item"
              disabled={tabs.length === 1}
              onSelect={() => closeOthers(tab.id)}
            >
              {t('closeOthers')}
            </ContextMenu.Item>
          </ContextMenu.Content>
        </ContextMenu.Portal>
      </ContextMenu.Root>
    {/each}
  </div>
  <DropdownMenu.Root>
    <DropdownMenu.Trigger
      class="icon-button tabs-overflow"
      aria-label={t('tabMenu')}
    >
      <MoreHorizontal size={18} />
    </DropdownMenu.Trigger><DropdownMenu.Portal>
      <DropdownMenu.Content class="menu" align="end" sideOffset={10}>
        <DropdownMenu.Item
          class="menu-item"
          onSelect={() => requestCloseTabs([active])}
        >
          {t('closeCurrent')}
        </DropdownMenu.Item><DropdownMenu.Item
          class="menu-item"
          onSelect={closeAll}
        >
          {t('closeAll')}
        </DropdownMenu.Item>
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
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
        {@render workspaceMenu()}
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
      <h1 class="wordmark" aria-label="Probex">Probex<span>.</span></h1>
      {@render workspaceMenu()}
    </div>
    <Stats total={nodes.length} {online} {offline} />
    <section class="nodes-card">
      <div class="nodes-card-heading">
        <label class="search-input">
          <Search size={15} /><input
            aria-label={t('search')}
            placeholder={t('search')}
            bind:value={search}
          />
        </label>
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
                      <span class="node-name">{node.name}</span><small>
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
                  {node.info.platform?.trim() || node.info.os || '—'}<span
                    class="arch-tag"
                  >
                    {node.info.arch || '—'}
                  </span>
                </td><td class="report-cell">{date(node.last_seen)}</td>
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
  <main
    class="terminal-workspace"
    style={`--sidebar-width:${sidebarWidth}px;--info-width:${infoWidth}px`}
  >
    <aside class="sidebar">
      <div class="brand-area">
        <span class="wordmark">Probex<span>.</span></span>
        {@render workspaceMenu()}
      </div>
      <div class="sidebar-heading">
        <label class="search-input sidebar-search">
          <Search size={15} /><input
            aria-label={t('search')}
            placeholder={t('search')}
            bind:value={search}
          />
        </label>
        <div class="sidebar-actions">
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
            onclick={addNode}
          >
            <Plus size={15} />
          </button>
        </div>
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
            <button class="sidebar-node-name" onclick={() => openNode(node)}>
              <span
                class="status-dot"
                class:online={node.online}
                data-status={node.status}
              ></span><span>
                {node.name}
              </span><span class="sr-only">
                {t(node.status)}
              </span>
            </button>
          </NodeMenu>{/each}
      </div>
    </aside>
    <button
      class="sidebar-resize"
      aria-label={t('resize')}
      onpointerdown={resizeSidebar}
      onkeydown={(event) => {
        if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
          event.preventDefault();
          sidebarWidth = Math.max(
            180,
            Math.min(
              480,
              sidebarWidth + (event.key === 'ArrowLeft' ? -16 : 16),
            ),
          );
          localStorage.setItem('probex.sidebar-width', String(sidebarWidth));
        }
      }}
    ></button>
    <section class="workspace-content" aria-label={t('workspace')}>
      <header class="workspace-tabs">{@render workspaceTabs()}</header>
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
            <div class="node-workspace">
              <div class="workspace-terminal">
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
              <button
                class="info-resize"
                aria-label={t('resizeInfo')}
                onpointerdown={resizeInfo}
                onkeydown={(event) => {
                  if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
                    event.preventDefault();
                    const maximum = Math.max(
                      240,
                      Math.min(
                        720,
                        event.currentTarget.parentElement!.clientWidth - 200,
                      ),
                    );
                    infoWidth = Math.max(
                      240,
                      Math.min(
                        maximum,
                        infoWidth + (event.key === 'ArrowLeft' ? 16 : -16),
                      ),
                    );
                    localStorage.setItem(
                      'probex.info-width',
                      String(infoWidth),
                    );
                  }
                }}
              ></button>
              <NodeInfo
                {node}
                active={active === tab.id &&
                  confirmation?.title !== 'delete' &&
                  confirmation?.title !== 'clearHistory'}
                onerror={notify}
                onrename={() => renameNode(node)}
                onreview={reviewNode}
                onremove={() => removeNode(node)}
                onenrollment={() => regenerateNode(node)}
                onrequestconfirm={requestConfirmation}
              />
            </div>
          </div>{/each}
      </div>
    </section>
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
