<script lang="ts">
  import { DropdownMenu } from 'bits-ui';
  import { Check, ChevronRight } from '@lucide/svelte';
  import type FileEditor from './FileEditor.svelte';
  import { t } from '@/shared/settings.svelte';
  let {
    editor,
    kind,
    submenu = false,
  }: {
    editor: FileEditor | undefined;
    kind: 'encoding' | 'newline';
    submenu?: boolean;
  } = $props();
  const status = $derived(editor?.status());
  const value = $derived(
    kind === 'encoding'
      ? status?.encoding.toUpperCase()
      : status?.separator === 'Mixed'
        ? t('mixedNewlines')
        : status?.separator,
  );
</script>

{#snippet choices()}
  {#if kind === 'encoding'}
    {#each ['reopen', 'save'] as action, index}
      {#if index}<DropdownMenu.Separator class="menu-separator" />{/if}
      <DropdownMenu.Group>
        <DropdownMenu.GroupHeading class="menu-label">
          {t(action === 'reopen' ? 'reopenEncoding' : 'saveEncoding')}
        </DropdownMenu.GroupHeading>
        {#each ['utf-8', 'gbk'] as const as encoding}
          <DropdownMenu.Item
            class="menu-item"
            disabled={action === 'save' ? !status?.canSave : !status?.canReload}
            onSelect={() =>
              action === 'reopen'
                ? editor?.reload(encoding)
                : editor?.saveEncoding(encoding)}
          >
            <span class="check-slot">
              {#if status?.encoding === encoding}<Check size={13} />{/if}
            </span>{encoding.toUpperCase()}
          </DropdownMenu.Item>
        {/each}
      </DropdownMenu.Group>
    {/each}
  {:else}
    <DropdownMenu.RadioGroup
      value={status?.separator}
      onValueChange={(value) => editor?.setNewline(value)}
    >
      {#each ['LF', 'CRLF', 'CR'] as newline}
        <DropdownMenu.RadioItem
          class="menu-item"
          value={newline}
          disabled={!status?.canEdit}
        >
          <span class="check-slot">
            {#if status?.separator === newline}<Check size={13} />{/if}
          </span>{newline}
        </DropdownMenu.RadioItem>
      {/each}
    </DropdownMenu.RadioGroup>
  {/if}
{/snippet}

{#if submenu}
  <DropdownMenu.Sub>
    <DropdownMenu.SubTrigger class="menu-item" disabled={!status?.loaded}>
      {t(kind === 'encoding' ? 'encoding' : 'lineEnding')}<span
        class="editor-menu-value"
      >
        {value}
      </span><ChevronRight size={13} />
    </DropdownMenu.SubTrigger>
    <DropdownMenu.Portal>
      <DropdownMenu.SubContent
        class="menu editor-menu"
        data-file-window-menu
        sideOffset={4}
      >
        {@render choices()}
      </DropdownMenu.SubContent>
    </DropdownMenu.Portal>
  </DropdownMenu.Sub>
{:else}
  <DropdownMenu.Root>
    <DropdownMenu.Trigger
      class="editor-status-button"
      disabled={!status?.loaded}
      aria-label={t(kind === 'encoding' ? 'encoding' : 'lineEnding')}
    >
      {value || '—'}
    </DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <DropdownMenu.Content
        preventScroll={false}
        class="menu editor-menu"
        data-file-window-menu
        side="top"
        align="end"
        sideOffset={6}
      >
        {@render choices()}
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
{/if}
