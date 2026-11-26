<script lang="ts">
  import { untrack } from 'svelte';
  import { Dialog } from 'bits-ui';
  import { t } from '@/shared/settings.svelte';
  let {
    title,
    initial,
    target,
    onsave,
    onclose,
  }: {
    title: 'rename' | 'newFile' | 'newDirectory';
    initial: string;
    target: string;
    onsave: (name: string) => Promise<void>;
    onclose: () => void;
  } = $props();
  let open = $state(true),
    name = $state(untrack(() => initial)),
    busy = $state(false),
    error = $state('');
  async function save(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    error = '';
    try {
      await onsave(name);
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
    <Dialog.Overlay class="dialog-overlay" /><Dialog.Content
      class="join-dialog rename-dialog"
      onInteractOutside={(e) => {
        if (busy) e.preventDefault();
      }}
      onEscapeKeydown={(e) => {
        if (busy) e.preventDefault();
      }}
    >
      <div class="dialog-heading">
        <div>
          <Dialog.Title class="dialog-title">
            {t(title)}
          </Dialog.Title><Dialog.Description class="dialog-description">
            {target}
          </Dialog.Description>
        </div>
      </div>
      <form class="join-content" onsubmit={save}>
        <label for="file-name">{t('fileName')}</label><input
          id="file-name"
          bind:value={name}
          required
          disabled={busy}
          autocomplete="off"
        />{#if error}<p class="form-error" role="alert">{error}</p>{/if}
        <div class="dialog-actions">
          <button
            type="button"
            class="quiet-button"
            disabled={busy}
            onclick={onclose}
          >
            {t('cancel')}
          </button><button class="primary-button" disabled={busy}>
            {t(busy ? 'processing' : 'save')}
          </button>
        </div>
      </form>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>
