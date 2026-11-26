<script lang="ts">
  import PendingLabel from '@/shared/components/PendingLabel.svelte';
  import { untrack } from 'svelte';
  import { Dialog, DropdownMenu } from 'bits-ui';
  import { Check, ChevronDown } from '@lucide/svelte';
  import { api, ApiError } from '@/shared/api';
  import type { Node } from '@/features/nodes/types';
  import {
    parseAddress,
    targetAddress,
    type Forward,
    type ForwardProtocol,
  } from './types';
  import { t } from '@/shared/settings.svelte';
  let {
    node,
    mapping,
    onclose,
  }: {
    node: Node;
    mapping?: Forward;
    onclose: () => void;
  } = $props();
  const initial = untrack(() => mapping);
  const protocols = [
    { value: 'tcp', label: 'TCP' },
    { value: 'udp', label: 'UDP' },
    { value: 'tcp+udp', label: 'TCP+UDP' },
  ] as const;
  let open = $state(true),
    name = $state(initial?.name || ''),
    protocol = $state<ForwardProtocol>(initial?.protocol || 'tcp'),
    listen = $state(initial?.listen_addr || '127.0.0.1:'),
    target = $state(initial ? targetAddress(initial) : '127.0.0.1:'),
    busy = $state(false),
    portError = $state(''),
    error = $state('');
  const id = $props.id();
  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    busy = true;
    portError = '';
    error = '';
    try {
      const destination = parseAddress(target);
      if (!parseAddress(listen) || !destination)
        throw new Error(t('invalidForwardAddress'));
      await api(initial ? `forwards/${initial.id}` : 'forwards', {
        method: initial ? 'PUT' : 'POST',
        body: JSON.stringify({
          node_id: node.id,
          name: name.trim(),
          protocol,
          listen_addr: listen.trim(),
          target_host: destination.host,
          target_port: destination.port,
        }),
      });
      onclose();
    } catch (failure) {
      if (
        failure instanceof ApiError &&
        failure.code === 'forward_port_unavailable'
      )
        portError = failure.message;
      else error = (failure as Error).message;
    } finally {
      busy = false;
    }
  }
</script>

<Dialog.Root
  bind:open
  onOpenChange={(value) => {
    if (!value) onclose();
  }}
>
  <Dialog.Portal>
    <Dialog.Overlay class="dialog-overlay" />
    <Dialog.Content
      class="join-dialog forward-dialog"
      aria-busy={busy}
      onEscapeKeydown={(event) => {
        if (busy) event.preventDefault();
      }}
      onInteractOutside={(event) => {
        if (busy) event.preventDefault();
      }}
    >
      <div class="dialog-heading">
        <div>
          <Dialog.Title class="dialog-title">
            {t(initial ? 'editForward' : 'addForward')}
          </Dialog.Title><Dialog.Description class="dialog-description">
            {node.name}
          </Dialog.Description>
        </div>
      </div>
      <form class="join-content forward-form" onsubmit={save}>
        <label for={id + '-name'}>{t('forwardName')}</label><input
          id={id + '-name'}
          bind:value={name}
          required
          disabled={busy}
          autocomplete="off"
        />
        <label for={id + '-protocol'}>{t('forwardProtocol')}</label>
        <DropdownMenu.Root>
          <DropdownMenu.Trigger
            id={id + '-protocol'}
            class="forward-select"
            disabled={busy}
          >
            {protocol.toUpperCase()}<ChevronDown size={14} />
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              class="menu forward-protocol-menu"
              sideOffset={4}
              align="start"
            >
              <DropdownMenu.RadioGroup
                value={protocol}
                onValueChange={(value) => {
                  protocol = value as ForwardProtocol;
                  portError = '';
                }}
              >
                {#each protocols as option}<DropdownMenu.RadioItem
                    class="menu-item"
                    value={option.value}
                  >
                    <span>
                      {option.label}
                    </span>{#if protocol === option.value}<Check
                        size={13}
                      />{/if}
                  </DropdownMenu.RadioItem>{/each}
              </DropdownMenu.RadioGroup>
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
        <label for={id + '-listen'}>{t('listenAddress')}</label><input
          id={id + '-listen'}
          bind:value={listen}
          oninput={() => (portError = '')}
          placeholder="127.0.0.1:2222"
          required
          disabled={busy}
          aria-invalid={!!portError}
          aria-describedby={portError ? id + '-listen-error' : undefined}
          autocomplete="off"
        />
        {#if portError}
          <p id={id + '-listen-error'} class="inline-error" role="alert">
            {t('forwardPortUnavailable')} · {portError}
          </p>
        {/if}
        <label for={id + '-target'}>{t('nodeTarget')}</label><input
          id={id + '-target'}
          bind:value={target}
          placeholder="127.0.0.1:22"
          required
          disabled={busy}
          autocomplete="off"
        />
        {#if error}<p class="inline-error" role="alert">{error}</p>{/if}
        <div class="dialog-actions">
          <button
            type="button"
            class="quiet-button"
            disabled={busy}
            onclick={onclose}
          >
            {t('cancel')}
          </button><button class="primary-button" disabled={busy}>
            {#if busy}<PendingLabel />{:else}{t(
                initial ? 'save' : 'addForward',
              )}{/if}
          </button>
        </div>
      </form>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>
