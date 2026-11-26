<script lang="ts">
  import { sortableTabs, reorderTabs } from '@/shared/tab-sort';
  import NodeInfo from '@/features/nodes/NodeInfo.svelte';
  import NodeMenu from '@/features/nodes/NodeMenu.svelte';
  import FileBrowser from '@/features/files/FileBrowser.svelte';
  import Forwards from '@/features/forwards/Forwards.svelte';
  import type { Node } from '@/features/nodes/types';
  import type { FileEntry } from '@/features/files/api';
  import type { Transfers } from '@/features/transfers/store.svelte';
  import type { Confirmation } from '@/shared/components/ConfirmDialog.svelte';
  import { t } from '@/shared/settings.svelte';
  let {
    node,
    active,
    historyPaused,
    manager,
    onerror,
    onrequestconfirm,
    onrename,
    onreview,
    onremove,
    onenrollment,
    onupload,
    onopen,
    onrenamed,
    onremoved,
  }: {
    node: Node;
    active: boolean;
    historyPaused: boolean;
    manager: Transfers;
    onerror: (message: string) => void;
    onrequestconfirm: (request: Confirmation) => void;
    onrename: () => void;
    onreview: (node: Node, action: 'approve' | 'reject') => void;
    onremove: () => void;
    onenrollment: () => void;
    onupload: (directory: string, kind?: 'files' | 'folder') => void;
    onopen: (entry: FileEntry) => void;
    onrenamed: (path: string, destination: string) => void;
    onremoved: (paths: string[]) => void;
  } = $props();
  let tab = $state('info'),
    filesMounted = $state(false),
    forwardsMounted = $state(false);
  const initialTabs = [
    { id: 'info', label: 'nodeInfo' },
    { id: 'files', label: 'fileManager' },
    { id: 'forwards', label: 'portForwards' },
  ] as const;
  let tabs = $state([...initialTabs]);
  function select(value: string) {
    tab = value;
    if (value === 'files') filesMounted = true;
    if (value === 'forwards') forwardsMounted = true;
  }
</script>

<aside class="node-info" aria-label={`${node.name} ${t('nodeInfo')}`}>
  <header class="node-info-heading">
    <div
      class="node-panel-tabs"
      use:sortableTabs={(from, to) => (tabs = reorderTabs(tabs, from, to))}
      role="tablist"
      aria-label={t('nodePanel')}
    >
      {#each tabs as item, index (item.id)}<button
          class="workspace-tab"
          data-tab-id={item.id}
          role="tab"
          id={`node-${node.id}-${item.id}-tab`}
          aria-controls={`node-${node.id}-${item.id}`}
          aria-selected={tab === item.id}
          tabindex={tab === item.id ? 0 : -1}
          onclick={() => select(item.id)}
          onkeydown={(e) => {
            if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
              e.preventDefault();
              const next = tabs[(index + (e.key === 'ArrowRight' ? 1 : 2)) % 3];
              select(next.id);
              document
                .getElementById(`node-${node.id}-${next.id}-tab`)
                ?.focus();
            }
          }}
        >
          {t(item.label)}
        </button>{/each}
    </div>
    <NodeMenu {node} {onrename} {onenrollment} {onremove} {onreview} />
  </header>
  <div
    class="node-panel-tab"
    role="tabpanel"
    id={`node-${node.id}-info`}
    aria-labelledby={`node-${node.id}-info-tab`}
    hidden={tab !== 'info'}
  >
    <NodeInfo
      {node}
      active={active && tab === 'info' && !historyPaused}
      {onerror}
      {onrequestconfirm}
    />
  </div>
  <div
    class="node-panel-tab"
    role="tabpanel"
    id={`node-${node.id}-files`}
    aria-labelledby={`node-${node.id}-files-tab`}
    hidden={tab !== 'files'}
  >
    {#if filesMounted}<FileBrowser
        {node}
        active={active && tab === 'files'}
        {manager}
        {onerror}
        {onrequestconfirm}
        {onupload}
        {onopen}
        {onrenamed}
        {onremoved}
      />{/if}
  </div>
  <div
    class="node-panel-tab"
    role="tabpanel"
    id={`node-${node.id}-forwards`}
    aria-labelledby={`node-${node.id}-forwards-tab`}
    hidden={tab !== 'forwards'}
  >
    {#if forwardsMounted}<Forwards
        {node}
        active={active && tab === 'forwards'}
        {onrequestconfirm}
      />{/if}
  </div>
</aside>
