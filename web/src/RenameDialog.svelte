<script lang="ts">
  import { untrack } from 'svelte';
  import { Dialog } from 'bits-ui';
  import { api, type Node } from './api';
  import { t } from './settings.svelte';

  let {
    node,
    onclose,
    onrenamed,
  }: {
    node: Node;
    onclose: () => void;
    onrenamed: (node: Node) => void;
  } = $props();
  let open = $state(true);
  let name = $state(untrack(() => node.name));
  let busy = $state(false);
  let error = $state('');

  async function rename(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    error = '';
    try {
      const renamed = await api<Node>(`nodes/${node.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ name }),
      });
      onrenamed(renamed);
      onclose();
    } catch (failure) {
      error = (failure as Error).message;
    } finally {
      busy = false;
    }
  }
</script>

<Dialog.Root
  bind:open
  onOpenChange={(value) => {
    if (!value) onclose();
  }}
>
  <Dialog.Portal>
    <Dialog.Overlay class="dialog-overlay" />
    <Dialog.Content
      class="join-dialog rename-dialog"
      aria-busy={busy}
      onEscapeKeydown={(event) => {
        if (busy) event.preventDefault();
      }}
      onInteractOutside={(event) => {
        if (busy) event.preventDefault();
      }}
    >
      <div class="dialog-heading">
        <div>
          <Dialog.Title class="dialog-title">{t('rename')}</Dialog.Title>
          <Dialog.Description class="dialog-description">
            {t('renameHint')}
          </Dialog.Description>
        </div>
      </div>
      <form class="join-content" onsubmit={rename}>
        <label for="rename-node">{t('name')}</label>
        <input
          id="rename-node"
          bind:value={name}
          required
          maxlength="64"
          autocomplete="off"
          disabled={busy}
        />
        {#if error}<p class="inline-error" role="alert">{error}</p>{/if}
        <div class="dialog-actions">
          <Dialog.Close class="quiet-button" disabled={busy}>
            {t('cancel')}
          </Dialog.Close>
          <button class="primary-button" disabled={busy}>
            {t(busy ? 'processing' : 'save')}
          </button>
        </div>
      </form>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>
