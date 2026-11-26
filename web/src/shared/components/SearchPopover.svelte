<script lang="ts">
  import { Popover } from 'bits-ui';
  import { CircleX, Search } from '@lucide/svelte';
  import { t } from '@/shared/settings.svelte';
  let {
    label,
    value = $bindable(''),
    align = 'end',
    closeOnEnter = false,
    onquery,
  }: {
    label: string;
    value: string;
    align?: 'start' | 'end';
    closeOnEnter?: boolean;
    onquery?: () => void;
  } = $props();
  let open = $state(false);
  let input = $state<HTMLInputElement>();
</script>

<Popover.Root bind:open>
  <Popover.Trigger
    class="icon-button"
    data-search-active={String(!!value)}
    aria-label={label}
    title={label}
  >
    <Search size={15} />
  </Popover.Trigger>
  <Popover.Portal>
    <Popover.Content
      class="menu search-popover"
      side="bottom"
      {align}
      sideOffset={6}
      collisionPadding={8}
    >
      <div class="search-field">
        <input
          type="search"
          aria-label={label}
          placeholder={label}
          bind:this={input}
          bind:value
          oninput={onquery}
          onkeydown={(event) => {
            if (closeOnEnter && event.key === 'Enter') {
              event.preventDefault();
              open = false;
            }
          }}
        />
        {#if value}
          <button
            type="button"
            class="icon-button search-clear"
            aria-label={t('clearSearch')}
            title={t('clearSearch')}
            onclick={() => {
              value = '';
              input?.focus();
              onquery?.();
            }}
          >
            <CircleX size={14} fill="var(--muted)" color="var(--surface)" />
          </button>
        {/if}
      </div>
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>

<style>
  .search-field {
    position: relative;
  }
  .search-field input {
    display: block;
    width: min(200px, calc(100vw - 40px));
    font-size: 12px;
    padding: 6px;
    padding-inline-end: 28px;
    border: 1px solid var(--border);
    border-radius: 5px;
  }
  input::-webkit-search-cancel-button {
    display: none;
  }
  .search-clear {
    position: absolute;
    inset-inline-end: 3px;
    top: 50%;
    transform: translateY(-50%);
    width: 22px;
    height: 22px;
  }
</style>
