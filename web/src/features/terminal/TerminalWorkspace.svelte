<script lang="ts">
  import type TerminalPane from '@/features/terminal/Terminal.svelte';
  import { onMount } from 'svelte';
  import TerminalLayout from '@/features/terminal/TerminalLayout.svelte';
  import {
    replace,
    remove,
    type Axis,
    type Layout,
  } from '@/features/terminal/layout';
  import type { Confirmation } from '@/shared/components/ConfirmDialog.svelte';
  import { t } from '@/shared/settings.svelte';

  let {
    nodeID,
    name,
    active,
    online,
    onerror,
    onrequestconfirm,
  }: {
    nodeID: string;
    name: string;
    active: boolean;
    online: boolean;
    onerror: (message: string) => void;
    onrequestconfirm: (request: Confirmation) => void;
  } = $props();
  let nextID = 1;
  let layout = $state<Layout>({ id: 'p1' });
  let panes = $state([{ id: 'p1' }]);
  let selected = $state('p1');
  let container: HTMLDivElement;
  let positions = $state<
    Record<string, { x: number; y: number; width: number; height: number }>
  >({});
  let dragging = $state(false);
  let Terminal = $state<typeof TerminalPane>();
  const slots = new Map<string, HTMLDivElement>();
  const layouts = new Map<string, number[]>();
  let frame = 0;
  function measure() {
    frame = 0;
    const origin = container.getBoundingClientRect();
    // A hidden tab has no layout. Keep its last pane positions for the first visible frame.
    if (!origin.width || !origin.height) return;
    positions = Object.fromEntries(
      [...slots].map(([id, element]) => {
        const rect = element.getBoundingClientRect();
        return [
          id,
          {
            x: rect.x - origin.x,
            y: rect.y - origin.y,
            width: rect.width,
            height: rect.height,
          },
        ];
      }),
    );
  }
  function schedule() {
    if (!frame) frame = requestAnimationFrame(measure);
  }
  function register(element: HTMLDivElement, id: string) {
    slots.set(id, element);
    const observer = new ResizeObserver(schedule);
    observer.observe(element);
    schedule();
    return {
      destroy() {
        observer.disconnect();
        if (slots.get(id) === element) slots.delete(id);
      },
    };
  }
  function layoutChanged(id: string, sizes: number[]) {
    layouts.set(id, sizes);
    schedule();
  }
  onMount(() => {
    void import('@/features/terminal/Terminal.svelte')
      .then(({ default: component }) => (Terminal = component))
      .catch((error: Error) => onerror(error.message));
    const observer = new ResizeObserver(schedule);
    observer.observe(container);
    schedule();
    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
    };
  });
  const instances = new Map<string, TerminalPane>();
  export function hasSession() {
    return panes.some((p) => instances.get(p.id)?.hasSession());
  }
  export function reload() {
    instances.get(selected)?.reload();
  }
  function split(id: string, axis: Axis) {
    if (panes.length === 8) {
      onerror(t('terminalLimit'));
      return;
    }
    const next = `p${++nextID}`;
    layout = replace(layout, id, {
      id: `b${nextID}`,
      axis,
      children: [{ id }, { id: next }],
    });
    panes = [...panes, { id: next }];
    selected = next;
  }
  function closePane(id: string) {
    const close = () => {
      layout = remove(layout, id)!;
      panes = panes.filter((p) => p.id !== id);
      if (selected === id) selected = panes.at(-1)!.id;
    };
    if (instances.get(id)?.hasSession())
      onrequestconfirm({
        title: 'closePane',
        description: 'confirmClosePane',
        confirmLabel: 'closePane',
        detail: name,
        action: close,
      });
    else close();
  }
</script>

<div class="terminal-layout" class:resizing={dragging} bind:this={container}>
  <TerminalLayout
    node={layout}
    scope={nodeID}
    {layouts}
    {register}
    onlayout={layoutChanged}
    ondrag={(value) => (dragging = value)}
  />
  {#each panes as pane (pane.id)}
    {@const rect = positions[pane.id]}
    <div
      class="split-pane"
      data-pane={pane.id}
      style:visibility={rect ? 'visible' : 'hidden'}
      style:left={`${rect?.x ?? 0}px`}
      style:top={`${rect?.y ?? 0}px`}
      style:width={`${rect?.width ?? 0}px`}
      style:height={`${rect?.height ?? 0}px`}
    >
      {#if Terminal}<Terminal
          {nodeID}
          {name}
          {active}
          {online}
          {onerror}
          {onrequestconfirm}
          focused={active && selected === pane.id}
          splitDisabled={panes.length >= 8}
          closeVisible={panes.length > 1}
          onactivate={() => (selected = pane.id)}
          onsplit={(axis) => split(pane.id, axis)}
          onclose={() => closePane(pane.id)}
          bind:this={
            () => instances.get(pane.id),
            (value) => {
              if (value) instances.set(pane.id, value);
              else instances.delete(pane.id);
            }
          }
        />{/if}
    </div>
  {/each}
</div>
