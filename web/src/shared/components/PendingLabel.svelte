<script lang="ts">
  import { LoaderCircle } from '@lucide/svelte';
  import { t } from '@/shared/settings.svelte';
</script>

<LoaderCircle class="spin" size={14} aria-hidden="true" />{t('processing')}
