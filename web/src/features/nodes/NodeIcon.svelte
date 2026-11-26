<script lang="ts">
  import { Server, TerminalSquare } from '@lucide/svelte';
  import windows from '../../../assets/windows.svg?raw';
  import linux from '../../../assets/linux.svg?raw';
  import mac from '../../../assets/mac.svg?raw';

  let {
    os = '',
    size = 14,
    fallback = 'terminal',
  }: {
    os?: string;
    size?: number;
    fallback?: 'server' | 'terminal';
  } = $props();
  const icons = new Map([
    ['windows', windows],
    ['linux', linux],
    ['darwin', mac],
  ]);
  const icon = $derived(icons.get(os));
</script>

{#if icon}
  <span
    class="node-os-icon"
    data-os={os}
    style:width={`${size}px`}
    style:height={`${size}px`}
    aria-hidden="true"
  >
    {@html icon}
  </span>
{:else if fallback === 'server'}
  <Server {size} />
{:else}
  <TerminalSquare {size} />
{/if}

<style>
  .node-os-icon {
    display: inline-flex;
    flex-shrink: 0;
  }
  .node-os-icon :global(svg) {
    display: block;
    width: 100%;
    height: 100%;
  }
  .node-os-icon :global(path) {
    fill: currentColor;
  }
</style>
