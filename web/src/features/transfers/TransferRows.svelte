<script lang="ts">
  import { Pause, Play, RotateCw, X, Check } from '@lucide/svelte';
  import type { Transfer, Transfers } from '@/features/transfers/store.svelte';
  import { t, bytes } from '@/shared/settings.svelte';
  import { remoteJoin } from '@/features/files/api';
  let { items, manager }: { items: Transfer[]; manager: Transfers } = $props();
  const states = {
    running: 'transferring',
    queued: 'transferQueued',
    paused: 'transferPaused',
    failed: 'transferFailed',
    completed: 'transferCompleted',
  } as const;
</script>

<div class="transfer-table-scroll">
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
        )}<tr data-transfer={item.id} data-state={item.state}>
          <td title={path}>
            <span class="transfer-name">
              {path}
            </span>{#if item.error}<small class="transfer-error">
                {item.error}
              </small>{/if}
          </td>
          <td>{item.nodeName}</td><td>{t(states[item.state])}</td><td
            class="transfer-size"
          >
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
              {#if item.needsOverwrite}<button
                  class="quiet-button"
                  onclick={() => manager.restart(item, true)}
                >
                  {t('overwrite')}
                </button>
              {:else if item.state === 'paused'}<button
                  class="icon-button"
                  aria-label={t('resumeTransfer')}
                  title={t('resumeTransfer')}
                  onclick={() => manager.resume(item)}
                >
                  <Play size={14} />
                </button>
              {:else if item.state === 'running' || item.state === 'queued'}<button
                  class="icon-button"
                  aria-label={t('pauseTransfer')}
                  title={t('pauseTransfer')}
                  onclick={() => manager.pause(item)}
                >
                  <Pause size={14} />
                </button>
              {:else if item.state === 'failed'}<button
                  class="icon-button"
                  aria-label={t('restartTransfer')}
                  title={t('restartTransfer')}
                  onclick={() => manager.restart(item)}
                >
                  <RotateCw size={14} />
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
                onclick={() => manager.cancel(item)}
              >
                <X size={14} />
              </button>
            </div>
          </td>
        </tr>{/each}
    </tbody>
  </table>
  {#if !items.length}<div class="file-empty">{t('noTransfers')}</div>{/if}
</div>
