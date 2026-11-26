import type { PickedFile } from './store.svelte';

export async function droppedFiles(items: DataTransferItem[]) {
  const files: PickedFile[] = [],
    directories: string[] = [];
  async function visit(entry: FileSystemEntry, path: string): Promise<void> {
    if (entry.isFile) {
      const file = await new Promise<File>((resolve, reject) =>
        (entry as FileSystemFileEntry).file(resolve, reject),
      );
      files.push({ file, relative: path });
      return;
    }
    const reader = (entry as FileSystemDirectoryEntry).createReader();
    let count = 0;
    for (;;) {
      const children = await new Promise<FileSystemEntry[]>((resolve, reject) =>
        reader.readEntries(resolve, reject),
      );
      if (!children.length) break;
      count += children.length;
      for (const child of children) await visit(child, path + '/' + child.name);
    }
    if (!count) directories.push(path);
  }
  // Capture entries while the drop event still exposes its data store.
  const entries = items.map((item) => ({
    entry: item.webkitGetAsEntry(),
    file: item.getAsFile(),
  }));
  for (const { entry, file } of entries) {
    if (entry) await visit(entry, entry.name);
    else if (file) files.push({ file, relative: file.name });
  }
  return { files, directories };
}
