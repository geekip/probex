<script lang="ts">
  import { Dialog } from 'bits-ui';
  import { KeyRound, Copy, Check, Plus } from '@lucide/svelte';
  import copyText from 'clipboard-copy';
  import { api, type Node, type Enrollment } from './api';
  import { t, date } from './settings.svelte';
  let {
    node,
    onclose,
    oncreated,
    onerror,
  }: {
    node?: Node;
    onclose: () => void;
    oncreated: () => void;
    onerror: (message: string) => void;
  } = $props();
  let open = $state(true);
  let name = $state('');
  let server = $state(
    `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}`,
  );
  let enrollment = $state<Enrollment | null>(null);
  let busy = $state(false);
  let copied = $state(false);
  function clientCommand() {
    const url = new URL(server);
    if (
      !['ws:', 'wss:'].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      !['', '/', '/agent'].includes(url.pathname)
    )
      throw new Error(t('invalidServer'));
    return `probex connect --server ${url.origin}/agent --key ${enrollment!.key}`;
  }
  async function create(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    try {
      enrollment = await api<Enrollment>(
        node ? `nodes/${node.id}/enrollment` : 'nodes',
        { method: 'POST', body: JSON.stringify(node ? {} : { name }) },
      );
      oncreated();
    } catch (error) {
      onerror((error as Error).message);
    } finally {
      busy = false;
    }
  }
  async function copy() {
    copied = false;
    let command: string;
    try {
      command = clientCommand();
    } catch (error) {
      onerror((error as Error).message);
      return;
    }
    try {
      await copyText(command);
      copied = true;
    } catch {
      onerror(t('copyFailed'));
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
    <Dialog.Overlay class="dialog-overlay" /><Dialog.Content
      class="join-dialog"
    >
      <div class="dialog-heading">
        <div>
          <Dialog.Title class="dialog-title">
            {enrollment ? t('joinKey') : t(node ? 'regenerate' : 'addNode')}
          </Dialog.Title><Dialog.Description class="dialog-description">
            {enrollment
              ? t('joinOnce')
              : t(node ? 'regenerateHint' : 'joinHint')}
          </Dialog.Description>
        </div>
      </div>
      <div class="join-content">
        {#if !enrollment}<form onsubmit={create}>
            <label for="new-node-name">{t('name')}</label><input
              id="new-node-name"
              value={node ? node.name : name}
              oninput={(event) => (name = event.currentTarget.value)}
              disabled={!!node}
              required
              maxlength="64"
              placeholder="node1"
              autocomplete="off"
            />
            <div class="dialog-actions">
              <Dialog.Close class="quiet-button" disabled={busy}>
                {t('cancel')}
              </Dialog.Close>
              <button class="primary-button" disabled={busy}>
                {#if node}<KeyRound size={15} />{:else}<Plus
                    size={15}
                  />{/if}{t(node ? 'regenerate' : 'create')}
              </button>
            </div>
          </form>
        {:else}<div class="join-node">
            <KeyRound size={18} /><strong>{enrollment.node.name}</strong><span
              class="node-badge"
            >
              {t('pending')}
            </span>
          </div>
          <label for="join-server">{t('server')}</label><input
            id="join-server"
            bind:value={server}
            oninput={() => (copied = false)}
            spellcheck="false"
          /><label for="join-key">{t('joinKey')}</label><textarea
            id="join-key"
            readonly
            value={enrollment.key}
            rows="4"
            spellcheck="false"></textarea>
          <div class="join-expiry">
            {t('expires')}
            {date(enrollment.expires_at)}
          </div>
          <div class="dialog-actions">
            <Dialog.Close class="quiet-button">{t('close')}</Dialog.Close>
            <button class="primary-button" onclick={copy}>
              {#if copied}<Check size={15} />{:else}<Copy size={15} />{/if}{t(
                copied ? 'copied' : 'copyCommand',
              )}
            </button>
          </div>{/if}
      </div>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>
