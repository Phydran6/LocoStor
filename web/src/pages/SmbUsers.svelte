<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Modal from '../components/Modal.svelte';
  import State from '../components/State.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { api } from '../lib/api.svelte.js';
  import { toast, confirm } from '../lib/ui.svelte.js';

  let users = $state(null);
  let error = $state('');

  let modalOpen = $state(false);
  let mode = $state('add'); // add | password
  let name = $state('');
  let password = $state('');
  let repeat = $state('');
  let saving = $state(false);
  let formError = $state('');

  async function load() {
    try {
      users = await api.get('/api/smb/users');
      error = '';
    } catch (e) {
      error = e.message;
    }
  }

  onMount(load);

  function open(m, user = '') {
    mode = m;
    name = user;
    password = repeat = formError = '';
    modalOpen = true;
  }

  async function save(e) {
    e.preventDefault();
    if (password !== repeat) {
      formError = 'Passwords do not match';
      return;
    }
    saving = true;
    formError = '';
    try {
      if (mode === 'add') {
        await api.post('/api/smb/users', { name, password });
        toast.success(`User "${name}" created`);
      } else {
        await api.put(`/api/smb/users/${encodeURIComponent(name)}/password`, { password });
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
      await api.del(`/api/smb/users/${encodeURIComponent(u.name)}`);
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

<div class="card overflow-hidden">
  {#if users === null || users.length === 0}
    <State loading={users === null && !error} {error} empty="No SMB users yet" icon="users" />
  {:else}
    <table class="table">
      <thead><tr><th>User</th><th>UID</th><th class="w-24 text-right">Actions</th></tr></thead>
      <tbody>
        {#each users as u}
          <tr>
            <td class="font-medium text-zinc-900 dark:text-zinc-100">
              {u.name}
              {#if u.full_name}<span class="ml-1 text-xs font-normal text-zinc-500">{u.full_name}</span>{/if}
            </td>
            <td class="mono">{u.uid}</td>
            <td class="text-right whitespace-nowrap">
              <button class="btn-icon" onclick={() => open('password', u.name)} title="Change password" aria-label="Change password of {u.name}"><Icon name="key" size={16} /></button>
              <button class="btn-icon hover:!text-red-600" onclick={() => remove(u)} title="Delete" aria-label="Delete {u.name}"><Icon name="trash" size={16} /></button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<Modal title={mode === 'add' ? 'Add SMB user' : `Change password of "${name}"`} bind:open={modalOpen}>
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
      <input id="u-pw" class="input" type="password" bind:value={password} required minlength="4" autocomplete="new-password" />
    </div>
    <div>
      <label class="label" for="u-pw2">Repeat password</label>
      <input id="u-pw2" class="input" type="password" bind:value={repeat} required autocomplete="new-password" />
    </div>
    {#if formError}<p class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{formError}</p>{/if}
  </form>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (modalOpen = false)}>Cancel</button>
    <button class="btn btn-primary" form="user-form" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
  {/snippet}
</Modal>
