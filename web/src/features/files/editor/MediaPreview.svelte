<script lang="ts">
  import type { OpenedFile } from '@/features/files/editor/FileWindow.svelte';
  import type { Transfers } from '@/features/transfers/store.svelte';
  import EditorNotice from './EditorNotice.svelte';
  import { t } from '@/shared/settings.svelte';
  let {
    file,
    active,
    manager,
    onerror,
  }: {
    file: OpenedFile;
    active: boolean;
    manager: Transfers;
    onerror: (message: string) => void;
  } = $props();
  let error = $state(''),
    generation = $state(0),
    media = $state<HTMLMediaElement>(),
    time = 0;
  const source = $derived(
    manager.owner
      ? `/api/nodes/${file.node.id}/preview?${new URLSearchParams({ path: file.entry.path, file_session: manager.owner })}`
      : '',
  );
  export function reload() {
    error = '';
    generation++;
  }
  function failed() {
    if (active) error = t('mediaLoadError');
  }
  $effect.pre(() => {
    if (!active && media) {
      time = media.currentTime;
      media.pause();
      media.removeAttribute('src');
      media.load();
    }
  });
  function restore() {
    if (media && time && Number.isFinite(media.duration))
      media.currentTime = Math.min(time, media.duration);
  }
</script>

<div class="media-preview">
  <div class="editor-notices">
    {#if !file.node.online}<EditorNotice
        message={t('nodeOffline')}
        tone="warning"
      />{/if}
    {#if !source}<EditorNotice
        message={t('fileSessionEnded')}
        tone="error"
      />{/if}
    {#if error}<EditorNotice message={error} tone="error" />{/if}
  </div>
  {#if file.kind === 'pdf'}{#await import('@/features/files/editor/PDFPreview.svelte')}<EditorNotice
        message={t('loading')}
      />{:then pdf}<pdf.default
        {source}
        active={active && !!source}
        revision={generation}
        {onerror}
      />{:catch failure}<EditorNotice
        message={failure.message}
        tone="error"
      />{/await}
  {:else}<div class="media-preview-stage">
      {#key generation}
        {#if file.kind === 'image'}{#if active && source}<img
              src={source}
              alt={file.entry.name}
              onerror={failed}
            />{/if}
        {:else if file.kind === 'audio'}<!-- svelte-ignore a11y_media_has_caption (Caption tracks are not supplied by remote files.) --><audio
            controls
            preload="metadata"
            src={active && source ? source : undefined}
            bind:this={media}
            onloadedmetadata={restore}
            onerror={failed}
          >
            {t('mediaLoadError')}
          </audio>
        {:else if file.kind === 'video'}<!-- svelte-ignore a11y_media_has_caption (Caption tracks are not supplied by remote files.) --><video
            controls
            preload="metadata"
            src={active && source ? source : undefined}
            bind:this={media}
            onloadedmetadata={restore}
            onerror={failed}
          >
            {t('mediaLoadError')}
          </video>{/if}
      {/key}
    </div>{/if}
</div>
