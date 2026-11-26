<script lang="ts">
  import { ContextMenu } from 'bits-ui';
  import {
    File,
    Scissors,
    Copy,
    ClipboardPaste,
    FilePlus,
    FolderPlus,
    Upload,
    FolderUp,
    Pencil,
    PenLine,
    SquareDashedPlus,
    Link,
    Download,
    Trash2,
  } from '@lucide/svelte';
  import { t } from '@/shared/settings.svelte';
  let {
    count,
    online,
    directory,
    canPaste,
    canEditLink,
    onlink,
    onopen,
    oncut,
    oncopy,
    onpaste,
    onnew,
    onupload,
    onrename,
    onpath,
    ondownload,
    ondelete,
  }: {
    count: number;
    online: boolean;
    directory: boolean;
    canPaste: boolean;
    canEditLink: boolean;
    onlink: (edit: boolean) => void;
    onopen: () => void;
    oncut: () => void;
    oncopy: () => void;
    onpaste: () => void;
    onnew: (kind: 'newFile' | 'newDirectory') => void;
    onupload: (kind: 'files' | 'folder') => void;
    onrename: () => void;
    onpath: () => void;
    ondownload: () => void;
    ondelete: () => void;
  } = $props();
</script>

<ContextMenu.Portal>
  <ContextMenu.Content class="menu file-context-menu">
    <ContextMenu.Item
      class="menu-item"
      disabled={count !== 1 || !online}
      onSelect={onopen}
    >
      <File size={14} />{t('openFile')}
    </ContextMenu.Item>
    <ContextMenu.Item
      class="menu-item"
      disabled={!count || !online}
      onSelect={oncut}
    >
      <Scissors size={14} />{t('cutFiles')}
    </ContextMenu.Item>
    <ContextMenu.Item
      class="menu-item"
      disabled={!count || !online}
      onSelect={oncopy}
    >
      <Copy size={14} />{t('copySelection')}
    </ContextMenu.Item>
    <ContextMenu.Item
      class="menu-item"
      disabled={!canPaste || !online}
      onSelect={onpaste}
    >
      <ClipboardPaste size={14} />{t('paste')}
    </ContextMenu.Item>
    <ContextMenu.Separator class="menu-separator" />
    <ContextMenu.Item
      class="menu-item"
      disabled={!directory || !online}
      onSelect={() => onnew('newFile')}
    >
      <FilePlus size={14} />{t('newFile')}
    </ContextMenu.Item>
    <ContextMenu.Item
      class="menu-item"
      disabled={!directory || !online}
      onSelect={() => onnew('newDirectory')}
    >
      <FolderPlus size={14} />{t('newDirectory')}
    </ContextMenu.Item>
    <ContextMenu.Item
      class="menu-item"
      disabled={!directory || !online}
      onSelect={() => onlink(false)}
    >
      <SquareDashedPlus size={14} />{t('createLink')}
    </ContextMenu.Item>
    <ContextMenu.Item
      class="menu-item"
      disabled={!directory || !online}
      onSelect={() => onupload('files')}
    >
      <Upload size={14} />{t('uploadFiles')}
    </ContextMenu.Item>
    <ContextMenu.Item
      class="menu-item"
      disabled={!directory || !online}
      onSelect={() => onupload('folder')}
    >
      <FolderUp size={14} />{t('uploadFolder')}
    </ContextMenu.Item>
    <ContextMenu.Separator class="menu-separator" />
    <ContextMenu.Item
      class="menu-item"
      disabled={count !== 1 || !online}
      onSelect={onrename}
    >
      <Pencil size={14} />{t('rename')}
    </ContextMenu.Item>
    <ContextMenu.Item
      class="menu-item"
      disabled={!canEditLink || !online}
      onSelect={() => onlink(true)}
    >
      <PenLine size={14} />{t('editLink')}
    </ContextMenu.Item>
    <ContextMenu.Item class="menu-item" onSelect={onpath}>
      <Link size={14} />{t('copyPath')}
    </ContextMenu.Item>
    <ContextMenu.Item
      class="menu-item"
      disabled={!count || !online}
      onSelect={ondownload}
    >
      <Download size={14} />{t('download')}
    </ContextMenu.Item>
    <ContextMenu.Separator class="menu-separator" />
    <ContextMenu.Item
      class="menu-item danger"
      disabled={!count || !online}
      onSelect={ondelete}
    >
      <Trash2 size={14} />{t('deleteFiles')}
    </ContextMenu.Item>
  </ContextMenu.Content>
</ContextMenu.Portal>
