<script lang="ts">
  import { untrack } from 'svelte';
  import {
    Plus,
    RotateCw,
    Pencil,
    Trash2,
    TriangleAlert,
  } from '@lucide/svelte';
  import { api } from '@/shared/api';
  import type { Node } from '@/features/nodes/types';
  import type { Confirmation } from '@/shared/components/ConfirmDialog.svelte';
  import ForwardDialog from './ForwardDialog.svelte';
  import { targetAddress, type Forward } from './types';
  import { t } from '@/shared/settings.svelte';
  let {
    node,
    active,
    onrequestconfirm,
  }: {
    node: Node;
    active: boolean;
    onrequestconfirm: (request: Confirmation) => void;
  } = $props();
  let mappings = $state<Forward[]>([]),
    busy = $state(false),
    loaded = $state(false),
    error = $state('');
  let dialog = $state<{ mapping?: Forward } | null>(null);
  let controller: AbortController | undefined;
  const nodeID = $derived(node.id);
  const statuses = {
    ready: 'forwardListening',
    stopped: 'forwardStopped',
    target_error: 'forwardTargetError',
    node_offline: 'forwardNodeOffline',
    listen_error: 'forwardPortUnavailable',
  } as const;
  async function refresh(force = false) {
    if (busy && !force) return;
    controller?.abort();
    const request = new AbortController();
    controller = request;
    busy = true;
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
      if (controller === request) busy = false;
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
        await refresh(true);
      },
    });
  }
  $effect(() => {
    if (!active) return;
    nodeID;
    untrack(() => void refresh(true));
    const timer = setInterval(() => void refresh(), 2000);
    return () => {
      clearInterval(timer);
      controller?.abort();
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
      aria-label={t('refreshForwards')}
      title={t('refreshForwards')}
      disabled={busy}
      onclick={() => refresh(true)}
    >
      <RotateCw size={14} />
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
            <dt>{t('serverListen')}</dt>
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
    onsaved={() => void refresh(true)}
    onclose={() => (dialog = null)}
  />{/if}
