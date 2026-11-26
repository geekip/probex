<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Popover, ContextMenu, DropdownMenu } from 'bits-ui';
  import copyText from 'clipboard-copy';
  import type { Axis } from '@/features/terminal/layout';
  import {
    ArrowDown,
    ArrowUp,
    RotateCw,
    Search,
    OctagonX,
    BrushCleaning,
    Columns2,
    Rows2,
    SquareTerminal,
    PanelsTopLeft,
    ChevronRight,
    Copy,
    ClipboardPaste,
    X,
  } from '@lucide/svelte';
  import { Terminal as XtermTerminal } from '@xterm/xterm';
  import '@xterm/xterm/css/xterm.css';
  import { FitAddon } from '@xterm/addon-fit';
  import { SearchAddon } from '@xterm/addon-search';
  import { WebLinksAddon } from '@xterm/addon-web-links';
  import { dark, t } from '@/shared/settings.svelte';
  import type { Confirmation } from '@/shared/components/ConfirmDialog.svelte';

  let {
    nodeID,
    name,
    active,
    online,
    onerror,
    onrequestconfirm,
    focused,
    splitDisabled,
    closeVisible,
    onactivate,
    onsplit,
    onclose,
  }: {
    nodeID: string;
    name: string;
    active: boolean;
    online: boolean;
    onerror: (message: string) => void;
    onrequestconfirm: (request: Confirmation) => void;
    focused: boolean;
    splitDisabled: boolean;
    closeVisible: boolean;
    onactivate: () => void;
    onsplit: (axis: Axis) => void;
    onclose: () => void;
  } = $props();
  let container: HTMLDivElement;
  let pane: HTMLElement;
  let searchInput = $state<HTMLInputElement>();
  let terminal: XtermTerminal | undefined;
  let fit: FitAddon;
  let searchAddon: SearchAddon;
  let searchOpen = $state(false);
  let splitMenuOpen = $state(false);
  let connectionMenuOpen = $state(false);
  let contextMenuOpen = $state(false);
  let query = $state('');
  let searchFound = $state<boolean | undefined>();
  let socket: WebSocket | undefined;
  const resizeInterval = 100;
  let resizeTimer: ReturnType<typeof setTimeout> | undefined;
  let sentSize: { cols: number; rows: number } | undefined;
  let status = $state<'connecting' | 'connected' | 'disconnected'>(
    'disconnected',
  );
  let reloadPending = false;
  let disconnectPending = false;
  let hasSelection = $state(false);
  async function copySelection() {
    try {
      await copyText(terminal?.getSelection() || '');
    } catch (error) {
      onerror(`${t('clipboardError')}: ${(error as Error).message}`);
    }
  }
  async function paste() {
    if (!window.isSecureContext || !navigator.clipboard?.readText) {
      onerror(t('pasteKeyboardHint'));
      return;
    }
    try {
      terminal?.paste(await navigator.clipboard.readText());
      terminal?.focus();
    } catch (error) {
      onerror(`${t('pasteDenied')}: ${(error as Error).message}`);
    }
  }
  function clear() {
    terminal?.clear();
    terminal?.focus();
  }
  function split(axis: Axis) {
    onactivate();
    onsplit(axis);
  }

  let ended = false;
  const palette = () =>
    dark()
      ? {
          background: '#181818',
          foreground: '#e5e5e5',
          cursor: '#ededed',
          selectionBackground: '#3b3b3b',
          black: '#242424',
        }
      : {
          background: '#ffffff',
          foreground: '#262626',
          cursor: '#171717',
          selectionBackground: '#d4d4d4',
          black: '#222222',
        };
  function send(value: object) {
    if (socket?.readyState === WebSocket.OPEN)
      socket.send(JSON.stringify(value));
  }
  function resize() {
    if (active && container?.clientWidth && container?.clientHeight) fit?.fit();
  }
  function clearResizeTimer() {
    clearTimeout(resizeTimer);
    resizeTimer = undefined;
  }
  function sendSize() {
    clearResizeTimer();
    if (
      !terminal ||
      socket?.readyState !== WebSocket.OPEN ||
      reloadPending ||
      disconnectPending
    )
      return;
    const { cols, rows } = terminal;
    if (sentSize?.cols === cols && sentSize.rows === rows) return;
    send({ type: 'resize', cols, rows });
    sentSize = { cols, rows };
  }
  async function openSearch() {
    searchOpen = true;
    await tick();
    searchInput?.focus();
    searchInput?.select();
  }
  async function closeSearch() {
    searchOpen = false;
    await tick();
    terminal?.focus();
  }
  function find(previous = false, incremental = false) {
    if (!query) {
      searchAddon.clearDecorations();
      terminal?.clearSelection();
      searchFound = undefined;
      return;
    }
    searchFound = previous
      ? searchAddon.findPrevious(query)
      : searchAddon.findNext(query, { incremental });
  }
  function connect() {
    if (!online) {
      status = 'disconnected';
      onerror(t('nodeOffline'));
      return;
    }
    status = 'connecting';
    sentSize = undefined;
    disconnectPending = false;
    ended = false;
    const connection = new WebSocket(
      `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/nodes/${nodeID}/terminal`,
    );
    socket = connection;
    connection.onopen = () => {
      status = 'connected';
      if (reloadPending || disconnectPending) {
        send({ type: 'close' });
        return;
      }
      resize();
      sendSize();
      if (focused) terminal?.focus();
    };
    connection.onmessage = (event) => {
      const message = JSON.parse(event.data);
      if (message.type === 'term_output' && message.data)
        terminal?.write(
          Uint8Array.from(atob(message.data), (c) => c.charCodeAt(0)),
        );
      if (message.type === 'term_exit') {
        ended = true;
        terminal?.writeln(
          `\r\n[${t('terminalEnded')}${message.error ? ': ' + message.error : ''}]`,
        );
      }
    };
    connection.onerror = () => {
      onerror(t('terminalError'));
    };
    connection.onclose = () => {
      clearResizeTimer();
      status = 'disconnected';
      if (reloadPending) {
        reloadPending = false;
        terminal?.write('\x1bc');
        connect();
      } else if (!ended) terminal?.writeln(`\r\n[${t('disconnected')}]`);
    };
  }
  export function hasSession() {
    return status === 'connecting' || status === 'connected';
  }
  export function reload() {
    if (hasSession()) {
      onrequestconfirm({
        title: 'reconnect',
        description: 'confirmReloadTerminal',
        confirmLabel: 'reconnect',
        detail: name,
        action: restart,
      });
    } else restart();
  }
  function restart() {
    if (socket?.readyState === WebSocket.OPEN) {
      reloadPending = true;
      send({ type: 'close' });
    } else if (socket?.readyState === WebSocket.CONNECTING) {
      reloadPending = true;
    } else if (status !== 'connecting') {
      terminal?.write('\x1bc');
      connect();
    }
  }
  function stop() {
    onrequestconfirm({
      title: 'disconnectTerminal',
      description: 'confirmStopTerminal',
      confirmLabel: 'disconnectTerminal',
      detail: name,
      action: () => {
        reloadPending = false;
        disconnectPending = true;
        send({ type: 'close' });
      },
    });
  }
  $effect(() => {
    if (!searchOpen) {
      searchFound = undefined;
      searchAddon?.clearDecorations();
      terminal?.clearSelection();
    }
  });
  $effect(() => {
    const theme = palette();
    if (terminal) terminal.options.theme = theme;
  });
  $effect(() => {
    const visible = active,
      focus =
        focused &&
        !searchOpen &&
        !splitMenuOpen &&
        !connectionMenuOpen &&
        !contextMenuOpen;
    if (visible) {
      void tick().then(() => {
        resize();
        if (focus) terminal?.focus();
      });
    }
    if (!visible || !focused) searchOpen = false;
  });
  onMount(() => {
    terminal = new XtermTerminal({
      cursorBlink: true,
      fontSize: 12,
      fontFamily: '"SFMono-Regular", Consolas, "Liberation Mono", monospace',
      scrollback: 3000,
      theme: palette(),
    });
    fit = new FitAddon();
    searchAddon = new SearchAddon();
    terminal.loadAddon(fit);
    terminal.loadAddon(searchAddon);
    terminal.loadAddon(new WebLinksAddon());
    terminal.open(container);
    resize();
    const keydown = (event: KeyboardEvent) => {
      if (
        active &&
        (event.ctrlKey || event.metaKey) &&
        event.key.toLowerCase() === 'f'
      ) {
        event.preventDefault();
        event.stopPropagation();
        void openSearch();
      }
    };
    const terminalPane = pane;
    terminalPane.addEventListener('keydown', keydown, true);
    const data = terminal.onData((text) => {
      if ((socket?.bufferedAmount || 0) > 65536) {
        onerror(t('error'));
        socket?.close();
        return;
      }
      const bytes = new TextEncoder().encode(text);
      for (let i = 0; i < bytes.length; i += 8192)
        send({
          type: 'input',
          data: btoa(String.fromCharCode(...bytes.subarray(i, i + 8192))),
        });
    });
    const selection = terminal.onSelectionChange(
      () => (hasSelection = !!terminal?.hasSelection()),
    );
    const size = terminal.onResize(() => {
      if (resizeTimer === undefined && socket?.readyState === WebSocket.OPEN)
        resizeTimer = setTimeout(sendSize, resizeInterval);
    });
    const observer = new ResizeObserver(resize);
    observer.observe(container);
    if (online) connect();
    else terminal.writeln(`[${t('nodeOffline')}]`);
    return () => {
      reloadPending = false;
      clearResizeTimer();
      terminalPane.removeEventListener('keydown', keydown, true);
      observer.disconnect();
      data.dispose();
      selection.dispose();
      size.dispose();
      if (socket) {
        socket.onclose = null;
        socket.onmessage = null;
        socket.onerror = null;
        socket.onopen = null;
        socket.close();
      }
      terminal?.dispose();
    };
  });
</script>

{#snippet splitItems(Item: typeof DropdownMenu.Item | typeof ContextMenu.Item)}
  <Item
    class="menu-item"
    disabled={splitDisabled || !online}
    onSelect={() => split('horizontal')}
  >
    <Columns2 size={14} />{t('splitHorizontal')}
  </Item>
  <Item
    class="menu-item"
    disabled={splitDisabled || !online}
    onSelect={() => split('vertical')}
  >
    <Rows2 size={14} />{t('splitVertical')}
  </Item>
{/snippet}
{#snippet connectionItems(
  Item: typeof DropdownMenu.Item | typeof ContextMenu.Item,
)}
  <Item class="menu-item" disabled={!online} onSelect={reload}>
    <RotateCw size={14} />{t('reconnect')}
  </Item>
  <Item class="menu-item" disabled={!hasSession()} onSelect={stop}>
    <OctagonX size={14} />{t('disconnectTerminal')}
  </Item>
{/snippet}

<Popover.Root bind:open={searchOpen}>
  <section
    class="terminal-pane"
    bind:this={pane}
    aria-label={`${name} ${t('terminalHint')}`}
    onpointerdown={() => onactivate()}
    onfocusin={() => onactivate()}
  >
    <div class="terminal-toolbar">
      <span class="connection-state" aria-label={t(status)} title={t(status)}>
        <span class:online={status === 'connected'} class="status-dot"></span>
        <span class="connection-label">{t(status)}</span>
      </span>
      <div class="toolbar-actions">
        <Popover.Trigger
          class="icon-button"
          data-search-active={String(!!query)}
          aria-label={t('terminalSearch')}
          title={t('terminalSearch')}
        >
          <Search size={15} />
        </Popover.Trigger>
        <button
          class="icon-button"
          aria-label={t('clearTerminal')}
          title={t('clearTerminal')}
          onclick={clear}
        >
          <BrushCleaning size={15} />
        </button>
        <DropdownMenu.Root bind:open={splitMenuOpen}>
          <DropdownMenu.Trigger
            class="icon-button"
            aria-label={t('splitTerminal')}
            title={t('splitTerminal')}
            disabled={splitDisabled || !online}
          >
            <PanelsTopLeft size={15} />
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content class="menu" align="end" sideOffset={6}>
              {@render splitItems(DropdownMenu.Item)}
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
        <DropdownMenu.Root bind:open={connectionMenuOpen}>
          <DropdownMenu.Trigger
            class="icon-button"
            aria-label={t('terminalConnection')}
            title={t('terminalConnection')}
          >
            <SquareTerminal size={15} />
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content class="menu" align="end" sideOffset={6}>
              {@render connectionItems(DropdownMenu.Item)}
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
        {#if closeVisible}
          <button
            class="icon-button"
            aria-label={t('closePane')}
            title={t('closePane')}
            onclick={() => onclose()}
          >
            <X size={15} />
          </button>{/if}
      </div>
    </div>
    <ContextMenu.Root bind:open={contextMenuOpen}>
      <ContextMenu.Trigger
        class="terminal-body"
        oncontextmenu={() => onactivate()}
      >
        <div class="terminal-container" bind:this={container}></div>
      </ContextMenu.Trigger>
      <ContextMenu.Portal>
        <ContextMenu.Content class="menu">
          <ContextMenu.Item
            class="menu-item"
            disabled={!hasSelection}
            onSelect={copySelection}
          >
            <Copy size={14} />{t('copySelection')}
          </ContextMenu.Item>
          <ContextMenu.Item
            class="menu-item"
            disabled={status !== 'connected'}
            onSelect={paste}
          >
            <ClipboardPaste size={14} />{t('paste')}
          </ContextMenu.Item>
          <ContextMenu.Item class="menu-item" onSelect={clear}>
            <BrushCleaning size={14} />{t('clearTerminal')}
          </ContextMenu.Item>
          <ContextMenu.Separator class="menu-separator" />
          <ContextMenu.Sub>
            <ContextMenu.SubTrigger
              class="menu-item"
              disabled={splitDisabled || !online}
            >
              <PanelsTopLeft size={14} />{t('splitTerminal')}<ChevronRight
                class="menu-arrow"
                size={14}
              />
            </ContextMenu.SubTrigger>
            <ContextMenu.SubContent class="menu" sideOffset={4}>
              {@render splitItems(ContextMenu.Item)}
            </ContextMenu.SubContent>
          </ContextMenu.Sub>
          <ContextMenu.Sub>
            <ContextMenu.SubTrigger class="menu-item">
              <SquareTerminal size={14} />{t('terminalConnection')}<ChevronRight
                class="menu-arrow"
                size={14}
              />
            </ContextMenu.SubTrigger>
            <ContextMenu.SubContent class="menu" sideOffset={4}>
              {@render connectionItems(ContextMenu.Item)}
            </ContextMenu.SubContent>
          </ContextMenu.Sub>
        </ContextMenu.Content>
      </ContextMenu.Portal>
    </ContextMenu.Root>
  </section>
  <Popover.Portal>
    <Popover.Content
      class="terminal-search"
      role="dialog"
      aria-label={t('terminalSearch')}
      side="bottom"
      align="end"
      sideOffset={8}
      strategy="fixed"
      collisionPadding={8}
      trapFocus={false}
      onCloseAutoFocus={(event) => event.preventDefault()}
      onEscapeKeydown={(event) => {
        event.preventDefault();
        void closeSearch();
      }}
    >
      <input
        type="search"
        bind:this={searchInput}
        bind:value={query}
        aria-label={t('findInTerminal')}
        placeholder={t('findInTerminal')}
        oninput={() => find(false, true)}
        onkeydown={(event) => {
          if (event.key === 'Enter') {
            event.preventDefault();
            find(event.shiftKey);
          }
          if (
            (event.ctrlKey || event.metaKey) &&
            event.key.toLowerCase() === 'f'
          ) {
            event.preventDefault();
            searchInput?.select();
          }
        }}
      />
      {#if searchFound === false}<span class="search-empty" role="status">
          {t('searchNoResults')}
        </span>{/if}
      <button
        class="icon-button"
        aria-label={t('searchPrevious')}
        title={t('searchPrevious')}
        disabled={!query}
        onclick={() => find(true)}
      >
        <ArrowUp size={15} />
      </button>
      <button
        class="icon-button"
        aria-label={t('searchNext')}
        title={t('searchNext')}
        disabled={!query}
        onclick={() => find()}
      >
        <ArrowDown size={15} />
      </button>
      <button
        class="icon-button"
        aria-label={t('closeSearch')}
        title={t('closeSearch')}
        onclick={closeSearch}
      >
        <X size={15} />
      </button>
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>
