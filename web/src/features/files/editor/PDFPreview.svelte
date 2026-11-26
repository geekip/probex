<script lang="ts">
  import EditorNotice from './EditorNotice.svelte';
  import { onMount } from 'svelte';
  import { ChevronLeft, ChevronRight, Minus, Plus } from '@lucide/svelte';
  import type {
    PDFDocumentProxy,
    PDFDocumentLoadingTask,
    RenderTask,
  } from 'pdfjs-dist';
  import { t } from '@/shared/settings.svelte';
  let {
    source,
    active,
    revision,
    onerror,
  }: {
    source: string;
    active: boolean;
    revision: number;
    onerror: (message: string) => void;
  } = $props();
  let document = $state.raw<PDFDocumentProxy>(),
    page = $state(1),
    zoom = $state(1),
    loading = $state(false),
    error = $state(''),
    width = $state(0);
  let container = $state<HTMLDivElement>(),
    canvas = $state<HTMLCanvasElement>();
  let loadingTask: PDFDocumentLoadingTask | undefined,
    renderTask: RenderTask | undefined,
    renderSequence = 0;
  $effect(() => {
    revision;
    if (!active || !source) return;
    let cancelled = false;
    loading = true;
    error = '';
    void (async () => {
      try {
        const pdf = await import('pdfjs-dist');
        const worker = await import('pdfjs-dist/build/pdf.worker.min.mjs?url');
        if (cancelled) return;
        pdf.GlobalWorkerOptions.workerSrc = worker.default;
        const task = pdf.getDocument({
          url: source,
          cMapUrl: '/pdfjs/cmaps/',
          cMapPacked: true,
          standardFontDataUrl: '/pdfjs/standard_fonts/',
          wasmUrl: '/pdfjs/wasm/',
          iccUrl: '/pdfjs/iccs/',
          disableAutoFetch: true,
          disableStream: true,
          enableXfa: false,
        });
        loadingTask = task;
        const loaded = await task.promise;
        if (cancelled) return;
        page = Math.min(page, loaded.numPages);
        document = loaded;
      } catch (failure) {
        if (!cancelled) error = (failure as Error).message;
      } finally {
        if (!cancelled) loading = false;
      }
    })();
    return () => {
      cancelled = true;
      document = undefined;
      renderSequence++;
      renderTask?.cancel();
      if (loadingTask) {
        void loadingTask
          .destroy()
          .catch((failure) => onerror((failure as Error).message));
        loadingTask = undefined;
      }
    };
  });
  $effect(() => {
    if (!document || !canvas || !active || !width) return;
    const pdf = document,
      target = canvas,
      number = page,
      scale = zoom,
      available = width,
      sequence = ++renderSequence;
    const previous = renderTask;
    previous?.cancel();
    void (async () => {
      try {
        if (previous)
          await previous.promise.catch((failure) => {
            if (failure.name !== 'RenderingCancelledException') throw failure;
          });
        const sheet = await pdf.getPage(number);
        if (sequence !== renderSequence) return;
        const original = sheet.getViewport({ scale: 1 }),
          viewport = sheet.getViewport({
            scale: Math.min((available - 24) / original.width, 1) * scale,
          });
        const ratio = devicePixelRatio || 1;
        target.width = Math.floor(viewport.width * ratio);
        target.height = Math.floor(viewport.height * ratio);
        target.style.width = viewport.width + 'px';
        target.style.height = viewport.height + 'px';
        renderTask = sheet.render({
          canvas: target,
          viewport,
          transform: ratio === 1 ? undefined : [ratio, 0, 0, ratio, 0, 0],
        });
        await renderTask.promise;
      } catch (failure) {
        if (sequence === renderSequence && active)
          error = (failure as Error).message;
      }
    })();
  });
  onMount(() => {
    const observer = new ResizeObserver(() => {
      width = container!.clientWidth;
    });
    observer.observe(container!);
    return () => observer.disconnect();
  });
</script>

<div class="pdf-preview">
  <div class="pdf-controls">
    <button
      class="icon-button"
      aria-label={t('previousPage')}
      title={t('previousPage')}
      disabled={!document || page <= 1}
      onclick={() => page--}
    >
      <ChevronLeft size={15} />
    </button><span>{page} / {document?.numPages || '—'}</span><button
      class="icon-button"
      aria-label={t('nextPage')}
      title={t('nextPage')}
      disabled={!document || page >= document.numPages}
      onclick={() => page++}
    >
      <ChevronRight size={15} />
    </button><button
      class="icon-button"
      aria-label={t('zoomOut')}
      title={t('zoomOut')}
      disabled={!document || zoom <= 0.5}
      onclick={() => (zoom = Math.max(0.5, zoom - 0.25))}
    >
      <Minus size={14} />
    </button><span>{Math.round(zoom * 100)}%</span><button
      class="icon-button"
      aria-label={t('zoomIn')}
      title={t('zoomIn')}
      disabled={!document || zoom >= 3}
      onclick={() => (zoom = Math.min(3, zoom + 0.25))}
    >
      <Plus size={14} />
    </button>
  </div>
  <div class="editor-notices">
    {#if loading}<EditorNotice message={t('loading')} />{/if}
    {#if error}<EditorNotice
        message={error + ' · ' + t('pdfLoadError')}
        tone="error"
      />{/if}
  </div>
  <div class="pdf-canvas-scroll" bind:this={container}>
    <canvas bind:this={canvas} aria-label={t('pdfPage')}></canvas>
  </div>
</div>
