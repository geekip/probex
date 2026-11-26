<script lang="ts">
  import type { PickedFile } from '@/features/transfers/store.svelte';
  let {
    onselect,
  }: { onselect: (target: string, files: PickedFile[]) => void } = $props();
  let files: HTMLInputElement, folders: HTMLInputElement;
  let destination: string;
  export function choose(target: string, kind: 'files' | 'folder' = 'files') {
    destination = target;
    (kind === 'folder' ? folders : files).click();
  }
  function selected(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const picked = [...input.files!].map((file) => ({
      file,
      relative: file.webkitRelativePath || file.name,
    }));
    input.value = '';
    if (picked.length) onselect(destination, picked);
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
