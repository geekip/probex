<script lang="ts">
  import { Server, Activity, CircleDashed } from '@lucide/svelte';
  import { t } from '@/shared/settings.svelte';
  let {
    total,
    online,
    offline,
    compact = false,
  }: {
    total: number;
    online: number;
    offline: number;
    compact?: boolean;
  } = $props();
</script>

<div class:compact class="stats" aria-label={t('overview')}>
  <div class="stat">
    <span class="stat-label"><Server size={15} />{t('total')}</span><strong>
      {total}
    </strong>
  </div>
  <div class="stat">
    <span class="stat-label"><Activity size={15} />{t('online')}</span><strong>
      {online}
    </strong>
  </div>
  <div class="stat">
    <span class="stat-label">
      <CircleDashed size={15} />{t('offline')}
    </span><strong>{offline}</strong>
  </div>
</div>
