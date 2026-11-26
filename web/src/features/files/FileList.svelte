<script lang="ts">
  import {
    File,
    FileSymlink,
    ArrowUpDown,
    ChevronUp,
    ChevronDown,
  } from '@lucide/svelte';
  import folderIcon from '../../../assets/folder.svg?raw';
  import folderSymlinkIcon from '../../../assets/folder-symlink.svg?raw';
  import {
    draggable,
    dropTargetForElements,
  } from '@atlaskit/pragmatic-drag-and-drop/element/adapter';
  import { combine } from '@atlaskit/pragmatic-drag-and-drop/combine';
  import { setCustomNativeDragPreview } from '@atlaskit/pragmatic-drag-and-drop/utils/set-custom-native-drag-preview';
  import { directoryTarget, parentPath, type FileEntry } from './api';
  import { clipboard } from './clipboard.svelte';
  import { t, bytes, date } from '@/shared/settings.svelte';
  let {
    entries,
    path,
    windows,
    nodeID,
    columns,
    selection,
    disabled,
    sort,
    order,
    onselect,
    onopen,
    onsort,
    onmove,
    onupload,
  }: {
    entries: FileEntry[];
    path: string;
    windows: boolean;
    nodeID: string;
    columns: string[];
    selection: string[];
    disabled: boolean;
    sort: string;
    order: 'asc' | 'desc';
    onselect: (entry: FileEntry, event: MouseEvent) => void;
    onopen: (entry: FileEntry) => void;
    onsort: (field: string) => void;
    onmove: (entries: FileEntry[], target: FileEntry) => void;
    onupload: (items: DataTransferItem[], target: FileEntry) => void;
  } = $props();
  const parent = $derived<FileEntry>({
    name: '..',
    path: parentPath(path, windows),
    type: 'directory',
  });
  const rows = $derived([parent, ...entries]);
  const headers = {
    name: { label: 'fileName', min: 180, weight: 4 },
    mtime: { label: 'modifiedTime', min: 100, max: 160, weight: 2 },
    type: { label: 'fileType', min: 70, max: 120, weight: 1 },
    size: { label: 'fileSize', min: 70, max: 120, weight: 1 },
    mode: { label: 'fileMode', min: 80, max: 120, weight: 1 },
    owner: { label: 'fileOwner', min: 80, max: 180, weight: 1.5 },
  } as const;
  const shown = $derived([
    'name',
    ...Object.keys(headers).filter(
      (key) => key !== 'name' && columns.includes(key),
    ),
  ] as (keyof typeof headers)[]);
  const minWidth = $derived(
    shown.reduce((width, field) => width + headers[field].min, 0),
  );
  const totalWeight = $derived(
    shown.reduce((weight, field) => weight + headers[field].weight, 0),
  );
  function columnWidth(field: keyof typeof headers) {
    if (field === 'name') return;
    const { min, max, weight } = headers[field];
    // The name column receives its share plus any space left by capped columns.
    return `min(${max}px, calc(${min}px + max(0px, 100cqi - ${minWidth}px) * ${weight / totalWeight}))`;
  }
  function canMove(items: FileEntry[], target: string) {
    const normalize = (value: string) =>
      windows ? value.toLowerCase().replaceAll('\\', '/') : value;
    const destination = normalize(target).replace(/\/$/, '');
    return items.every((entry) => {
      const source = normalize(entry.path).replace(/\/$/, '');
      return (
        destination !== source &&
        (entry.type !== 'directory' || !destination.startsWith(source + '/'))
      );
    });
  }
  function rowDrag(element: HTMLElement, entry: FileEntry) {
    const cleanup = combine(
      draggable({
        element,
        dragHandle: element.querySelector('.file-entry')!,
        canDrag: () => !disabled && entry.name !== '..',
        getInitialData: () => ({
          nodeID,
          entries: selection.includes(entry.path)
            ? entries.filter((e) => selection.includes(e.path))
            : [entry],
        }),
        onGenerateDragPreview: ({ source, nativeSetDragImage }) => {
          const items = source.data.entries as FileEntry[];
          setCustomNativeDragPreview({
            nativeSetDragImage,
            render: ({ container }) => {
              const preview = document.createElement('div');
              preview.className = 'file-drag-preview';
              preview.textContent =
                items.length === 1
                  ? items[0].name
                  : t('dragFileCount').replace('{count}', String(items.length));
              container.append(preview);
            },
          });
        },
      }),
      dropTargetForElements({
        element,
        canDrop: ({ source }) =>
          !disabled &&
          directoryTarget(entry) &&
          source.data.nodeID === nodeID &&
          canMove(source.data.entries as FileEntry[], entry.path),
        getDropEffect: () => 'move',
        onDragEnter: () => element.classList.add('drop-target'),
        onDragLeave: () => element.classList.remove('drop-target'),
        onDrop: ({ source }) => {
          element.classList.remove('drop-target');
          onmove(source.data.entries as FileEntry[], entry);
        },
      }),
    );
    return {
      update(value: FileEntry) {
        entry = value;
      },
      destroy: cleanup,
    };
  }
  function externalDrop(element: HTMLElement) {
    const over = (event: DragEvent) => {
      if (!disabled && event.dataTransfer?.types.includes('Files')) {
        event.preventDefault();
        event.dataTransfer.dropEffect = 'copy';
      }
    };
    const drop = (event: DragEvent) => {
      if (disabled || !event.dataTransfer?.types.includes('Files')) return;
      event.preventDefault();
      const row = (event.target as Element).closest<HTMLElement>(
        '[data-file-path]',
      );
      const entry = row?.hasAttribute('data-parent')
        ? parent
        : entries.find((entry) => entry.path === row?.dataset.filePath);
      onupload(
        [...event.dataTransfer.items],
        directoryTarget(entry) ? entry! : { name: '', path, type: 'directory' },
      );
    };
    element.addEventListener('dragover', over);
    element.addEventListener('drop', drop);
    return {
      destroy() {
        element.removeEventListener('dragover', over);
        element.removeEventListener('drop', drop);
      },
    };
  }
  function fileType(entry: FileEntry) {
    if (entry.type === 'directory') return t('folderType');
    if (entry.type === 'symlink') return t('symlinkType');
    if (entry.link_kind === 'shortcut') return t('shortcutType');
    const dot = entry.name.lastIndexOf('.');
    return dot > 0 ? entry.name.slice(dot + 1) : t('regularFileType');
  }
</script>

<table
  class="file-table"
  style:min-width={`${minWidth}px`}
  aria-label={t('fileManager')}
  use:externalDrop
>
  <colgroup>
    {#each shown as field (field)}<col
        style:width={columnWidth(field)}
      />{/each}
  </colgroup>
  <thead>
    <tr>
      {#each shown as field}<th
          aria-sort={sort === field
            ? order === 'asc'
              ? 'ascending'
              : 'descending'
            : 'none'}
        >
          <button
            class="file-sort"
            title={t(headers[field].label)}
            onclick={() => onsort(field)}
          >
            <span>{t(headers[field].label)}</span>
            {#if sort === field}{#if order === 'asc'}<ChevronUp
                  size={12}
                />{:else}<ChevronDown size={12} />{/if}{:else}<ArrowUpDown
                size={12}
              />{/if}
          </button>
        </th>{/each}
    </tr>
  </thead>
  <tbody>
    {#each rows as entry, index (index === 0 ? 'parent' : entry.path)}
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions a11y_click_events_have_key_events (The file button provides keyboard activation for the row.) -->
      <tr
        class:selected={index !== 0 && selection.includes(entry.path)}
        class:cut={index !== 0 &&
          clipboard.nodeID === nodeID &&
          clipboard.cut &&
          clipboard.entries.some((e) => e.path === entry.path)}
        data-file-path={entry.path}
        data-file-name={entry.name}
        data-file-type={entry.type}
        data-parent={index === 0 ? true : undefined}
        use:rowDrag={entry}
        onclick={(event) => {
          if (!disabled && index !== 0) onselect(entry, event);
        }}
        ondblclick={() => {
          if (!disabled) onopen(entry);
        }}
      >
        {#each shown as field}<td>
            {#if field === 'name'}<button
                class="file-entry"
                title={entry.link_error
                  ? `${entry.path}\n${entry.link_error}`
                  : entry.path}
                aria-pressed={index !== 0 && selection.includes(entry.path)}
                {disabled}
                onkeydown={(event) => {
                  if (event.key === 'Enter') {
                    event.preventDefault();
                    onopen(entry);
                  }
                }}
              >
                {#if entry.type === 'symlink' || entry.link_kind}
                  {#if entry.link_target_type === 'directory'}{@html folderSymlinkIcon}{:else}<FileSymlink
                      size={15}
                    />{/if}
                {:else if entry.type === 'directory'}{@html folderIcon}{:else}<File
                    size={15}
                  />{/if}<span>{entry.name}</span>
              </button>
            {:else if index === 0}—
            {:else if field === 'size'}{entry.size === undefined
                ? '—'
                : bytes(entry.size)}
            {:else if field === 'mtime'}{entry.mtime ? date(entry.mtime) : '—'}
            {:else if field === 'type'}{fileType(entry)}
            {:else if field === 'mode'}{entry.mode ?? '—'}
            {:else if field === 'owner'}{entry.owner ?? entry.uid ?? '—'} / {entry.group ??
                entry.gid ??
                '—'}{/if}
          </td>{/each}
      </tr>{/each}
  </tbody>
</table>
