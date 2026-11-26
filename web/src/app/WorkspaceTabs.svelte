<script lang="ts">
  import { tick } from 'svelte';
  import { sortableTabs, reorderTabs } from '@/shared/tab-sort';
  import { scrollTabs } from '@/shared/tab-scroll';
  import { ContextMenu, DropdownMenu } from 'bits-ui';
  import { X, RotateCw, MoreHorizontal } from '@lucide/svelte';
  import NodeIcon from '@/features/nodes/NodeIcon.svelte';
  import type { Node } from '@/features/nodes/types';
  import { t } from '@/shared/settings.svelte';
  let {
    tabs = $bindable(),
    nodes,
    active = $bindable(),
    onclose,
    onreload,
  }: {
    tabs: { id: string; name: string }[];
    nodes: Node[];
    active: string;
    onclose: (ids: string[]) => void;
    onreload: (id: string) => void;
  } = $props();
  let tabStrip = $state<HTMLDivElement>();
  const tabButtons = new Map<string, HTMLButtonElement>();
  function closeOthers(id: string) {
    onclose(tabs.filter((tab) => tab.id !== id).map((tab) => tab.id));
  }
  function closeRight(id: string) {
    onclose(
      tabs
        .slice(tabs.findIndex((tab) => tab.id === id) + 1)
        .map((tab) => tab.id),
    );
  }
  function closeAll() {
    onclose(tabs.map((tab) => tab.id));
  }
  function tabKeys(event: KeyboardEvent, id: string) {
    const index = tabs.findIndex((tab) => tab.id === id);
    let next: string | undefined;
    if (event.key === 'ArrowRight') next = tabs[(index + 1) % tabs.length]?.id;
    if (event.key === 'ArrowLeft')
      next = tabs[(index - 1 + tabs.length) % tabs.length]?.id;
    if (event.key === 'Home') next = tabs[0]?.id;
    if (event.key === 'End') next = tabs.at(-1)?.id;
    if (next) {
      event.preventDefault();
      active = next;
      tabButtons.get(next)?.focus();
    }
  }
  $effect(() => {
    if (active)
      void tick().then(() =>
        tabButtons
          .get(active)
          ?.scrollIntoView({ block: 'nearest', inline: 'nearest' }),
      );
  });
  $effect(() => {
    if (!tabStrip) return;
    const observer = new ResizeObserver(() =>
      tabButtons
        .get(active)
        ?.scrollIntoView({ block: 'nearest', inline: 'nearest' }),
    );
    observer.observe(tabStrip);
    return () => observer.disconnect();
  });
</script>

<div
  class="tabs-scroll"
  bind:this={tabStrip}
  onwheel={scrollTabs}
  use:sortableTabs={(from, to) => (tabs = reorderTabs(tabs, from, to))}
  role="tablist"
  aria-label={t('terminalHint')}
>
  {#each tabs as tab (tab.id)}
    <ContextMenu.Root>
      <ContextMenu.Trigger
        data-tab-id={tab.id}
        class={`workspace-tab tab-wrap ${active === tab.id ? 'active' : ''}`}
      >
        <button
          class="terminal-tab tab-title"
          role="tab"
          id={`tab-${tab.id}`}
          aria-selected={active === tab.id}
          aria-controls={`panel-${tab.id}`}
          tabindex={active === tab.id ? 0 : -1}
          onclick={() => (active = tab.id)}
          onkeydown={(event) => tabKeys(event, tab.id)}
          bind:this={
            () => tabButtons.get(tab.id),
            (value) => {
              if (value) tabButtons.set(tab.id, value);
              else tabButtons.delete(tab.id);
            }
          }
        >
          <NodeIcon os={nodes.find((node) => node.id === tab.id)?.info.os} />
          <span>{tab.name}</span>
        </button>
        <button
          class="tab-close"
          aria-label={`${t('close')} ${tab.name}`}
          onclick={() => onclose([tab.id])}
        >
          <X size={13} />
        </button>
      </ContextMenu.Trigger><ContextMenu.Portal>
        <ContextMenu.Content class="menu">
          <ContextMenu.Item class="menu-item" onSelect={() => onreload(tab.id)}>
            <RotateCw size={14} />{t('reconnect')}
          </ContextMenu.Item><ContextMenu.Separator class="menu-separator" />
          <ContextMenu.Item
            class="menu-item"
            onSelect={() => onclose([tab.id])}
          >
            {t('closeCurrent')}
          </ContextMenu.Item>
          <ContextMenu.Item
            class="menu-item"
            disabled={tabs.at(-1)?.id === tab.id}
            onSelect={() => closeRight(tab.id)}
          >
            {t('closeRight')}
          </ContextMenu.Item>
          <ContextMenu.Item
            class="menu-item"
            disabled={tabs.length === 1}
            onSelect={() => closeOthers(tab.id)}
          >
            {t('closeOthers')}
          </ContextMenu.Item>
        </ContextMenu.Content>
      </ContextMenu.Portal>
    </ContextMenu.Root>
  {/each}
</div>
<DropdownMenu.Root>
  <DropdownMenu.Trigger
    class="icon-button tabs-overflow"
    aria-label={t('tabMenu')}
  >
    <MoreHorizontal size={18} />
  </DropdownMenu.Trigger><DropdownMenu.Portal>
    <DropdownMenu.Content class="menu" align="end" sideOffset={10}>
      <DropdownMenu.Item class="menu-item" onSelect={() => onclose([active])}>
        {t('closeCurrent')}
      </DropdownMenu.Item><DropdownMenu.Item
        class="menu-item"
        onSelect={closeAll}
      >
        {t('closeAll')}
      </DropdownMenu.Item>
    </DropdownMenu.Content>
  </DropdownMenu.Portal>
</DropdownMenu.Root>
