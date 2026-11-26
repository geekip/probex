<script lang="ts" generics="ID extends string | number">
  import { DropdownMenu } from 'bits-ui';
  import { MoreHorizontal } from '@lucide/svelte';
  import { t } from '@/shared/settings.svelte';

  let {
    ids,
    active,
    insideEditor = false,
    onclose,
  }: {
    ids: ID[];
    active: ID | undefined;
    insideEditor?: boolean;
    onclose: (ids: ID[]) => void;
  } = $props();
</script>

<DropdownMenu.Root>
  <DropdownMenu.Trigger
    class="icon-button tabs-overflow"
    aria-label={t('tabMenu')}
    disabled={!ids.length}
  >
    <MoreHorizontal size={18} />
  </DropdownMenu.Trigger>
  <DropdownMenu.Portal>
    <DropdownMenu.Content
      class="menu"
      preventScroll={!insideEditor}
      data-file-window-menu={insideEditor || undefined}
      align="end"
      sideOffset={10}
    >
      <DropdownMenu.Item
        class="menu-item"
        disabled={active === undefined}
        onSelect={() => active !== undefined && onclose([active])}
      >
        {t('closeCurrent')}
      </DropdownMenu.Item>
      <DropdownMenu.Item class="menu-item" onSelect={() => onclose(ids)}>
        {t('closeAll')}
      </DropdownMenu.Item>
    </DropdownMenu.Content>
  </DropdownMenu.Portal>
</DropdownMenu.Root>
