<script lang="ts">
  import {
    Plus,
    RotateCw,
    Pencil,
    Trash2,
    TriangleAlert,
  } from '@lucide/svelte';
  import { api } from '@/shared/api';
  import type { Node } from '@/features/nodes/types';
  import type { Transfers } from '@/features/transfers/store.svelte';
  import type { Confirmation } from '@/shared/components/ConfirmDialog.svelte';
  import ForwardDialog from './ForwardDialog.svelte';
  import { targetAddress, type Forward } from './types';
  import { t } from '@/shared/settings.svelte';
  let {
    node,
    active,
    manager,
    onrequestconfirm,
  }: {
    node: Node;
    active: boolean;
    manager: Transfers;
    onrequestconfirm: (request: Confirmation) => void;
  } = $props();
  let mappings = $state<Forward[]>([]),
    refreshing = $state(false),
    loaded = $state(false),
    error = $state('');
  let dialog = $state<{ mapping?: Forward } | null>(null);
  let controller: AbortController | undefined;
  let pending = false;
  const nodeID = $derived(node.id);
  const statuses = {
    ready: 'forwardListening',
    stopped: 'forwardStopped',
    target_error: 'forwardTargetError',
    node_offline: 'forwardNodeOffline',
    listen_error: 'forwardPortUnavailable',
  } as const;
  async function refresh(source: 'background' | 'manual' = 'background') {
    if (source === 'background' && controller) {
      pending = true;
      return;
    }
    if (source === 'manual' && refreshing) return;
    pending = false;
    controller?.abort();
    const request = new AbortController();
    controller = request;
    refreshing = source === 'manual';
    try {
      const result = await api<Forward[]>(
        `forwards?node_id=${encodeURIComponent(node.id)}`,
        { signal: request.signal },
      );
      if (!request.signal.aborted) {
        mappings = result;
        error = '';
        loaded = true;
      }
    } catch (failure) {
      if (!request.signal.aborted) {
        error = (failure as Error).message;
        loaded = true;
      }
    } finally {
      if (controller === request) {
        controller = undefined;
        refreshing = false;
        if (pending) void refresh();
      }
    }
  }
  function remove(mapping: Forward) {
    onrequestconfirm({
      title: 'removeForward',
      description: 'confirmRemoveForward',
      confirmLabel: 'removeForward',
      danger: true,
      detail: `${mapping.name} · ${mapping.protocol.toUpperCase()} · ${mapping.listen_addr} → ${targetAddress(mapping)}`,
      action: async () => {
        await api(`forwards/${mapping.id}`, { method: 'DELETE' });
      },
    });
  }
  $effect(() => {
    if (!active) return;
    const unsubscribe = manager.subscribe(
      { topic: 'forwards', node_id: nodeID },
      () => void refresh(),
    );
    return () => {
      unsubscribe();
      controller?.abort();
      controller = undefined;
      pending = false;
      refreshing = false;
    };
  });
</script>

<div class="forwards-panel">
  <div class="forwards-heading">
    <button
      class="quiet-button"
      disabled={node.admission !== 'approved'}
      onclick={() => (dialog = {})}
    >
      <Plus size={13} />{t('addForward')}
    </button>
    <button
      class="icon-button"
      aria-label={t('refresh')}
      title={t('refresh')}
      disabled={refreshing}
      aria-busy={refreshing}
      onclick={() => refresh('manual')}
    >
      <RotateCw size={14} class={refreshing ? 'spin' : undefined} />
    </button>
  </div>
  <div class="forwards-list">
    {#if error}<p class="file-error" role="alert">{error}</p>{/if}
    {#each mappings as mapping (mapping.id)}
      <article class="forward-card" data-forward={mapping.id}>
        <header>
          <span class="forward-name" title={mapping.name}>{mapping.name}</span>
          <span class="forward-protocols">
            {#each mapping.protocol.split('+') as protocol}
              <span
                class="forward-protocol"
                data-status={mapping.status}
                title={mapping.error || t(statuses[mapping.status])}
              >
                {protocol.toUpperCase()}
              </span>
            {/each}
          </span>
          <button
            class="icon-button"
            aria-label={`${t('editForward')} ${mapping.name}`}
            title={t('editForward')}
            onclick={() => (dialog = { mapping })}
          >
            <Pencil size={13} />
          </button>
          <button
            class="icon-button danger"
            aria-label={`${t('removeForward')} ${mapping.name}`}
            title={t('removeForward')}
            onclick={() => remove(mapping)}
          >
            <Trash2 size={13} />
          </button>
        </header>
        {#if mapping.status === 'listen_error'}
          <p class="forward-warning" title={mapping.error}>
            <TriangleAlert size={13} />{t('forwardPortUnavailable')}
          </p>
        {/if}
        <dl>
          <div>
            <dt>{t('listenAddress')}</dt>
            <dd>{mapping.listen_addr}</dd>
          </div>
          <div>
            <dt>{t('nodeTarget')}</dt>
            <dd>{targetAddress(mapping)}</dd>
          </div>
        </dl>
      </article>
    {/each}
    {#if !loaded}<p class="file-empty" role="status">{t('loading')}</p>
    {:else if !error && !mappings.length}<p class="file-empty">
        {t('noForwards')}
      </p>{/if}
  </div>
</div>
{#if dialog}<ForwardDialog
    {node}
    mapping={dialog.mapping}
    onclose={() => (dialog = null)}
  />{/if}
