<script lang="ts">
  import { untrack } from 'svelte';
  import { Cpu, Server, History, Trash2 } from '@lucide/svelte';
  import { api, output } from '@/shared/api';
  import type { Node, Command } from '@/features/nodes/types';
  import { t, bytes, date } from '@/shared/settings.svelte';
  import type { Confirmation } from '@/shared/components/ConfirmDialog.svelte';
  let {
    node,
    active,
    onerror,
    onrequestconfirm,
  }: {
    node: Node;
    active: boolean;
    onerror: (message: string) => void;
    onrequestconfirm: (request: Confirmation) => void;
  } = $props();
  let records = $state<Command[]>([]);
  let result = $state<Command | null>(null);
  let selected = $state('');
  let hasMore = $state(true);
  let loadingMore = $state(false);
  let moreFailed = $state(false);
  let sentinel = $state<HTMLDivElement>();
  let refreshing = false;
  let controller = new AbortController();
  const pending = () =>
    result?.status === 'queued' || result?.status === 'running';
  async function refresh() {
    if (!active || refreshing || loadingMore) return;
    const signal = controller.signal;
    refreshing = true;
    try {
      const known = new Set(records.map((record) => record.id));
      const newer: Command[] = [];
      let page: Command[];
      do {
        page = await api<Command[]>(
          `nodes/${node.id}/commands?offset=${newer.length}`,
          { signal },
        );
        newer.push(...page);
      } while (
        active &&
        known.size &&
        page.length === 20 &&
        !page.some((record) => known.has(record.id))
      );
      const fresh = new Map(newer.map((record) => [record.id, record]));
      if (!records.length) hasMore = page.length === 20;
      if (page.length < 20) hasMore = false;
      records = [
        ...fresh.values(),
        ...(page.length < 20
          ? []
          : records.filter((record) => !fresh.has(record.id))),
      ];
      if (selected && !records.some((record) => record.id === selected)) {
        selected = '';
        result = null;
      }
      if (selected && (!result || pending())) {
        const updated = await api<Command>(`commands/${selected}`, { signal });
        if (selected === updated.id) {
          result = updated;
          records = records.map((item) =>
            item.id === updated.id
              ? {
                  ...item,
                  status: updated.status,
                  exit_code: updated.exit_code,
                }
              : item,
          );
        }
      }
    } catch (error) {
      if (!signal.aborted) onerror((error as Error).message);
    } finally {
      refreshing = false;
    }
  }
  async function loadMore() {
    if (!active || !hasMore || loadingMore || refreshing) return;
    const signal = controller.signal;
    loadingMore = true;
    moreFailed = false;
    try {
      const page = await api<Command[]>(
        `nodes/${node.id}/commands?offset=${records.length}`,
        { signal },
      );
      const known = new Set(records.map((record) => record.id));
      records = [...records, ...page.filter((record) => !known.has(record.id))];
      hasMore = page.length === 20;
    } catch (error) {
      if (signal.aborted) return;
      moreFailed = true;
      onerror((error as Error).message);
    } finally {
      loadingMore = false;
    }
  }
  async function select(id: string) {
    selected = selected === id ? '' : id;
    result = null;
    if (!selected) return;
    const signal = controller.signal;
    try {
      const record = await api<Command>(`commands/${id}`, { signal });
      if (selected === id) {
        result = record;
        records = records.map((item) =>
          item.id === id
            ? { ...item, status: record.status, exit_code: record.exit_code }
            : item,
        );
      }
    } catch (error) {
      if (signal.aborted) return;
      if (selected === id) selected = '';
      onerror((error as Error).message);
    }
  }
  function clearHistory() {
    onrequestconfirm({
      title: 'clearHistory',
      description: 'confirmClearHistory',
      confirmLabel: 'clearHistory',
      detail: node.name,
      danger: true,
      action: async () => {
        records = await api<Command[]>(`nodes/${node.id}/commands`, {
          method: 'DELETE',
        });
        if (!records.some((record) => record.id === selected)) {
          result = null;
          selected = '';
        }
        hasMore = records.length === 20;
        moreFailed = false;
      },
    });
  }
  $effect(() => {
    if (!active) return;
    const requests = new AbortController();
    controller = requests;
    untrack(() => void refresh());
    const timer = setInterval(() => void refresh(), 2000);
    return () => {
      clearInterval(timer);
      requests.abort();
    };
  });
  $effect(() => {
    if (
      !active ||
      !hasMore ||
      loadingMore ||
      moreFailed ||
      !sentinel ||
      !records.length
    )
      return;
    // The viewport respects both the desktop panel and mobile workspace clips.
    const observer = new IntersectionObserver((entries) => {
      if (entries[0].isIntersecting) void loadMore();
    });
    observer.observe(sentinel);
    return () => observer.disconnect();
  });
  const info = $derived(node.info);
  const hasDeviceInfo = $derived(
    !!info.collected_at && !info.collected_at.startsWith('0001-'),
  );
  const fields = $derived([
    [t('hostname'), info.hostname || t('unavailable')],
    [t('os'), info.platform?.trim() || info.os],
    [t('kernel'), info.kernel],
    [t('arch'), info.arch],
    [t('cpu'), `${info.cpu_model || '—'} · ${info.cpus}`],
    [
      t('cpuUsage'),
      info.cpu_percent == null
        ? t('unavailable')
        : `${info.cpu_percent.toFixed(1)}%`,
    ],
    [
      t('memory'),
      `${bytes(info.memory_available)} / ${bytes(info.memory_total)}`,
    ],
    [t('disk'), `${bytes(info.disk_free)} / ${bytes(info.disk_total)}`],
    [
      t('uptime'),
      `${Math.floor(info.uptime_seconds / 86400)}d ${Math.floor((info.uptime_seconds % 86400) / 3600)}h`,
    ],
    [t('network'), `↑ ${bytes(info.bytes_sent)}  ↓ ${bytes(info.bytes_recv)}`],
  ]);
</script>

<div class="node-info-scroll">
  <section class="detail-card node-metadata">
    <h2><Server size={17} />{t('nodeInfo')}</h2>
    <dl class="device-grid">
      <div>
        <dt>{t('status')}</dt>
        <dd>
          <span
            class="node-badge"
            class:online={node.online}
            data-status={node.status}
          >
            <span class="status-dot" class:online={node.online}></span>{t(
              node.status,
            )}
          </span>
        </dd>
      </div>
      <div>
        <dt>{t('reported')}</dt>
        <dd>{date(node.last_seen, true)}</dd>
      </div>
      {#each [[t('nodeID'), node.id], [t('name'), node.name], [t('joinMode'), t(node.join_mode === 'local' ? 'localMode' : node.join_mode === 'key' ? 'keyMode' : 'requestMode')], [t('createdAt'), date(node.created_at)], [t('joinedAt'), date(node.joined_at || '')], [t('expires'), date(node.key_expires_at || '')]] as [label, value]}
        <div>
          <dt>{label}</dt>
          <dd>{value || t('unavailable')}</dd>
        </div>
      {/each}
    </dl>
  </section>
  {#if hasDeviceInfo}
    <section class="detail-card device-info">
      <h2><Cpu size={17} />{t('device')}</h2>
      <dl class="device-grid">
        {#each fields as [label, value]}
          <div>
            <dt>{label}</dt>
            <dd>{value || t('unavailable')}</dd>
          </div>
        {/each}
      </dl>
      {#if info.errors && Object.keys(info.errors).length}<details
          class="collection-errors"
        >
          <summary>{t('collectionErrors')}</summary>
          <pre>{Object.entries(info.errors)
              .map(([key, value]) => `${key}: ${value}`)
              .join('\n')}</pre>
        </details>{/if}
    </section>
  {/if}
  <section class="detail-card">
    <h2>
      <History size={17} />{t('history')}
      <button
        class="icon-button history-clear"
        aria-label={t('clearHistory')}
        title={t('clearHistory')}
        disabled={!records.length}
        onclick={clearHistory}
      >
        <Trash2 size={15} />
      </button>
    </h2>
    {#if records.length}
      <div class="history-list">
        {#each records as item (item.id)}
          {@const record = selected === item.id && result ? result : item}
          <div class="history-entry">
            <button
              class="history-item"
              id={`history-record-${item.id}`}
              aria-expanded={selected === item.id}
              aria-controls={`history-result-${item.id}`}
              onclick={() => select(item.id)}
            >
              <code>{item.command}</code>
              <div class="history-meta">
                <span
                  class="result-status"
                  class:success={record.status === 'succeeded'}
                >
                  {t(record.status)}
                </span>
                {#if record.exit_code != null}<span
                    class="history-exit-code muted"
                  >
                    {t('exitCode')}
                    {record.exit_code}
                  </span>{/if}
                <time class="muted small" datetime={item.created_at}>
                  {date(item.created_at)}
                </time>
              </div>
            </button>
            {#if selected === item.id}
              <div
                class="history-result"
                id={`history-result-${item.id}`}
                role="region"
                aria-labelledby={`history-record-${item.id}`}
              >
                {#if result}
                  <pre class="command-output" data-testid="stdout">{output(
                      result.stdout,
                    ) || (pending() ? '…' : t('noOutput'))}</pre>
                  {#if result.stderr}<div class="output-label">
                      {t('stderr')}
                    </div>
                    <pre class="command-output error-output">{output(
                        result.stderr,
                      )}</pre>{/if}
                  {#if result.error}<p class="inline-error">
                      {result.error}
                    </p>{/if}
                  {#if result.truncated}<p class="muted small">
                      {t('truncated')} · 256 KiB
                    </p>{/if}
                {:else}<p class="muted small" role="status">
                    {t('loading')}
                  </p>{/if}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {:else}<p class="empty-history">{t('noHistory')}</p>{/if}
    <div class="history-load-more" bind:this={sentinel} aria-busy={loadingMore}>
      {#if loadingMore}<span role="status">{t('loading')}</span>
      {:else if moreFailed}<button
          class="quiet-button"
          onclick={() => {
            moreFailed = false;
            void loadMore();
          }}
        >
          {t('retryLoad')}
        </button>
      {:else if records.length && !hasMore}<span>
          {t('allHistoryLoaded')}
        </span>{/if}
    </div>
  </section>
</div>
