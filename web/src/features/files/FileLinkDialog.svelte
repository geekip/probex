<script lang="ts">
  import PendingLabel from '@/shared/components/PendingLabel.svelte';
  import { LoaderCircle } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { Dialog, Switch } from 'bits-ui';
  import type { FileLink } from './api';
  import { t } from '@/shared/settings.svelte';
  let {
    source,
    directory,
    windows,
    onload,
    onsave,
    onclose,
  }: {
    source?: string;
    directory: string;
    windows: boolean;
    onload: (source: string, signal: AbortSignal) => Promise<FileLink>;
    onsave: (
      name: string,
      target: string,
      hard: boolean,
      link: FileLink | null,
    ) => Promise<void>;
    onclose: () => void;
  } = $props();
  let open = $state(true),
    link = $state<FileLink | null>(null),
    name = $state(''),
    target = $state(''),
    hard = $state(false),
    busy = $state(false),
    error = $state('');
  const loading = $derived(!!source && !link && !error);
  const disabled = $derived(busy || (!!source && !link));

  async function load(path: string, signal: AbortSignal) {
    try {
      const result = await onload(path, signal);
      if (signal.aborted) return;
      if (!result.kind) {
        error = t('notFileLink');
        return;
      }
      name = result.name;
      target = result.target;
      hard = result.hard;
      link = result;
    } catch (failure) {
      if (!signal.aborted) error = (failure as Error).message;
    }
  }

  onMount(() => {
    if (!source) return;
    const request = new AbortController();
    void load(source, request.signal);
    return () => request.abort();
  });

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (disabled) return;
    busy = true;
    error = '';
    try {
      await onsave(name, target, hard, link);
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
            {t(source ? 'editLink' : 'createLink')}
          </Dialog.Title>
          <Dialog.Description class="dialog-description">
            {directory}
          </Dialog.Description>
        </div>
      </div>
      <form class="join-content" onsubmit={save}>
        <div class="link-dialog-fields">
          <label for="link-name">{t('linkName')}</label>
          <input
            id="link-name"
            bind:value={name}
            required
            {disabled}
            autocomplete="off"
          />
          <label for="link-target">{t('linkTarget')}</label>
          <input
            id="link-target"
            bind:value={target}
            required={!(link?.hard && hard)}
            {disabled}
            autocomplete="off"
          />
          <div class="link-switch-row">
            <label for="link-hard">{t('hardLink')}</label>
            <Switch.Root
              id="link-hard"
              class="link-switch"
              bind:checked={hard}
              {disabled}
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
          {#if loading}
            <div class="link-loading-layer" role="status">
              <LoaderCircle class="spin" size={18} aria-hidden="true" />
              {t('loading')}
            </div>
          {/if}
        </div>
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
          <button class="primary-button" {disabled}>
            {#if busy}<PendingLabel />{:else}{t('save')}{/if}
          </button>
        </div>
      </form>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<style>
  .link-dialog-fields {
    position: relative;
  }
  .link-loading-layer {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    background: var(--surface);
    color: var(--muted);
  }
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
