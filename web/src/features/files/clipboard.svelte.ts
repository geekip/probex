import type { FileEntry } from './api';

// Clipboard entries belong to one node's filesystem.
export const clipboard = $state({
  nodeID: '',
  cut: false,
  entries: [] as FileEntry[],
});
