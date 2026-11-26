<script lang="ts">
  import { untrack } from 'svelte';
  import { Dialog, Switch } from 'bits-ui';
  import type { FileLink } from './api';
  import { t } from '@/shared/settings.svelte';
  let {
    link,
    directory,
    windows,
    onsave,
    onclose,
  }: {
    link: FileLink | null;
    directory: string;
    windows: boolean;
    onsave: (name: string, target: string, hard: boolean) => Promise<void>;
    onclose: () => void;
  } = $props();
  let open = $state(true),
    name = $state(untrack(() => link?.name || '')),
    target = $state(untrack(() => link?.target || '')),
    hard = $state(untrack(() => link?.hard || false)),
    busy = $state(false),
    error = $state('');
  async function save(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    error = '';
    try {
      await onsave(name, target, hard);
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
      onInteractOutside={(event) => {
        if (busy) event.preventDefault();
      }}
      onEscapeKeydown={(event) => {
        if (busy) event.preventDefault();
      }}
    >
      <div class="dialog-heading">
        <div>
          <Dialog.Title class="dialog-title">
            {t(link ? 'editLink' : 'createLink')}
          </Dialog.Title>
          <Dialog.Description class="dialog-description">
            {directory}
          </Dialog.Description>
        </div>
      </div>
      <form class="join-content" onsubmit={save}>
        <label for="link-name">{t('linkName')}</label>
        <input
          id="link-name"
          bind:value={name}
          required
          disabled={busy}
          autocomplete="off"
        />
        <label for="link-target">{t('linkTarget')}</label>
        <input
          id="link-target"
          bind:value={target}
          required={!(link?.hard && hard)}
          disabled={busy}
          autocomplete="off"
        />
        <div class="link-switch-row">
          <label for="link-hard">{t('hardLink')}</label>
          <Switch.Root
            id="link-hard"
            class="link-switch"
            bind:checked={hard}
            disabled={busy}
          >
            <Switch.Thumb class="link-switch-thumb" />
          </Switch.Root>
        </div>
        <p class="dialog-description">
          {t(
            hard
              ? link?.hard
                ? 'editHardLinkHint'
                : 'hardLinkHint'
              : windows
                ? 'windowsLinkHint'
                : 'symbolicLinkHint',
          )}
        </p>
        {#if error}<p class="form-error" role="alert">{error}</p>{/if}
        <div class="dialog-actions">
          <button
            type="button"
            class="quiet-button"
            disabled={busy}
            onclick={onclose}
          >
            {t('cancel')}
          </button>
          <button class="primary-button" disabled={busy}>
            {t(busy ? 'processing' : 'save')}
          </button>
        </div>
      </form>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<style>
  .link-switch-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  :global(.link-switch) {
    width: 30px;
    height: 18px;
    padding: 2px;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: var(--surface-hover);
  }
  :global(.link-switch[data-state='checked']) {
    background: var(--accent);
  }
  :global(.link-switch-thumb) {
    display: block;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: var(--muted);
    transition: transform 0.15s;
  }
  :global(.link-switch[data-state='checked'] .link-switch-thumb) {
    transform: translateX(12px);
    background: var(--on-accent);
  }
</style>
