<script lang="ts">
  import type { Snippet } from 'svelte';
  import { ContextMenu, DropdownMenu } from 'bits-ui';
  import { Settings, Pencil, KeyRound, Trash2, Check, X } from '@lucide/svelte';
  import type { Node } from './api';
  import { t } from './settings.svelte';

  let {
    node,
    context = false,
    selected = false,
    children,
    onrename,
    onenrollment,
    onremove,
    onreview,
  }: {
    node: Node;
    context?: boolean;
    selected?: boolean;
    children?: Snippet;
    onrename: (node: Node) => void;
    onenrollment: (node: Node) => void;
    onremove: (node: Node) => void;
    onreview: (node: Node, action: 'approve' | 'reject') => void;
  } = $props();
</script>

{#snippet items(Item: typeof DropdownMenu.Item | typeof ContextMenu.Item)}
  {#if node.admission === 'review'}
    <Item class="menu-item" onSelect={() => onreview(node, 'approve')}>
      <Check size={14} />{t('approve')}
    </Item>
    <Item class="menu-item danger" onSelect={() => onreview(node, 'reject')}>
      <X size={14} />{t('reject')}
    </Item>
  {:else if node.admission !== 'rejected'}
    <Item class="menu-item" onSelect={() => onrename(node)}>
      <Pencil size={14} />{t('rename')}
    </Item>
  {/if}
  {#if node.join_mode === 'key' && (node.admission === 'invited' || node.admission === 'expired')}
    <Item class="menu-item" onSelect={() => onenrollment(node)}>
      <KeyRound size={14} />{t('regenerate')}
    </Item>
  {/if}
  <Item class="menu-item danger" onSelect={() => onremove(node)}>
    <Trash2 size={14} />{t('delete')}
  </Item>
{/snippet}

{#if context}
  <ContextMenu.Root>
    <ContextMenu.Trigger
      class={`sidebar-node ${selected ? 'selected' : ''}`}
      data-node-id={node.id}
    >
      {@render children?.()}
    </ContextMenu.Trigger>
    <ContextMenu.Portal>
      <ContextMenu.Content class="menu">
        {@render items(ContextMenu.Item)}
      </ContextMenu.Content>
    </ContextMenu.Portal>
  </ContextMenu.Root>
{:else}
  <DropdownMenu.Root>
    <DropdownMenu.Trigger
      class="icon-button node-menu"
      aria-label={`${node.name} ${t('settings')}`}
      title={t('settings')}
    >
      <Settings size={15} />
    </DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <DropdownMenu.Content class="menu" align="end" sideOffset={6}>
        {@render items(DropdownMenu.Item)}
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
{/if}
