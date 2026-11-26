<script lang="ts">
  import type { Snippet } from 'svelte';
  import { CircleAlert, TriangleAlert, Info, X } from '@lucide/svelte';
  import { t } from '@/shared/settings.svelte';
  let {
    message,
    tone = 'info',
    children,
  }: {
    message: string;
    tone?: 'info' | 'warning' | 'error';
    children?: Snippet;
  } = $props();
  let dismissed = $state(false);
  $effect(() => {
    message;
    dismissed = false;
  });
</script>

{#if !dismissed}
  <div
    class="editor-notice"
    data-tone={tone}
    role={tone === 'error' ? 'alert' : 'status'}
  >
    {#if tone === 'error'}<CircleAlert
        size={14}
      />{:else if tone === 'warning'}<TriangleAlert size={14} />{:else}<Info
        size={14}
      />{/if}
    <span class="editor-notice-message">{message}</span>
    {#if children}<div class="editor-notice-actions">
        {@render children()}
      </div>{/if}
    <button
      class="icon-button editor-notice-close"
      aria-label={t('closeNotice')}
      title={t('closeNotice')}
      onclick={() => (dismissed = true)}
    >
      <X size={13} />
    </button>
  </div>
{/if}
