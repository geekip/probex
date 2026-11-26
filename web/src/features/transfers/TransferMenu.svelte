<script lang="ts">
  import { Popover } from 'bits-ui';
  import { FolderUp } from '@lucide/svelte';
  import type { Transfers } from '@/features/transfers/store.svelte';
  import TransferRows from '@/features/transfers/TransferRows.svelte';
  import { t } from '@/shared/settings.svelte';
  import type { Confirmation } from '@/shared/components/ConfirmDialog.svelte';
  let {
    manager,
    labeled = false,
    onrequestconfirm,
  }: {
    manager: Transfers;
    labeled?: boolean;
    onrequestconfirm: (request: Confirmation) => void;
  } = $props();
  let removing = $state(false);
  async function remove(completedOnly: boolean) {
    removing = true;
    try {
      if (completedOnly) await manager.removeCompleted();
      else await manager.removeAll();
    } finally {
      removing = false;
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
  const count = $derived(
    manager.tasks.filter(
      (t) => !t.directory && ['running', 'queued'].includes(t.state),
    ).length,
  );
</script>

<Popover.Root
  bind:open={() => manager.visible, (value) => (manager.visible = value)}
>
  <Popover.Trigger
    class="icon-button workspace-tool transfer-trigger"
    aria-label={t('fileTransfers')}
    title={t('fileTransfers')}
  >
    <FolderUp size={16} />{#if labeled}<span>
        {t('fileTransfers')}
      </span>{/if}{#if count}<span class="workspace-count">
        {count}
      </span>{/if}
  </Popover.Trigger>
  <Popover.Portal>
    <Popover.Content
      class="transfer-popover"
      side="bottom"
      align="start"
      sideOffset={8}
      collisionPadding={8}
    >
      <div class="transfer-heading">
        <h2>{t('fileTransfers')}</h2>
        <div class="transfer-bulk-actions">
          <button
            class="quiet-button"
            disabled={removing ||
              !manager.tasks.some((task) => task.state === 'completed')}
            onclick={() => remove(true)}
          >
            {t('removeCompletedTransfers')}
          </button>
          <button
            class="quiet-button"
            disabled={removing || !manager.tasks.length}
            onclick={removeAll}
          >
            {t('removeAllTransfers')}
          </button>
        </div>
      </div>
      <TransferRows items={manager.tasks} {manager} />
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>
