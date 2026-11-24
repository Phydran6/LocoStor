<script>
  // How this Samba server shows up in the network: name, workgroup and
  // whether it announces itself.
  import Icon from './Icon.svelte';
  import Badge from './Badge.svelte';
  import Modal from './Modal.svelte';
  import { api } from '../lib/api.svelte.js';
  import { toast, confirm } from '../lib/ui.svelte.js';

  let { base, where = 'this container', onchange } = $props();

  let info = $state(null);
  let error = $state('');
  let open = $state(false);
  let form = $state({});
  let saving = $state(false);
  let formError = $state('');

  async function load() {
    try {
      info = await api.get(`${base}/network`);
      error = '';
    } catch (e) {
      error = e.message;
    }
  }
  $effect(() => {
    base;
    info = null;
    load();
  });

  export function reload() {
    load();
  }

  function edit() {
    form = { netbios_name: info.netbios_name, workgroup: info.workgroup, server_string: info.server_string, discovery: info.discovery };
    formError = '';
    open = true;
  }

  async function save(e) {
    e.preventDefault();
    const renamed = (form.netbios_name || '') !== (info.netbios_name || '') || form.workgroup.toUpperCase() !== info.workgroup.toUpperCase();
    if (renamed && info.serving) {
      const yes = await confirm({
        title: 'Restart Samba',
        message: 'A new name or workgroup needs a restart of Samba. Open files over SMB are closed for a moment. Continue?',
        confirmLabel: 'Save and restart',
      });
      if (!yes) return;
    }
    saving = true;
    formError = '';
    try {
      info = await api.put(`${base}/network`, form);
      toast.success('Network settings saved');
      open = false;
      onchange?.();
    } catch (err) {
      formError = err.message;
    } finally {
      saving = false;
    }
  }

  async function removePrinters() {
    const yes = await confirm({
      title: 'Remove printer shares',
      message: 'Remove the default [printers] and [print$] sections and turn printer sharing off? A backup of smb.conf is kept.',
      confirmLabel: 'Remove',
    });
    if (!yes) return;
    try {
      info = await api.post(`${base}/network/remove-printers`);
      toast.success('Printer shares removed');
      onchange?.();
    } catch (e) {
      toast.error(e.message);
    }
  }

  const modes = [
    { value: 'auto', label: 'Automatic', hint: 'Visible only while at least one share is offered. Without shares the server stays invisible.' },
    { value: 'on', label: 'Always visible', hint: 'Always announced in the network neighbourhood.' },
    { value: 'off', label: 'Hidden', hint: 'Never announced. Shares still work when opened directly, e.g. \\\\NAS\\Data.' },
  ];
</script>

<section class="card mb-4">
  <div class="card-header">
    <div class="min-w-0">
      <h2 class="card-title">In the network</h2>
      <p class="mt-0.5 text-xs text-zinc-500">How Samba on {where} appears on Windows and macOS.</p>
    </div>
    {#if info?.installed}<button class="btn btn-secondary" onclick={edit}><Icon name="pencil" size={15} />Change</button>{/if}
  </div>
  {#if error}
    <p class="px-4 py-3 text-sm text-red-600 dark:text-red-400">{error}</p>
  {:else if !info}
    <p class="px-4 py-3 text-sm text-zinc-500">Loading…</p>
  {:else if !info.installed}
    <p class="px-4 py-3 text-sm text-zinc-500">Samba is not installed here.</p>
  {:else}
    <div class="grid gap-x-6 gap-y-3 px-4 py-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
      <div>
        <p class="text-xs text-zinc-500">Address</p>
        <p class="mono font-medium text-zinc-900 dark:text-zinc-100">\\{info.name}</p>
        {#if !info.netbios_name}<p class="text-xs text-zinc-500">from the hostname</p>{/if}
      </div>
      <div>
        <p class="text-xs text-zinc-500">Workgroup</p>
        <p class="mono">{info.workgroup}</p>
      </div>
      <div>
        <p class="text-xs text-zinc-500">Network neighbourhood</p>
        <div class="flex flex-wrap items-center gap-1.5">
          <Badge tone={info.visible ? 'ok' : 'muted'} dot>{info.visible ? 'visible' : 'not visible'}</Badge>
          <span class="text-xs text-zinc-500">{modes.find((m) => m.value === info.discovery)?.label}</span>
        </div>
      </div>
      <div>
        <p class="text-xs text-zinc-500">Offers</p>
        <p class="truncate" title={info.shares.join(', ')}>
          {#if info.shares.length}{info.shares.join(', ')}{:else}<span class="text-zinc-500">nothing</span>{/if}
        </p>
        {#if !info.serving && info.manages_smbd}<p class="text-xs text-zinc-500">Samba is stopped until something is shared</p>{/if}
      </div>
    </div>
    {#if info.printers || (!info.wsdd && info.visible)}
      <div class="space-y-1.5 border-t border-zinc-200 px-4 py-2.5 text-xs text-zinc-600 dark:border-ink-800 dark:text-zinc-400">
        {#if info.printers}
          <p class="flex flex-wrap items-center gap-2">
            <Icon name="info" size={14} />The default printer shares (printers, print$) are offered as well.
            <button class="font-medium text-brand-700 hover:underline dark:text-brand-300" onclick={removePrinters}>Remove them</button>
          </p>
        {/if}
        {#if !info.wsdd && info.visible}
          <p class="flex items-center gap-2">
            <Icon name="info" size={14} />Windows 10/11 lists servers via WS-Discovery. Install <span class="mono">wsdd</span> (System packages on the Update page) so it shows up there too.
          </p>
        {/if}
      </div>
    {/if}
  {/if}
</section>

<Modal title="Samba in the network" bind:open>
  <form id="net-form" class="space-y-4" onsubmit={save}>
    <div class="grid gap-4 sm:grid-cols-2">
      <div>
        <label class="label" for="n-name">Server name</label>
        <input id="n-name" class="input mono uppercase" bind:value={form.netbios_name} maxlength="15" placeholder={info?.hostname?.toUpperCase()} />
        <p class="hint">Shown as \\NAME. Empty = hostname ({info?.hostname}).</p>
      </div>
      <div>
        <label class="label" for="n-wg">Workgroup</label>
        <input id="n-wg" class="input mono uppercase" bind:value={form.workgroup} maxlength="15" required disabled={info?.security === 'ads'} />
        <p class="hint">Usually WORKGROUP (Windows default).</p>
      </div>
    </div>
    <div>
      <label class="label" for="n-desc">Description</label>
      <input id="n-desc" class="input" bind:value={form.server_string} placeholder="e.g. NAS in the basement" />
    </div>
    <fieldset>
      <legend class="label">Visible in the network neighbourhood</legend>
      <div class="space-y-2">
        {#each modes as m}
          <label class="flex cursor-pointer items-start gap-2.5 rounded-lg border px-3 py-2 {form.discovery === m.value ? 'border-brand-500 bg-brand-500/5' : 'border-zinc-200 dark:border-ink-800'}">
            <input type="radio" class="mt-1 accent-brand-500" bind:group={form.discovery} value={m.value} />
            <span>
              <span class="text-sm font-medium">{m.label}</span>
              <span class="block text-xs text-zinc-500">{m.hint}</span>
            </span>
          </label>
        {/each}
      </div>
    </fieldset>
    {#if formError}<p class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{formError}</p>{/if}
  </form>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (open = false)}>Cancel</button>
    <button class="btn btn-primary" form="net-form" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
  {/snippet}
</Modal>
