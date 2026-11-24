<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Modal from '../components/Modal.svelte';
  import State from '../components/State.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import ScopeTabs from '../components/ScopeTabs.svelte';
  import { api } from '../lib/api.svelte.js';
  import { toast, confirm } from '../lib/ui.svelte.js';
  import { host, scope, loadHost } from '../lib/host.svelte.js';

  import Badge from '../components/Badge.svelte';

  let users = $state(null);
  let error = $state('');
  let net = $state(null);
  let dir = $state(null);
  let onHost = $derived(!!host.info?.available && scope.value === 'host');
  let base = $derived(onHost ? '/api/host/smb/users' : '/api/smb/users');
  let v2 = $derived(!onHost || host.info?.features?.includes('users-v2'));

  let modalOpen = $state(false);
  let mode = $state('add'); // add | password | test
  let testResult = $state(null);
  let name = $state('');
  let password = $state('');
  let repeat = $state('');
  let saving = $state(false);
  let formError = $state('');

  async function load() {
    try {
      users = await api.get(base);
      error = '';
    } catch (e) {
      error = e.message;
    }
    api.get(base.replace(/\/users$/, '/network')).then((n) => (net = n)).catch(() => (net = null));
    if (!onHost) api.get('/api/directory').then((d) => (dir = d)).catch(() => (dir = null));
  }

  onMount(() => loadHost());

  // Load once the host state is known, and again when the tab changes.
  let lastScope = '';
  $effect(() => {
    const key = `${onHost}`;
    if (host.info && key !== lastScope) {
      lastScope = key;
      users = null;
      load();
    }
  });

  function open(m, user = '') {
    mode = m;
    name = user;
    password = repeat = formError = '';
    testResult = null;
    modalOpen = true;
  }

  async function test() {
    saving = true;
    testResult = null;
    try {
      testResult = await api.post(`${base}/${encodeURIComponent(name)}/test`, { password });
    } catch (err) {
      testResult = { ok: false, error: err.message };
    } finally {
      saving = false;
    }
  }

  async function setEnabled(u, enabled) {
    try {
      await api.put(`${base}/${encodeURIComponent(u.name)}/enabled`, { enabled });
      toast.success(`"${u.name}" ${enabled ? 'enabled' : 'disabled'}`);
      load();
    } catch (err) {
      toast.error(err.message);
    }
  }

  async function repair(u) {
    try {
      await api.post(`${base}/${encodeURIComponent(u.name)}/repair`);
      toast.success(`"${u.name}" repaired`);
      load();
    } catch (err) {
      toast.error(err.message);
    }
  }

  async function save(e) {
    e.preventDefault();
    if (mode === 'test') {
      test();
      return;
    }
    if (password !== repeat) {
      formError = 'Passwords do not match';
      return;
    }
    saving = true;
    formError = '';
    try {
      if (mode === 'add') {
        await api.post(base, { name, password });
        toast.success(`User "${name}" created`);
      } else {
        await api.put(`${base}/${encodeURIComponent(name)}/password`, { password });
        toast.success(`Password for "${name}" changed`);
      }
      modalOpen = false;
      load();
    } catch (err) {
      formError = err.message;
    } finally {
      saving = false;
    }
  }

  async function remove(u) {
    const yes = await confirm({
      title: 'Delete SMB user',
      message: `Remove "${u.name}" from Samba? The Linux account and files are kept.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!yes) return;
    try {
      await api.del(`${base}/${encodeURIComponent(u.name)}`);
      toast.success(`User "${u.name}" removed`);
      load();
    } catch (err) {
      toast.error(err.message);
    }
  }
</script>

<PageHeader title="SMB users" description="Accounts that can log in to SMB shares">
  {#snippet actions()}
    <button class="btn btn-primary" onclick={() => open('add')}><Icon name="plus" size={16} />Add user</button>
  {/snippet}
</PageHeader>

<ScopeTabs />

<p class="mb-3 flex flex-wrap items-center gap-1.5 text-sm text-zinc-600 dark:text-zinc-400">
  <Icon name="info" size={15} />
  These accounts log in to the Samba server on
  <b class="font-medium text-zinc-800 dark:text-zinc-200">{onHost ? `the Proxmox host (${host.info?.hostname})` : 'this container'}</b>
  {#if net?.name}– <span class="mono">\\{net.name}</span>{/if}. Users of the other server do not work here.
</p>

{#if !onHost && dir && dir.settings.mode !== 'none'}
  <p class="mb-3 flex items-center gap-1.5 rounded-lg bg-sky-500/10 px-3 py-2 text-sm text-sky-800 dark:text-sky-300">
    <Icon name="building" size={15} />Connected to {dir.settings.mode === 'ad' ? `the domain ${dir.settings.realm}` : `LDAP ${dir.settings.uri}`} – directory accounts log in with their own password.
    <a class="underline" href="#/integrations/directory">Details</a>
  </p>
{/if}

<div class="card overflow-hidden">
  {#if users === null || users.length === 0}
    <State loading={users === null && !error} {error} empty="No SMB users yet" icon="users" />
  {:else}
    <table class="table">
      <thead><tr><th>User</th><th>UID</th><th>Status</th><th class="w-40 text-right">Actions</th></tr></thead>
      <tbody>
        {#each users as u}
          <tr>
            <td class="font-medium text-zinc-900 dark:text-zinc-100">
              {u.name}
              {#if u.full_name}<span class="ml-1 text-xs font-normal text-zinc-500">{u.full_name}</span>{/if}
            </td>
            <td class="mono">{u.uid || '–'}</td>
            <td>
              {#if !v2}
                <Badge>–</Badge>
              {:else if u.problems?.length}
                <div class="space-y-0.5">
                  {#each u.problems as p}<p class="flex items-center gap-1 text-xs text-amber-700 dark:text-amber-400"><Icon name="alert" size={13} />{p}</p>{/each}
                </div>
              {:else}
                <Badge tone="ok" dot>can log in</Badge>
              {/if}
            </td>
            <td class="text-right whitespace-nowrap">
              {#if v2}
                {#if u.problems?.length}
                  <button class="btn btn-secondary mr-1 !px-2 !py-1 text-xs" onclick={() => repair(u)}>Repair</button>
                {/if}
                <button class="btn-icon" onclick={() => open('test', u.name)} title="Test login" aria-label="Test login of {u.name}"><Icon name="play" size={16} /></button>
                <button class="btn-icon" onclick={() => setEnabled(u, u.disabled)} title={u.disabled ? 'Enable' : 'Disable'} aria-label="{u.disabled ? 'Enable' : 'Disable'} {u.name}">
                  <Icon name={u.disabled ? 'userCheck' : 'userX'} size={16} />
                </button>
              {/if}
              <button class="btn-icon" onclick={() => open('password', u.name)} title="Change password" aria-label="Change password of {u.name}"><Icon name="key" size={16} /></button>
              <button class="btn-icon hover:!text-red-600" onclick={() => remove(u)} title="Delete" aria-label="Delete {u.name}"><Icon name="trash" size={16} /></button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<Modal
  title={mode === 'add' ? `Add SMB user${onHost ? ' on the host' : ''}` : mode === 'test' ? `Test login of "${name}"` : `Change password of "${name}"`}
  bind:open={modalOpen}
>
  <form id="user-form" class="space-y-4" onsubmit={save}>
    {#if mode === 'add'}
      <div>
        <label class="label" for="u-name">User name</label>
        <input id="u-name" class="input" bind:value={name} required pattern="[a-z_][a-z0-9_\-]*" maxlength="32" autocomplete="off" />
        <p class="hint">Lowercase letters, digits, "_" and "-". A Linux account without login is created if needed.</p>
      </div>
    {/if}
    <div>
      <label class="label" for="u-pw">Password</label>
      <input id="u-pw" class="input" type="password" bind:value={password} required minlength={mode === 'test' ? 1 : 8} autocomplete={mode === 'test' ? 'off' : 'new-password'} />
    </div>
    {#if mode === 'test'}
      <p class="hint">Logs in to Samba on {onHost ? 'the host' : 'this container'} like a Windows PC would and lists the shares the user sees.</p>
      {#if testResult}
        {#if testResult.ok}
          <div class="rounded-lg bg-emerald-500/10 px-3 py-2 text-sm text-emerald-800 dark:text-emerald-300">
            <p class="flex items-center gap-1.5 font-medium"><Icon name="check" size={16} />Login works</p>
            <p class="mt-1 text-xs">Visible shares: {testResult.shares.length ? testResult.shares.join(', ') : 'none'}</p>
          </div>
        {:else}
          <div class="rounded-lg bg-red-500/10 px-3 py-2 text-sm text-red-700 dark:text-red-400">
            <p class="flex items-center gap-1.5 font-medium"><Icon name="alert" size={16} />{testResult.error}</p>
            {#if testResult.hint}<p class="mt-1 text-xs">{testResult.hint}</p>{/if}
          </div>
        {/if}
      {/if}
    {:else}
      <div>
        <label class="label" for="u-pw2">Repeat password</label>
        <input id="u-pw2" class="input" type="password" bind:value={repeat} required autocomplete="new-password" />
      </div>
    {/if}
    {#if formError}<p class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{formError}</p>{/if}
  </form>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (modalOpen = false)}>Cancel</button>
    <button class="btn btn-primary" form="user-form" disabled={saving}>{saving ? (mode === 'test' ? 'Testing…' : 'Saving…') : mode === 'test' ? 'Test login' : 'Save'}</button>
  {/snippet}
</Modal>
