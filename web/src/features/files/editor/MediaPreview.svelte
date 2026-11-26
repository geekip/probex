<script lang="ts">
  import type { OpenedFile } from '@/features/files/editor/FileWindow.svelte';
  import type { Transfers } from '@/features/transfers/store.svelte';
  import EditorNotice from './EditorNotice.svelte';
  import FileLoadFeedback from './FileLoadFeedback.svelte';
  import PDFPreview from './PDFPreview.svelte';
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
    loading = $state(true),
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
    loading = true;
    generation++;
  }
  function failed(message = t('mediaLoadError')) {
    if (!active) return;
    error = message;
    loading = false;
  }
  $effect.pre(() => {
    generation;
    if (active && file.kind !== 'pdf') {
      loading = true;
      error = '';
    }
    if (!active && media) {
      time = media.currentTime;
      media.pause();
      media.removeAttribute('src');
      media.load();
    }
  });
  $effect(() => {
    if (active && !source)
      void manager
        .session()
        .catch((failure) => failed((failure as Error).message));
    generation;
  });
  $effect(() => {
    file.loading = active && loading;
  });
  function restore() {
    if (media && time && Number.isFinite(media.duration))
      media.currentTime = Math.min(time, media.duration);
    loading = false;
  }
</script>

<div class="media-preview">
  <div class="editor-notices">
    {#if !file.node.online}<EditorNotice
        message={t('nodeOffline')}
        tone="warning"
      />{/if}
  </div>
  {#if !source}
    <FileLoadFeedback {error} onretry={reload} disabled={!file.node.online} />
  {:else if file.kind === 'pdf'}
    <PDFPreview
      {source}
      {active}
      revision={generation}
      {onerror}
      onreload={reload}
      bind:loading
    />
  {:else}
    {#if loading || error}<FileLoadFeedback
        {error}
        onretry={reload}
        disabled={!file.node.online}
      />{/if}
    <div class="media-preview-stage" hidden={loading || !!error}>
      {#key generation}
        {#if file.kind === 'image'}{#if active && source}<img
              src={source}
              alt={file.entry.name}
              onload={() => (loading = false)}
              onerror={() => failed()}
            />{/if}
        {:else if file.kind === 'audio'}<!-- svelte-ignore a11y_media_has_caption (Caption tracks are not supplied by remote files.) --><audio
            controls
            preload="metadata"
            src={active && source ? source : undefined}
            bind:this={media}
            onloadedmetadata={restore}
            onerror={() => failed()}
          >
            {t('mediaLoadError')}
          </audio>
        {:else if file.kind === 'video'}<!-- svelte-ignore a11y_media_has_caption (Caption tracks are not supplied by remote files.) --><video
            controls
            preload="metadata"
            src={active && source ? source : undefined}
            bind:this={media}
            onloadedmetadata={restore}
            onerror={() => failed()}
          >
            {t('mediaLoadError')}
          </video>{/if}
      {/key}
    </div>{/if}
</div>
