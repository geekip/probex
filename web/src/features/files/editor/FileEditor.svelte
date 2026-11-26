<script lang="ts">
  import { onMount } from 'svelte';
  import { basicSetup } from 'codemirror';
  import { Compartment, EditorState } from '@codemirror/state';
  import { EditorView, keymap } from '@codemirror/view';
  import { indentWithTab } from '@codemirror/commands';
  import { openSearchPanel, closeSearchPanel } from '@codemirror/search';
  import { LanguageDescription } from '@codemirror/language';
  import { languages } from '@codemirror/language-data';
  import EditorNotice from './EditorNotice.svelte';
  import FileLoadFeedback from './FileLoadFeedback.svelte';
  import PendingLabel from '@/shared/components/PendingLabel.svelte';
  import { FileError, fileJSON } from '@/features/files/api';
  import type { OpenedFile } from '@/features/files/editor/FileWindow.svelte';
  import type { Confirmation } from '@/shared/components/ConfirmDialog.svelte';
  import type { Transfers } from '@/features/transfers/store.svelte';
  import {
    editorDocument,
    editorContent,
    sameEditorContent,
    newlineLabel,
    chooseNewline,
    newlineState,
  } from '@/features/files/editor/newlines';
  import { t, dark, language } from '@/shared/settings.svelte';
  type TextDocument = {
    path: string;
    size: number;
    text: string;
    encoding: 'utf-8' | 'gbk';
    bom: boolean;
    version: string;
    readonly: boolean;
    download_only: boolean;
  };
  let {
    file,
    active,
    manager,
    onrequestconfirm,
    onerror,
    showLineNumbers,
    wrapLines,
    fontSize,
  }: {
    file: OpenedFile;
    active: boolean;
    manager: Transfers;
    onrequestconfirm: (request: Confirmation) => void;
    onerror: (message: string) => void;
    showLineNumbers: boolean;
    wrapLines: boolean;
    fontSize: number;
  } = $props();
  let host: HTMLDivElement;
  let view: EditorView | undefined;
  let savedState: EditorState;
  let document = $state<TextDocument>(),
    loadError = $state(''),
    error = $state(''),
    conflict = $state(false),
    hasLongLines = $state(false);
  let encoding = $state<'utf-8' | 'gbk'>('utf-8'),
    bom = $state(false),
    separator = $state('LF'),
    line = $state(1);
  let controller: AbortController | undefined,
    disposed = false;
  const input = new Compartment(),
    theme = new Compartment(),
    syntax = new Compartment(),
    phrases = new Compartment(),
    wrapping = new Compartment();
  const readonly = $derived(document?.readonly ?? true);
  export function status() {
    const editable =
      !!document && !readonly && !file.loading && !file.saving && !loadError;
    return {
      encoding,
      bom,
      separator,
      line,
      loaded: !!document && !file.loading && !loadError,
      readonly: document?.readonly ?? false,
      canEdit: editable,
      canWrap: !hasLongLines,
      canSave:
        editable && file.node.online && !file.missing && !file.nodeMissing,
      canReload: !file.loading && !file.saving && file.node.online,
    };
  }
  export function search() {
    if (view) openSearchPanel(view);
  }
  export function clearBOM() {
    bom = false;
    changed();
  }
  export function saveEncoding(next: 'utf-8' | 'gbk') {
    encoding = next;
    if (next === 'gbk') bom = false;
    changed();
    return save();
  }
  export function setNewline(next: string) {
    view?.dispatch({
      effects: chooseNewline.of(
        next === 'CRLF' ? '\r\n' : next === 'CR' ? '\r' : '\n',
      ),
    });
  }
  export function closeSearch() {
    if (!view || !closeSearchPanel(view)) return false;
    view.focus();
    return true;
  }
  function changed() {
    if (!document || !view) return;
    file.dirty =
      !sameEditorContent(view.state, savedState) ||
      encoding !== document.encoding ||
      bom !== document.bom;
    separator = newlineLabel(view.state);
  }
  function editorTheme() {
    return EditorView.theme(
      {
        '&': {
          height: '100%',
          fontSize: fontSize + 'px',
          backgroundColor: 'var(--surface)',
          color: 'var(--text)',
        },
        '.cm-scroller': {
          fontFamily: 'SFMono-Regular, Consolas, monospace',
          overflow: 'auto',
        },
        '.cm-lineNumbers': {
          display: showLineNumbers ? 'flex !important' : 'none !important',
        },
        '.cm-gutters': {
          backgroundColor: 'var(--bg)',
          color: 'var(--muted)',
          borderRight: '1px solid var(--border)',
        },
        '.cm-activeLine, .cm-activeLineGutter': {
          backgroundColor: 'color-mix(in srgb, var(--accent) 7%, transparent)',
        },
        '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--text)' },
        '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
          backgroundColor: 'color-mix(in srgb, var(--accent) 20%, transparent)',
        },
        '.cm-panels': { backgroundColor: 'var(--bg)', color: 'var(--text)' },
      },
      { dark: dark() },
    );
  }
  function editorPhrases() {
    return EditorState.phrases.of(
      language() === 'zh'
        ? {
            Find: '查找',
            Replace: '替换',
            next: '下一个',
            previous: '上一个',
            all: '全部',
            'match case': '区分大小写',
            regexp: '正则表达式',
            'by word': '全字匹配',
            replace: '替换',
            'replace all': '全部替换',
            close: '关闭',
            'Go to line': '跳转到行',
            go: '跳转',
            'current match': '当前匹配',
            'on line': '位于行',
          }
        : {},
    );
  }
  function inputMode(locked: boolean) {
    return [EditorState.readOnly.of(locked), EditorView.editable.of(!locked)];
  }
  async function load(requested = 'auto') {
    controller?.abort();
    const request = new AbortController();
    controller = request;
    file.loading = true;
    loadError = '';
    view?.dispatch({ effects: input.reconfigure(inputMode(true)) });
    error = '';
    conflict = false;
    try {
      const next = await fileJSON<TextDocument>(
        `nodes/${file.node.id}/text?${new URLSearchParams({ path: file.entry.path, encoding: requested })}`,
        { signal: request.signal },
        await manager.session(),
      );
      if (disposed || request.signal.aborted) return;
      if (next.download_only) {
        file.entry.size = next.size;
        file.dirty = false;
        file.kind = 'download';
        return;
      }
      document = next;
      file.missing = false;
      encoding = next.encoding;
      bom = next.bom;
      file.entry.size = next.size;
      file.dirty = false;
      const source = editorDocument(next.text);
      hasLongLines = source.hasLongLines;
      const state = EditorState.create({
        doc: source.doc,
        extensions: [
          basicSetup,
          wrapping.of(
            wrapLines && !hasLongLines ? EditorView.lineWrapping : [],
          ),
          source.extensions,
          theme.of(editorTheme()),
          syntax.of([]),
          phrases.of(editorPhrases()),
          input.of(inputMode(true)),
          EditorView.contentAttributes.of({
            'aria-label': file.entry.name,
            tabindex: '0',
          }),
          keymap.of([
            {
              key: 'Mod-s',
              run: () => {
                if (
                  !readonly &&
                  !file.loading &&
                  !loadError &&
                  !file.missing &&
                  !file.nodeMissing &&
                  !file.saving &&
                  file.node.online
                )
                  void save();
                return true;
              },
            },
            indentWithTab,
          ]),
          EditorView.updateListener.of((update) => {
            if (update.selectionSet || update.docChanged)
              line = update.state.doc.lineAt(
                update.state.selection.main.head,
              ).number;
            if (
              update.docChanged ||
              update.startState.field(newlineState) !==
                update.state.field(newlineState)
            )
              changed();
          }),
        ],
      });
      savedState = state;
      view?.destroy();
      const created = new EditorView({ state, parent: host });
      view = created;
      separator = newlineLabel(state);
      line = 1;
      const language = LanguageDescription.matchFilename(
        languages,
        file.entry.name,
      );
      if (language && !source.hasLongLines)
        void language
          .load()
          .then((support) => {
            if (!disposed && view === created)
              view.dispatch({ effects: syntax.reconfigure(support) });
          })
          .catch((failure) => {
            if (!disposed && view === created)
              error = (failure as Error).message;
          });
    } catch (failure) {
      if (!request.signal.aborted) {
        if (
          failure instanceof FileError &&
          failure.code === 'invalid_text' &&
          !document
        ) {
          file.kind = 'download';
        } else loadError = (failure as Error).message;
      }
    } finally {
      if (controller === request) {
        file.loading = false;
        if (!disposed)
          view?.dispatch({ effects: input.reconfigure(inputMode(readonly)) });
      }
    }
  }
  export function reload(requested = 'auto') {
    if (file.dirty)
      onrequestconfirm({
        title: 'reloadFile',
        description: 'confirmDiscardEdits',
        confirmLabel: 'reloadFile',
        detail: file.node.name + ' · ' + file.entry.path,
        action: () => load(requested),
      });
    else void load(requested);
  }
  export async function save(overwrite = false) {
    if (
      !document ||
      !view ||
      readonly ||
      !file.node.online ||
      file.missing ||
      file.nodeMissing ||
      file.loading ||
      loadError ||
      file.saving
    )
      return;
    const savingState = view.state,
      text = editorContent(savingState),
      selectedEncoding = encoding,
      selectedBOM = bom;
    // Reject unpaired UTF-16 surrogates before JSON would replace them on the server.
    if (
      /[\uD800-\uDFFF]/u.test(
        text.replace(/[\uD800-\uDBFF][\uDC00-\uDFFF]/g, ''),
      )
    ) {
      error = t('invalidUnicode');
      onerror(t('fileSaveFailed').replace('{name}', () => file.entry.name));
      return;
    }
    controller?.abort();
    const request = new AbortController();
    controller = request;
    file.saving = true;
    error = '';
    try {
      const saved = await fileJSON<TextDocument>(
        `nodes/${file.node.id}/text?${new URLSearchParams({ path: file.entry.path })}`,
        {
          method: 'PUT',
          body: JSON.stringify({
            text,
            encoding: selectedEncoding,
            bom: selectedBOM,
            version: document.version,
            overwrite,
          }),
          signal: request.signal,
        },
        await manager.session(),
      );
      if (disposed || request.signal.aborted) return;
      savedState = savingState;
      document = saved;
      file.missing = false;
      file.entry.size = saved.size;
      conflict = false;
      changed();
    } catch (failure) {
      if (!request.signal.aborted) {
        error = (failure as Error).message;
        conflict =
          failure instanceof FileError && failure.code === 'version_conflict';
        onerror(t('fileSaveFailed').replace('{name}', () => file.entry.name));
      }
    } finally {
      file.saving = false;
    }
  }
  function overwrite() {
    onrequestconfirm({
      title: 'overwriteFile',
      description: 'confirmOverwriteFile',
      confirmLabel: 'overwrite',
      detail: file.node.name + ' · ' + file.entry.path,
      danger: true,
      action: () => save(true),
    });
  }
  $effect(() => {
    language();
    view?.dispatch({ effects: phrases.reconfigure(editorPhrases()) });
  });
  $effect(() => {
    const appearance = editorTheme();
    view?.dispatch({ effects: theme.reconfigure(appearance) });
  });
  $effect(() => {
    const extension = wrapLines && !hasLongLines ? EditorView.lineWrapping : [];
    view?.dispatch({ effects: wrapping.reconfigure(extension) });
  });
  $effect(() => {
    if (active && !file.loading) view?.requestMeasure();
  });
  onMount(() => {
    void load();
    return () => {
      disposed = true;
      controller?.abort();
      view?.destroy();
    };
  });
</script>

<div class="text-editor">
  <div class="editor-notices">
    {#if error}<EditorNotice message={error} tone="error">
        {#if conflict}<button class="quiet-button" onclick={() => reload()}>
            {t('reloadFile')}
          </button><button
            class="quiet-button danger"
            disabled={!file.node.online || file.saving || file.loading}
            onclick={overwrite}
          >
            {#if file.saving}<PendingLabel />{:else}{t('overwriteFile')}{/if}
          </button>{/if}
      </EditorNotice>{/if}
    {#if file.missing || file.nodeMissing}<EditorNotice
        message={t('editorFileMissing')}
        tone="warning"
      />{/if}
    {#if !file.node.online}<EditorNotice
        message={t('editorOffline')}
        tone="warning"
      />{/if}
    {#if hasLongLines && !file.loading && !loadError}
      <EditorNotice message={t('longLineDisplay')} />
    {/if}
    {#if readonly && document}<EditorNotice message={t('textReadOnly')} />{/if}
  </div>
  {#if file.loading || loadError}
    <FileLoadFeedback
      error={loadError}
      onretry={() => reload()}
      disabled={!file.node.online}
    />
  {/if}
  <div
    class="editor-host"
    bind:this={host}
    hidden={file.loading || !!loadError}
  ></div>
</div>
