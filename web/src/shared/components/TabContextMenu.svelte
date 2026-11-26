<script lang="ts" generics="ID extends string | number">
  import type { Snippet } from 'svelte';
  import { ContextMenu } from 'bits-ui';
  import { RotateCw } from '@lucide/svelte';
  import { t, type Message } from '@/shared/settings.svelte';

  let {
    id,
    index,
    ids,
    active,
    reloadLabel,
    canReload = true,
    insideEditor = false,
    onreload,
    onclose,
    children,
  }: {
    id: ID;
    index: number;
    ids: ID[];
    active: boolean;
    reloadLabel: Message;
    canReload?: boolean;
    insideEditor?: boolean;
    onreload: (id: ID) => void;
    onclose: (ids: ID[]) => void;
    children: Snippet;
  } = $props();
  let keepPointerFocus = false;
</script>

<ContextMenu.Root>
  <ContextMenu.Trigger
    data-tab-id={id}
    class={`workspace-tab tab-wrap ${active ? 'active' : ''}`}
  >
    {@render children()}
  </ContextMenu.Trigger>
  <ContextMenu.Portal>
    <ContextMenu.Content
      class="menu"
      preventScroll={!insideEditor}
      data-file-window-menu={insideEditor || undefined}
      onInteractOutside={() => (keepPointerFocus = insideEditor)}
      onCloseAutoFocus={(event) => {
        if (keepPointerFocus) event.preventDefault();
        keepPointerFocus = false;
      }}
    >
      <ContextMenu.Item
        class="menu-item"
        disabled={!canReload}
        onSelect={() => onreload(id)}
      >
        <RotateCw size={14} />{t(reloadLabel)}
      </ContextMenu.Item>
      <ContextMenu.Separator class="menu-separator" />
      <ContextMenu.Item class="menu-item" onSelect={() => onclose([id])}>
        {t('closeCurrent')}
      </ContextMenu.Item>
      <ContextMenu.Item
        class="menu-item"
        disabled={index === ids.length - 1}
        onSelect={() => onclose(ids.slice(index + 1))}
      >
        {t('closeRight')}
      </ContextMenu.Item>
      <ContextMenu.Item
        class="menu-item"
        disabled={ids.length === 1}
        onSelect={() => onclose(ids.filter((other) => other !== id))}
      >
        {t('closeOthers')}
      </ContextMenu.Item>
    </ContextMenu.Content>
  </ContextMenu.Portal>
</ContextMenu.Root>
