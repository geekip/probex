<script lang="ts">
  import { AlertDialog } from 'bits-ui';
  import { t } from '@/shared/settings.svelte';
  import type { UploadPlan } from './upload-preflight';

  let {
    plan,
    onchoose,
    onclose,
  }: {
    plan: UploadPlan;
    onchoose: (replace: boolean) => void;
    onclose: () => void;
  } = $props();
  const blocked = $derived(
    plan.conflicts.some(({ kind }) => kind === 'blocked'),
  );
</script>

<AlertDialog.Root
  open
  onOpenChange={(open) => {
    if (!open) onclose();
  }}
>
  <AlertDialog.Portal>
    <AlertDialog.Overlay class="dialog-overlay confirmation-overlay" />
    <AlertDialog.Content class="confirmation-dialog">
      <AlertDialog.Title class="dialog-title">
        {t('uploadConflicts')}
      </AlertDialog.Title>
      <AlertDialog.Description class="dialog-description">
        {t('uploadConflictsDescription')}
      </AlertDialog.Description>
      <p class="upload-conflict-target">{plan.target}</p>
      <ul class="upload-conflict-list">
        {#each plan.conflicts as { relative, kind } (relative)}
          <li>
            {relative}
            {#if kind === 'directory'}<small>
                {t('uploadMergeDirectory')}
              </small>{/if}
            {#if kind === 'blocked'}<small>
                {t('uploadTypeConflict')}
              </small>{/if}
          </li>
        {/each}
      </ul>
      {#if blocked}
        <p class="dialog-description">{t('uploadTypeConflictDescription')}</p>
      {/if}
      <div class="dialog-actions">
        <AlertDialog.Cancel class="quiet-button">
          {t('cancel')}
        </AlertDialog.Cancel>
        <button class="quiet-button" onclick={() => onchoose(false)}>
          {t('skipUploadConflicts')}
        </button>
        <button
          class="primary-button"
          disabled={!plan.conflicts.some(({ kind }) => kind !== 'blocked')}
          onclick={() => onchoose(true)}
        >
          {t('replaceUploadConflicts')}
        </button>
      </div>
    </AlertDialog.Content>
  </AlertDialog.Portal>
</AlertDialog.Root>
