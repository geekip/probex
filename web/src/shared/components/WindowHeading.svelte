<script lang="ts">
  import type { Component, Snippet } from 'svelte';
  import { t } from '@/shared/settings.svelte';
  import {
    Maximize2,
    Minimize2,
    Minus,
    Pin,
    PinOff,
    X,
    type LucideProps,
  } from '@lucide/svelte';

  let {
    titleId,
    title,
    icon: Icon,
    pinned = $bindable(),
    maximized = $bindable(),
    onminimize,
    onclose,
    children,
  }: {
    titleId: string;
    title: string;
    icon: Component<LucideProps>;
    pinned: boolean;
    maximized: boolean;
    onminimize: () => void;
    onclose: () => void;
    children?: Snippet;
  } = $props();
</script>

<div class="window-heading window-drag-area">
  <Icon size={17} aria-hidden="true" />
  <h2 id={titleId} class="dialog-title">{title}</h2>
  {@render children?.()}
  <div class="toolbar-actions">
    <button
      class="icon-button"
      class:active={pinned}
      aria-label={t(pinned ? 'unpin' : 'pin')}
      title={t(pinned ? 'unpin' : 'pin')}
      aria-pressed={pinned}
      onclick={() => (pinned = !pinned)}
    >
      {#if pinned}<PinOff size={15} />{:else}<Pin size={15} />{/if}
    </button>
    <button
      class="icon-button"
      aria-label={t('minimize')}
      title={t('minimize')}
      onclick={onminimize}
    >
      <Minus size={15} />
    </button>
    <button
      class="icon-button"
      aria-label={t(maximized ? 'restore' : 'maximize')}
      title={t(maximized ? 'restore' : 'maximize')}
      onclick={() => (maximized = !maximized)}
    >
      {#if maximized}<Minimize2 size={15} />{:else}<Maximize2 size={15} />{/if}
    </button>
    <button
      class="icon-button"
      aria-label={t('close')}
      title={t('close')}
      onclick={onclose}
    >
      <X size={15} />
    </button>
  </div>
</div>

<style>
  .window-heading {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 10px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }
  .dialog-title {
    font-size: 14px;
    white-space: nowrap;
  }
  @media (max-width: 600px) {
    .window-heading {
      gap: 5px;
      padding-inline: 8px;
    }
  }
</style>
