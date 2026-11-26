<script lang="ts">
  import { DropdownMenu } from 'bits-ui';
  import { Check, ChevronRight } from '@lucide/svelte';
  import type FileEditor from './FileEditor.svelte';
  import TextFormatMenu from './TextFormatMenu.svelte';
  import { t } from '@/shared/settings.svelte';
  let {
    editor,
    online,
    canReload,
    pinned = $bindable(),
    maximized = $bindable(),
    showLineNumbers = $bindable(),
    wrapLines = $bindable(),
    fontSize = $bindable(),
    onreload,
    ondownload,
    onminimize,
    onclose,
  }: {
    editor: FileEditor | undefined;
    online: boolean;
    canReload: boolean;
    pinned: boolean;
    maximized: boolean;
    showLineNumbers: boolean;
    wrapLines: boolean;
    fontSize: number;
    onreload: () => void;
    ondownload: () => void;
    onminimize: () => void;
    onclose: () => void;
  } = $props();
  const status = $derived(editor?.status());
  const modifier = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl+';
  let searchRequested = false;
</script>

<div class="editor-menus">
  <DropdownMenu.Root>
    <DropdownMenu.Trigger class="editor-menu-trigger">
      {t('fileMenu')}
    </DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <DropdownMenu.Content
        preventScroll={false}
        class="menu editor-menu"
        data-file-window-menu
        sideOffset={6}
        align="start"
        onCloseAutoFocus={(event) => {
          if (searchRequested) {
            event.preventDefault();
            editor?.search();
            searchRequested = false;
          }
        }}
      >
        <DropdownMenu.Item
          class="menu-item"
          disabled={!status?.canSave}
          onSelect={() => editor?.save()}
        >
          {t('save')}<kbd>{modifier}S</kbd>
        </DropdownMenu.Item>
        <DropdownMenu.Item
          class="menu-item"
          disabled={!status?.loaded}
          onSelect={() => {
            searchRequested = true;
          }}
        >
          {t('search')}<kbd>{modifier}F</kbd>
        </DropdownMenu.Item>
        <DropdownMenu.Item
          class="menu-item"
          disabled={!canReload}
          onSelect={onreload}
        >
          {t('refreshEditor')}
        </DropdownMenu.Item>
        <DropdownMenu.Item
          class="menu-item"
          disabled={!online}
          onSelect={ondownload}
        >
          {t('downloadFiles')}
        </DropdownMenu.Item>
        <DropdownMenu.Separator class="menu-separator" />
        <TextFormatMenu {editor} kind="encoding" submenu />
        <DropdownMenu.Item
          class="menu-item"
          disabled={!status?.canEdit || !status.bom}
          onSelect={() => editor?.clearBOM()}
        >
          {t('clearBOM')}
        </DropdownMenu.Item>
        <TextFormatMenu {editor} kind="newline" submenu />
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
  <DropdownMenu.Root>
    <DropdownMenu.Trigger class="editor-menu-trigger">
      {t('viewMenu')}
    </DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <DropdownMenu.Content
        preventScroll={false}
        class="menu editor-menu"
        data-file-window-menu
        sideOffset={6}
        align="start"
      >
        <DropdownMenu.CheckboxItem class="menu-item" bind:checked={pinned}>
          {t('pinEditor')}<span class="menu-arrow">
            {#if pinned}<Check size={13} />{/if}
          </span>
        </DropdownMenu.CheckboxItem>
        <DropdownMenu.Item class="menu-item" onSelect={onminimize}>
          {t('minimizeEditor')}
        </DropdownMenu.Item>
        <DropdownMenu.Item
          class="menu-item"
          onSelect={() => (maximized = !maximized)}
        >
          {t(maximized ? 'restoreEditor' : 'maximizeEditor')}
        </DropdownMenu.Item>
        <DropdownMenu.Item class="menu-item" onSelect={onclose}>
          {t('close')}
        </DropdownMenu.Item>
        <DropdownMenu.Separator class="menu-separator" />
        <DropdownMenu.Sub>
          <DropdownMenu.SubTrigger class="menu-item" disabled={!editor}>
            {t('wordWrap')}<span class="editor-menu-value">
              {t(wrapLines ? 'yes' : 'no')}
            </span><ChevronRight size={13} />
          </DropdownMenu.SubTrigger>
          <DropdownMenu.Portal>
            <DropdownMenu.SubContent
              class="menu editor-menu"
              data-file-window-menu
              sideOffset={4}
            >
              <DropdownMenu.RadioGroup
                value={wrapLines ? 'yes' : 'no'}
                onValueChange={(value) => (wrapLines = value === 'yes')}
              >
                {#each ['yes', 'no'] as value}
                  <DropdownMenu.RadioItem class="menu-item" {value}>
                    <span class="check-slot">
                      {#if (wrapLines ? 'yes' : 'no') === value}<Check
                          size={13}
                        />{/if}
                    </span>{t(value as 'yes' | 'no')}
                  </DropdownMenu.RadioItem>
                {/each}
              </DropdownMenu.RadioGroup>
            </DropdownMenu.SubContent>
          </DropdownMenu.Portal>
        </DropdownMenu.Sub>
        <DropdownMenu.Item
          class="menu-item"
          disabled={!editor}
          onSelect={() => (showLineNumbers = !showLineNumbers)}
        >
          {t(showLineNumbers ? 'hideLineNumbers' : 'showLineNumbers')}
        </DropdownMenu.Item>
        <DropdownMenu.Separator class="menu-separator" />
        <DropdownMenu.Item
          class="menu-item"
          disabled={!editor}
          onSelect={() => fontSize++}
        >
          {t('increaseFontSize')}
        </DropdownMenu.Item>
        <DropdownMenu.Item
          class="menu-item"
          disabled={!editor || fontSize <= 8}
          onSelect={() => fontSize--}
        >
          {t('decreaseFontSize')}
        </DropdownMenu.Item>
        <DropdownMenu.Item
          class="menu-item"
          disabled={!editor}
          onSelect={() => (fontSize = 12)}
        >
          {t('defaultFontSize')}
        </DropdownMenu.Item>
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
</div>
