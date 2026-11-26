<script lang="ts">
  import type { Node } from '@/features/nodes/types';
  import type { Transfers } from './store.svelte';
  let { manager }: { manager: Transfers } = $props();
  let files: HTMLInputElement, folders: HTMLInputElement;
  let destination: { node: Node; target: string };
  export function choose(
    node: Node,
    target: string,
    kind: 'files' | 'folder' = 'files',
  ) {
    destination = { node, target };
    (kind === 'folder' ? folders : files).click();
  }
  function selected(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const picked = [...input.files!].map((file) => ({
      file,
      relative: file.webkitRelativePath || file.name,
    }));
    if (picked.length)
      void manager.enqueue(destination.node, destination.target, picked);
    input.value = '';
  }
</script>

<input hidden type="file" multiple bind:this={files} onchange={selected} />
<input
  hidden
  type="file"
  multiple
  webkitdirectory
  bind:this={folders}
  onchange={selected}
/>
