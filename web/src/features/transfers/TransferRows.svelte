<script module lang="ts">
  import type { Transfer } from '@/features/transfers/store.svelte';
  import type { Message } from '@/shared/settings.svelte';

  export const transferStatusLabels = {
    running: 'transferring',
    queued: 'transferQueued',
    paused: 'transferPaused',
    failed: 'transferFailed',
    completed: 'transferCompleted',
  } as const satisfies Record<Transfer['state'], Message>;
</script>

<script lang="ts">
  import { SvelteMap } from 'svelte/reactivity';
  import {
    Pause,
    Play,
    RotateCw,
    X,
    Check,
    LoaderCircle,
  } from '@lucide/svelte';
  import type { Transfers } from '@/features/transfers/store.svelte';
  import { t, bytes } from '@/shared/settings.svelte';
  import { remoteJoin } from '@/features/files/api';
  let {
    items,
    manager,
    disabled,
  }: { items: Transfer[]; manager: Transfers; disabled: boolean } = $props();
  type Action = 'resume' | 'restart' | 'cancel';
  const pending = new SvelteMap<number, Action>();
  async function act(item: Transfer, action: Action) {
    if (disabled || pending.has(item.id)) return;
    pending.set(item.id, action);
    try {
      await manager[action](item);
    } finally {
      pending.delete(item.id);
    }
  }
</script>

<div class="transfer-table-scroll" hidden={!items.length}>
  <table class="transfer-table">
    <thead>
      <tr>
        <th>{t('fileName')}</th><th>{t('node')}</th><th>
          {t('transferStatus')}
        </th><th>
          {t('fileSize')}
        </th><th>{t('actions')}</th>
      </tr>
    </thead>
    <tbody>
      {#each items as item (item.id)}{@const path = remoteJoin(
          item.target,
          item.relative,
          item.windows,
        )}{@const action = pending.get(item.id)}<tr
          data-transfer={item.id}
          data-state={item.state}
          aria-busy={!!action}
        >
          <td title={path}>
            <span class="transfer-name">
              {path}
            </span>{#if item.error}<small class="transfer-error">
                {item.error}
              </small>{/if}
          </td>
          <td>{item.nodeName}</td><td>
            {t(transferStatusLabels[item.state])}
          </td><td class="transfer-size">
            <span>
              {bytes(item.offset)} / {bytes(item.size)}
            </span><progress
              max={item.size || 1}
              value={item.size
                ? item.offset
                : item.state === 'completed'
                  ? 1
                  : 0}
            ></progress>
          </td>
          <td>
            <div class="transfer-actions">
              {#if item.state === 'paused'}<button
                  class="icon-button"
                  aria-label={t('resumeTransfer')}
                  title={t('resumeTransfer')}
                  disabled={disabled || !!action}
                  onclick={() => act(item, 'resume')}
                >
                  {#if action === 'resume'}<LoaderCircle
                      class="spin"
                      size={14}
                    />{:else}<Play size={14} />{/if}
                </button>
              {:else if item.state === 'running' || item.state === 'queued'}<button
                  class="icon-button"
                  aria-label={t('pauseTransfer')}
                  title={t('pauseTransfer')}
                  disabled={disabled || !!action}
                  onclick={() => manager.pause(item)}
                >
                  <Pause size={14} />
                </button>
              {:else if item.state === 'failed'}<button
                  class="icon-button"
                  aria-label={t('restartTransfer')}
                  title={t('restartTransfer')}
                  disabled={disabled || !!action}
                  onclick={() => act(item, 'restart')}
                >
                  {#if action === 'restart'}<LoaderCircle
                      class="spin"
                      size={14}
                    />{:else}<RotateCw size={14} />{/if}
                </button>
              {:else}<Check size={14} />{/if}
              <button
                class="icon-button"
                aria-label={t(
                  item.state === 'completed'
                    ? 'removeTransfer'
                    : 'cancelTransfer',
                )}
                title={t(
                  item.state === 'completed'
                    ? 'removeTransfer'
                    : 'cancelTransfer',
                )}
                disabled={disabled || !!action}
                onclick={() => act(item, 'cancel')}
              >
                {#if action === 'cancel'}<LoaderCircle
                    class="spin"
                    size={14}
                  />{:else}<X size={14} />{/if}
              </button>
            </div>
          </td>
        </tr>{/each}
    </tbody>
  </table>
</div>
