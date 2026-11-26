<script lang="ts">
  import { tick } from 'svelte';
  import { RadioGroup } from 'bits-ui';
  import { FolderUp, Server } from '@lucide/svelte';
  import type { Transfers } from '@/features/transfers/store.svelte';
  import TransferRows, { transferStatusLabels } from './TransferRows.svelte';
  import PendingLabel from '@/shared/components/PendingLabel.svelte';
  import WindowHeading from '@/shared/components/WindowHeading.svelte';
  import { dragWindow } from '@/shared/window-drag';
  import { t } from '@/shared/settings.svelte';
  import type { Confirmation } from '@/shared/components/ConfirmDialog.svelte';
  let {
    manager,
    onrequestconfirm,
  }: {
    manager: Transfers;
    onrequestconfirm: (request: Confirmation) => void;
  } = $props();
  let pinned = $state(false);
  let maximized = $state(false);
  let offset = $state({ x: 0, y: 0 });
  let content: HTMLDivElement;
  let removing = $state<'completed' | 'all' | null>(null);
  const filterLabels = {
    all: 'allTransfers',
    ...transferStatusLabels,
  } as const;
  type Filter = keyof typeof filterLabels;
  const filters = Object.keys(filterLabels) as Filter[];
  let filter = $state<Filter>('all');
  const counts = $derived.by(() => {
    const counts: Partial<Record<Filter, number>> = {
      all: manager.tasks.length,
    };
    for (const task of manager.tasks)
      counts[task.state] = (counts[task.state] ?? 0) + 1;
    return counts;
  });
  const filteredTasks = $derived(
    filter === 'all'
      ? manager.tasks
      : manager.tasks.filter((task) => task.state === filter),
  );
  function filterText(value: Filter) {
    return `${t(filterLabels[value])}(${counts[value] ?? 0})`;
  }
  function move(dx: number, dy: number) {
    offset.x += dx;
    offset.y += dy;
  }
  function minimize() {
    manager.visible = false;
    document
      .getElementById('transfer-window-trigger')
      ?.focus({ preventScroll: true });
  }
  function close() {
    pinned = false;
    maximized = false;
    offset = { x: 0, y: 0 };
    filter = 'all';
    minimize();
  }
  function outside(event: PointerEvent) {
    const target = event.target as Element;
    if (
      manager.visible &&
      !pinned &&
      !content.contains(target) &&
      !target.closest(
        '#transfer-window-trigger,[role="alertdialog"],.confirmation-overlay',
      )
    )
      manager.visible = false;
  }
  function handleKeydown(event: KeyboardEvent) {
    if (!event.defaultPrevented && event.key === 'Escape') {
      event.preventDefault();
      minimize();
    }
  }
  $effect(() => {
    if (manager.visible)
      void tick().then(() => content.focus({ preventScroll: true }));
  });
  async function remove(completedOnly: boolean) {
    if (removing) return;
    removing = completedOnly ? 'completed' : 'all';
    try {
      if (completedOnly) await manager.removeCompleted();
      else await manager.removeAll();
    } finally {
      removing = null;
    }
  }
  function removeAll() {
    if (
      manager.tasks.some(
        (task) => task.state === 'running' || task.state === 'queued',
      )
    ) {
      onrequestconfirm({
        title: 'removeAllTransfers',
        description: 'removeAllTransfersWarning',
        confirmLabel: 'removeAllTransfers',
        danger: true,
        action: () => remove(false),
      });
    } else void remove(false);
  }
</script>

<svelte:window onpointerdown={outside} />
<div
  bind:this={content}
  id="transfer-window"
  class="transfer-window"
  class:maximized
  role="dialog"
  aria-modal="false"
  aria-labelledby="transfer-window-title"
  tabindex="-1"
  hidden={!manager.visible}
  onkeydown={handleKeydown}
  style:--transfer-x={offset.x + 'px'}
  style:--transfer-y={offset.y + 'px'}
  use:dragWindow={{ enabled: manager.visible && !maximized, move }}
>
  <WindowHeading
    titleId="transfer-window-title"
    title={t('fileTransfers')}
    icon={FolderUp}
    bind:pinned
    bind:maximized
    onminimize={minimize}
    onclose={close}
  />
  <div class="transfer-body">
    <div class="transfer-toolbar">
      <RadioGroup.Root
        class="transfer-filter"
        aria-label={t('filterTransferStatus')}
        orientation="horizontal"
        value={filter}
        onValueChange={(value) => (filter = value as Filter)}
      >
        {#each filters as value}
          <RadioGroup.Item class="transfer-filter-item" {value}>
            {filterText(value)}
          </RadioGroup.Item>
        {/each}
      </RadioGroup.Root>
      <div class="transfer-bulk-actions">
        <button
          class="quiet-button"
          disabled={!!removing ||
            !manager.tasks.some((task) => task.state === 'completed')}
          onclick={() => remove(true)}
        >
          {#if removing === 'completed'}<PendingLabel />{:else}{t(
              'removeCompletedTransfers',
            )}{/if}
        </button>
        <button
          class="quiet-button"
          disabled={!!removing || !manager.tasks.length}
          onclick={removeAll}
        >
          {#if removing === 'all'}<PendingLabel />{:else}{t(
              'removeAllTransfers',
            )}{/if}
        </button>
      </div>
    </div>
    <TransferRows items={filteredTasks} {manager} disabled={!!removing} />
    {#if !filteredTasks.length}
      <div class="transfer-empty">
        <Server size={32} aria-hidden="true" />
        <span>
          {t(manager.tasks.length ? 'noTransfersInStatus' : 'noTransfers')}
        </span>
      </div>
    {/if}
  </div>
</div>
