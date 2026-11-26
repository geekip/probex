<script lang="ts">
  import { PaneGroup, Pane, PaneResizer } from 'paneforge';
  import type { Action } from 'svelte/action';
  import TerminalLayout from '@/features/terminal/TerminalLayout.svelte';
  import type { Layout } from '@/features/terminal/layout';
  import { t } from '@/shared/settings.svelte';

  let {
    node,
    scope,
    layouts,
    register,
    onlayout,
    ondrag,
  }: {
    node: Layout;
    scope: string;
    layouts: Map<string, number[]>;
    register: Action<HTMLDivElement, string>;
    onlayout: (id: string, sizes: number[]) => void;
    ondrag: (dragging: boolean) => void;
  } = $props();
</script>

{#key node.id}
  {#if 'axis' in node}
    <PaneGroup
      id={`${scope}-${node.id}`}
      direction={node.axis}
      class="pane-group"
      keyboardResizeBy={5}
      onLayoutChange={(sizes) => onlayout(node.id, sizes)}
    >
      {#each node.children as child, index (child.id)}
        {#if index}<PaneResizer
            class={`pane-divider ${node.axis === 'horizontal' ? 'horizontal' : ''}`}
            aria-label={t('resizePane')}
            onDraggingChange={ondrag}
          />{/if}
        <Pane
          order={index}
          defaultSize={layouts.get(node.id)?.[index] ?? 50}
          minSize={10}
          class="layout-cell"
        >
          <TerminalLayout
            node={child}
            {scope}
            {layouts}
            {register}
            {onlayout}
            {ondrag}
          />
        </Pane>
      {/each}
    </PaneGroup>
  {:else}
    <div class="layout-slot" use:register={node.id}></div>
  {/if}
{/key}
