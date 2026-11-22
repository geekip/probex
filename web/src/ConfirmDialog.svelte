<script module lang="ts">
  import type { Message } from './settings.svelte';

  export type Confirmation = {
    title: Message;
    description: Message;
    confirmLabel: Message;
    detail?: string;
    danger?: boolean;
    action: () => void | Promise<void>;
  };
</script>

<script lang="ts">
  import { AlertDialog } from 'bits-ui';
  import { t } from './settings.svelte';

  let { request, onclose }: { request: Confirmation; onclose: () => void } =
    $props();
  let open = $state(true);
  let busy = $state(false);
  let error = $state('');

  async function runAction() {
    busy = true;
    error = '';
    try {
      await request.action();
      onclose();
    } catch (failure) {
      error = (failure as Error).message;
    } finally {
      busy = false;
    }
  }
</script>

<AlertDialog.Root
  bind:open
  onOpenChange={(value) => {
    if (!value) onclose();
  }}
>
  <AlertDialog.Portal>
    <AlertDialog.Overlay class="dialog-overlay confirmation-overlay" />
    <AlertDialog.Content
      class="confirmation-dialog"
      aria-busy={busy}
      onEscapeKeydown={(event) => {
        if (busy) event.preventDefault();
      }}
    >
      <AlertDialog.Title class="dialog-title">
        {t(request.title)}
      </AlertDialog.Title>
      <AlertDialog.Description class="dialog-description">
        {t(request.description)}
      </AlertDialog.Description>
      {#if request.detail}<p class="confirmation-detail">
          {request.detail}
        </p>{/if}
      {#if error}<p class="inline-error" role="alert">{error}</p>{/if}
      <div class="dialog-actions">
        <AlertDialog.Cancel class="quiet-button" disabled={busy}>
          {t('cancel')}
        </AlertDialog.Cancel>
        <button
          class={`primary-button ${request.danger ? 'danger-button' : ''}`}
          disabled={busy}
          onclick={runAction}
        >
          {t(busy ? 'processing' : request.confirmLabel)}
        </button>
      </div>
    </AlertDialog.Content>
  </AlertDialog.Portal>
</AlertDialog.Root>
