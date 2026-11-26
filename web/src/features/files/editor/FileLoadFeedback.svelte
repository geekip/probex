<script lang="ts">
  import { CircleOff, LoaderCircle, RotateCw } from '@lucide/svelte';
  import { t } from '@/shared/settings.svelte';

  let {
    error,
    onretry,
    disabled = false,
  }: {
    error?: string;
    onretry?: () => void;
    disabled?: boolean;
  } = $props();
</script>

{#if error}
  <div class="editor-empty-state file-load-feedback" role="alert">
    <CircleOff size={32} class="file-load-error-icon" aria-hidden="true" />
    <p>{t('fileLoadFailed')}</p>
    <small>{error}</small>
    {#if onretry}<button class="quiet-button" {disabled} onclick={onretry}>
        <RotateCw size={14} aria-hidden="true" />{t('reload')}
      </button>{/if}
  </div>
{:else}
  <div class="editor-empty-state file-load-feedback" role="status">
    <LoaderCircle size={32} class="spin" aria-hidden="true" />
    <p>{t('loadingFile')}</p>
  </div>
{/if}
