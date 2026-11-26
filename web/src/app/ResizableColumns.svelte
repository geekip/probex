<script lang="ts">
  import type { Snippet } from 'svelte';
  import { PaneGroup, Pane, PaneResizer } from 'paneforge';
  let {
    size = $bindable(),
    min,
    max,
    minRemaining = 0,
    side,
    storageKey,
    label,
    resizerClass,
    class: className,
    first,
    second,
    active = true,
    stacked = false,
  }: {
    size: number;
    min: number;
    max: number;
    minRemaining?: number;
    side: 'start' | 'end';
    storageKey: string;
    label: string;
    resizerClass: string;
    class: string;
    first: Snippet;
    second: Snippet;
    active?: boolean;
    stacked?: boolean;
  } = $props();
  let width = $state(0),
    dragging = false;
  let group: PaneGroup;
  const index = $derived(side === 'start' ? 0 : 1);
  const minimum = $derived(width && !stacked ? (min / width) * 100 : 0);
  const maximum = $derived(
    width && !stacked
      ? (Math.max(min, Math.min(max, width - minRemaining)) / width) * 100
      : 100,
  );
  const percentage = $derived(
    width && !stacked
      ? Math.max(minimum, Math.min(maximum, (size / width) * 100))
      : 50,
  );
  function resized(layout: number[]) {
    if (!active || stacked || !width) return;
    size = Math.round((layout[index] / 100) * width);
  }
  function persist() {
    localStorage.setItem(storageKey, String(size));
  }
</script>

<PaneGroup
  direction="horizontal"
  class={className}
  bind:this={group}
  keyboardResizeBy={width ? 1600 / width : 0}
  onLayoutChange={(layout) => {
    if (dragging) resized(layout);
  }}
>
  {#snippet child({ props })}
    <div {...props} bind:clientWidth={width}>
      <Pane
        order={0}
        class="workspace-column"
        defaultSize={index === 0 ? percentage : 100 - percentage}
        minSize={index === 0 ? minimum : 0}
        maxSize={index === 0 ? maximum : 100}
      >
        {@render first()}
      </Pane>
      <PaneResizer
        class={resizerClass}
        aria-label={label}
        disabled={!active || stacked}
        tabindex={active && !stacked ? 0 : -1}
        onDraggingChange={(value) => {
          dragging = value;
          if (!value) persist();
        }}
        onkeydown={(event) => {
          if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key))
            queueMicrotask(() => {
              resized(group.getLayout());
              persist();
            });
        }}
      />
      <Pane
        order={1}
        class="workspace-column"
        defaultSize={index === 1 ? percentage : 100 - percentage}
        minSize={index === 1 ? minimum : 0}
        maxSize={index === 1 ? maximum : 100}
      >
        {@render second()}
      </Pane>
    </div>
  {/snippet}
</PaneGroup>
