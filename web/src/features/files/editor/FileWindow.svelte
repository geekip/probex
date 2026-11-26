<script module lang="ts">
  import type { Node } from '@/features/nodes/types';
  import type { FileEntry } from '@/features/files/api';
  import type { FileKind } from '@/features/files/editor/file-kind';
  export type OpenedFile = {
    id: number;
    node: Node;
    entry: FileEntry;
    kind: FileKind;
    ready: boolean;
    dirty: boolean;
    saving: boolean;
    missing?: boolean;
    nodeMissing?: boolean;
  };
</script>

<script lang="ts">
  import { tick } from 'svelte';
  import { SvelteMap } from 'svelte/reactivity';
  import { sortableTabs, reorderTabs } from '@/shared/tab-sort';
  import { scrollTabs } from '@/shared/tab-scroll';
  import '@interactjs/auto-start';
  import '@interactjs/actions/drag';
  import interact from '@interactjs/interact';
  import {
    Minus,
    X,
    Maximize2,
    Minimize2,
    FileJson,
    FileText,
    EyeOff,
    Download,
    Pin,
    PinOff,
  } from '@lucide/svelte';
  import type { Transfers } from '@/features/transfers/store.svelte';
  import type { Confirmation } from '@/shared/components/ConfirmDialog.svelte';
  import MediaPreview from '@/features/files/editor/MediaPreview.svelte';
  import type FileEditor from '@/features/files/editor/FileEditor.svelte';
  const editors = new SvelteMap<number, FileEditor>();
  const previews = new SvelteMap<number, MediaPreview>();
  import EditorMenu from './EditorMenu.svelte';
  import EditorNotice from './EditorNotice.svelte';
  import TextFormatMenu from './TextFormatMenu.svelte';
  import { t, bytes } from '@/shared/settings.svelte';
  let {
    files = $bindable(),
    visible,
    selected,
    manager,
    onselect,
    onready,
    onminimize,
    onremove,
    onclose,
    onrequestconfirm,
    onerror,
  }: {
    files: OpenedFile[];
    visible: boolean;
    selected: number;
    manager: Transfers;
    onselect: (id: number) => void;
    onready: (file: OpenedFile) => void;
    onminimize: () => void;
    onremove: (id: number) => void;
    onclose: () => void;
    onrequestconfirm: (request: Confirmation) => void;
    onerror: (message: string) => void;
  } = $props();
  let pinned = $state(false),
    maximized = $state(false),
    showLineNumbers = $state(true),
    wrapLines = $state(true),
    fontSize = $state(12),
    content = $state<HTMLDivElement | null>(null);
  let rectangle = $state({
    x: Math.max(8, (innerWidth - 920) / 2),
    y: Math.max(8, (innerHeight - 640) / 2),
    width: Math.min(920, innerWidth - 16),
    height: Math.min(640, innerHeight - 16),
  });
  const current = $derived(
    files.find((f) => f.id === selected) || files.at(-1),
  );
  const editor = $derived(current ? editors.get(current.id) : undefined);
  const status = $derived(editor?.status());
  function reload() {
    if (editor) editor.reload();
    else if (current) previews.get(current.id)?.reload();
  }
  function download() {
    if (current)
      void manager
        .download(current.node, [current.entry])
        .catch((failure) => onerror(failure.message));
  }
  const shown = $derived(
    visible && (current?.kind !== 'text' || current.ready),
  );
  function movable(element: HTMLElement, enabled: boolean) {
    const drag = interact(element)
      .styleCursor(false)
      .draggable({
        enabled,
        allowFrom: '.window-drag-area',
        ignoreFrom: '.toolbar-actions,button',
        listeners: {
          move(event) {
            rectangle.x += event.dx;
            rectangle.y += event.dy;
          },
        },
      });
    return {
      update(enabled: boolean) {
        drag.draggable({ enabled });
      },
      destroy() {
        drag.unset();
      },
    };
  }
  function outside(event: PointerEvent) {
    const target = event.target as Element;
    if (
      visible &&
      !pinned &&
      !content?.contains(target) &&
      !target.closest('[data-file-window-menu],[role="alertdialog"]')
    )
      onminimize();
  }
  function handleKeydown(event: KeyboardEvent) {
    if (event.defaultPrevented) return;
    if ((event.metaKey || event.ctrlKey) && !event.altKey) {
      if (event.key.toLowerCase() === 's' && editor) {
        event.preventDefault();
        if (status?.canSave) void editor.save();
        return;
      }
      if (event.key.toLowerCase() === 'f' && editor) {
        event.preventDefault();
        editor.search();
        return;
      }
    }
    if (event.key !== 'Escape') return;
    event.preventDefault();
    if (current && editors.get(current.id)?.closeSearch()) return;
    onminimize();
  }
  $effect(() => {
    if (shown) void tick().then(() => content?.focus({ preventScroll: true }));
  });
  $effect(() => {
    if (shown && current) {
      const id = current.id;
      void tick().then(() =>
        document
          .getElementById(`file-tab-${id}`)
          ?.scrollIntoView({ block: 'nearest', inline: 'nearest' }),
      );
    }
  });
  $effect(() => {
    if (!content) return;
    const observer = new ResizeObserver(() => {
      if (!maximized && shown) {
        rectangle.width = content!.offsetWidth;
        rectangle.height = content!.offsetHeight;
      }
    });
    observer.observe(content);
    return () => observer.disconnect();
  });
</script>

<svelte:window onpointerdown={outside} />
<div
  bind:this={content}
  role="dialog"
  aria-modal="false"
  aria-labelledby="file-window-title"
  aria-describedby="file-window-description"
  tabindex="-1"
  hidden={!shown}
  class="file-window"
  class:maximized
  onkeydown={handleKeydown}
  style:left={maximized ? '8px' : rectangle.x + 'px'}
  style:top={maximized ? '8px' : rectangle.y + 'px'}
  style:width={maximized ? 'calc(100vw - 16px)' : rectangle.width + 'px'}
  style:height={maximized ? 'calc(100dvh - 16px)' : rectangle.height + 'px'}
  use:movable={!maximized && shown}
>
  <div class="file-window-heading window-drag-area">
    <FileJson size={17} aria-hidden="true" />
    <h2 id="file-window-title" class="dialog-title">{t('editor')}</h2>
    <EditorMenu
      {editor}
      online={!!current?.node.online}
      canReload={editor
        ? !!status?.canReload
        : !!current?.node.online && current.kind !== 'download'}
      bind:pinned
      bind:maximized
      bind:wrapLines
      bind:showLineNumbers
      bind:fontSize
      onreload={reload}
      ondownload={download}
      {onminimize}
      {onclose}
    />
    <p id="file-window-description" class="sr-only">
      {t('fileWindowDescription')}
    </p>
    <div class="toolbar-actions">
      <button
        class="icon-button"
        class:active={pinned}
        aria-label={t(pinned ? 'unpinWindow' : 'pinWindow')}
        title={t(pinned ? 'unpinWindow' : 'pinWindow')}
        aria-pressed={pinned}
        onclick={() => (pinned = !pinned)}
      >
        {#if pinned}<PinOff size={15} />{:else}<Pin size={15} />{/if}
      </button>
      <button
        class="icon-button"
        aria-label={t('minimizeWindow')}
        title={t('minimizeWindow')}
        onclick={onminimize}
      >
        <Minus size={15} />
      </button>
      <button
        class="icon-button"
        aria-label={t(maximized ? 'restoreWindow' : 'maximizeWindow')}
        title={t(maximized ? 'restoreWindow' : 'maximizeWindow')}
        onclick={() => (maximized = !maximized)}
      >
        {#if maximized}<Minimize2 size={15} />{:else}<Maximize2
            size={15}
          />{/if}
      </button>
      <button
        class="icon-button"
        aria-label={t('closeFiles')}
        title={t('closeFiles')}
        onclick={onclose}
      >
        <X size={15} />
      </button>
    </div>
  </div>
  <div
    class="tabs-scroll file-window-tabs"
    onwheel={scrollTabs}
    use:sortableTabs={(from, to) => (files = reorderTabs(files, from, to))}
    role="tablist"
    aria-label={t('openFiles')}
  >
    {#if !files.length}<div class="workspace-tab tab-wrap active">
        <button
          class="file-tab-title tab-title"
          role="tab"
          id="file-tab-welcome"
          aria-controls="file-content-welcome"
          aria-selected="true"
        >
          <span>{t('welcome')}</span>
        </button>
      </div>{/if}
    {#each files as file, index (file.id)}<div
        class="workspace-tab tab-wrap"
        data-tab-id={file.id}
        class:active={file.id === current?.id}
      >
        <button
          class="file-tab-title tab-title"
          class:dirty={file.dirty}
          aria-label={`${file.entry.name}(${file.node.name})${file.dirty ? ' · ' + t('unsavedChanges') : ''}`}
          role="tab"
          id={`file-tab-${file.id}`}
          aria-controls={`file-content-${file.id}`}
          aria-selected={file.id === current?.id}
          tabindex={file.id === current?.id ? 0 : -1}
          onclick={() => onselect(file.id)}
          title={`${file.node.name} · ${file.entry.path}`}
          onkeydown={(e) => {
            if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
              e.preventDefault();
              const next =
                files[
                  (index + (e.key === 'ArrowRight' ? 1 : files.length - 1)) %
                    files.length
                ];
              onselect(next.id);
              document.getElementById(`file-tab-${next.id}`)?.focus();
            }
          }}
        >
          <span>{file.entry.name}({file.node.name})</span>
        </button><button
          class="tab-close"
          aria-label={`${t('close')} ${file.entry.name}`}
          onclick={() => onremove(file.id)}
        >
          <X size={13} />
        </button>
      </div>{/each}
  </div>
  {#if !files.length}<div
      class="file-window-content"
      role="tabpanel"
      id="file-content-welcome"
      aria-labelledby="file-tab-welcome"
    >
      <div class="editor-empty-state">
        <FileText size={48} strokeWidth={1.25} />
        <p>{t('editorWelcome')}</p>
      </div>
    </div>{/if}
  {#each files as file (file.id)}<div
      class="file-window-content"
      role="tabpanel"
      id={`file-content-${file.id}`}
      aria-labelledby={`file-tab-${file.id}`}
      hidden={file.id !== current?.id}
    >
      {#if file.kind === 'text'}
        {#await import('@/features/files/editor/FileEditor.svelte')}<EditorNotice
            message={t('loading')}
          />{:then editor}<editor.default
            bind:this={
              () => editors.get(file.id),
              (value) => {
                if (value) editors.set(file.id, value);
                else editors.delete(file.id);
              }
            }
            {file}
            {wrapLines}
            {showLineNumbers}
            {fontSize}
            active={shown && file.id === current?.id}
            {manager}
            {onrequestconfirm}
            onready={() => onready(file)}
          />{:catch failure}<EditorNotice
            message={failure.message}
            tone="error"
          />{/await}
      {:else if file.kind === 'download'}
        <div class="editor-empty-state">
          <EyeOff size={48} strokeWidth={1.25} aria-hidden="true" />
          <p>{t('unsupportedFile')}</p>
          <button
            class="primary-button"
            disabled={!file.node.online}
            onclick={download}
          >
            <Download size={14} aria-hidden="true" />{t('downloadFile')}
          </button>
        </div>
      {:else}<MediaPreview
          bind:this={
            () => previews.get(file.id),
            (value) => {
              if (value) previews.set(file.id, value);
              else previews.delete(file.id);
            }
          }
          {file}
          active={shown && file.id === current?.id}
          {manager}
          {onerror}
        />{/if}
    </div>{/each}
  <footer class="file-window-footer window-drag-area">
    {#if current}<div class="editor-status-left">
        <span class="editor-save-state" class:dirty={current?.dirty}>
          {t(
            current?.kind !== 'text' || status?.readonly
              ? 'readOnly'
              : current?.saving
                ? 'savingFile'
                : current?.dirty
                  ? 'unsavedChanges'
                  : 'savedFile',
          )}
        </span>
        <span
          class="editor-status-path"
          title={`${current?.entry.path}(${current?.node.name})`}
        >
          {current?.entry.path}({current?.node.name})
        </span>
        <span class="editor-file-size">
          {current?.entry.size === undefined ? '—' : bytes(current.entry.size)}
        </span>
      </div>
      <div class="editor-status-right">
        {#if current?.kind === 'text'}
          <span>
            {t('editorLine').replace('{line}', String(status?.line || 1))}
          </span>
          <TextFormatMenu {editor} kind="encoding" />
          <TextFormatMenu {editor} kind="newline" />
        {/if}
        <button
          class="icon-button"
          aria-label={t('downloadFiles')}
          title={t('downloadFiles')}
          disabled={!current?.node.online}
          onclick={download}
        >
          <Download size={14} />
        </button>
      </div>{/if}
  </footer>
</div>
