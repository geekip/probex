<script lang="ts">
  import { tick } from 'svelte';
  import { sortableTabs, reorderTabs } from '@/shared/tab-sort';
  import { scrollTabs } from '@/shared/tab-scroll';
  import { X } from '@lucide/svelte';
  import TabContextMenu from '@/shared/components/TabContextMenu.svelte';
  import TabOverflowMenu from '@/shared/components/TabOverflowMenu.svelte';
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
  const ids = $derived(tabs.map((tab) => tab.id));
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
  {#each tabs as tab, index (tab.id)}
    <TabContextMenu
      id={tab.id}
      {index}
      {ids}
      active={active === tab.id}
      reloadLabel="reconnect"
      {onreload}
      {onclose}
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
    </TabContextMenu>
  {/each}
</div>
<TabOverflowMenu {ids} {active} {onclose} />
