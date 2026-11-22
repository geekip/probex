<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Popover } from 'bits-ui';
  import {
    ArrowDown,
    ArrowUp,
    RotateCw,
    Search,
    Square,
    X,
  } from '@lucide/svelte';
  import { Terminal as XtermTerminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import { SearchAddon } from '@xterm/addon-search';
  import { WebLinksAddon } from '@xterm/addon-web-links';
  import { dark, t } from './settings.svelte';
  import type { Confirmation } from './ConfirmDialog.svelte';

  let {
    nodeID,
    name,
    active,
    online,
    onerror,
    onrequestconfirm,
  }: {
    nodeID: string;
    name: string;
    active: boolean;
    online: boolean;
    onerror: (message: string) => void;
    onrequestconfirm: (request: Confirmation) => void;
  } = $props();
  let container: HTMLDivElement;
  let pane: HTMLElement;
  let searchInput = $state<HTMLInputElement>();
  let terminal: XtermTerminal | undefined;
  let fit: FitAddon;
  let searchAddon: SearchAddon;
  let searchOpen = $state(false);
  let query = $state('');
  let searchFound = $state<boolean | undefined>();
  let socket: WebSocket | undefined;
  let status = $state<'connecting' | 'connected' | 'disconnected'>(
    'disconnected',
  );
  let reloadPending = false;
  let ended = false;
  const palette = () =>
    dark()
      ? {
          background: '#171918',
          foreground: '#d9dfdc',
          cursor: '#65c9a5',
          selectionBackground: '#315748',
          black: '#262b28',
          green: '#78c6a3',
        }
      : {
          background: '#ffffff',
          foreground: '#28332e',
          cursor: '#148562',
          selectionBackground: '#cfe9dd',
          black: '#28332e',
          green: '#16845e',
        };
  function send(value: object) {
    if (socket?.readyState === WebSocket.OPEN)
      socket.send(JSON.stringify(value));
  }
  function resize() {
    if (active && container?.clientWidth && container?.clientHeight) fit?.fit();
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
    ended = false;
    const connection = new WebSocket(
      `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/nodes/${nodeID}/terminal`,
    );
    socket = connection;
    connection.onopen = () => {
      status = 'connected';
      if (reloadPending) {
        send({ type: 'close' });
        return;
      }
      resize();
      send({ type: 'resize', cols: terminal!.cols, rows: terminal!.rows });
      if (active) terminal?.focus();
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
        title: 'reload',
        description: 'confirmReloadTerminal',
        confirmLabel: 'reload',
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
      title: 'stopTerminal',
      description: 'confirmStopTerminal',
      confirmLabel: 'stopTerminal',
      detail: name,
      action: () => {
        reloadPending = false;
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
    if (active) {
      void tick().then(() => {
        resize();
        terminal?.focus();
      });
    } else searchOpen = false;
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
    const size = terminal.onResize(({ cols, rows }) =>
      send({ type: 'resize', cols, rows }),
    );
    const observer = new ResizeObserver(resize);
    observer.observe(container);
    if (online) connect();
    else terminal.writeln(`[${t('nodeOffline')}]`);
    return () => {
      reloadPending = false;
      terminalPane.removeEventListener('keydown', keydown, true);
      observer.disconnect();
      data.dispose();
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

<Popover.Root bind:open={searchOpen}>
  <section
    class="terminal-pane"
    bind:this={pane}
    aria-label={`${name} ${t('terminalHint')}`}
  >
    <div class="terminal-toolbar">
      <span class="connection-state" aria-label={t(status)} title={t(status)}>
        <span class:online={status === 'connected'} class="status-dot"></span>
        <span class="connection-label">{t(status)}</span>
      </span>
      <div class="toolbar-actions">
        <Popover.Trigger
          class="icon-button"
          aria-label={t('terminalSearch')}
          title={t('terminalSearch')}
        >
          <Search size={15} />
        </Popover.Trigger><button
          class="icon-button"
          aria-label={t('stopTerminal')}
          title={t('stopTerminal')}
          disabled={status !== 'connected'}
          onclick={stop}
        >
          <Square size={14} />
        </button><button
          class="icon-button"
          aria-label={t('reload')}
          title={t('reload')}
          onclick={reload}
        >
          <RotateCw size={15} />
        </button>
      </div>
    </div>
    <div class="terminal-body">
      <div class="terminal-container" bind:this={container}></div>
    </div>
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
