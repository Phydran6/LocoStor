<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import Modal from '../components/Modal.svelte';
  import Segmented from '../components/Segmented.svelte';
  import Toggle from '../components/Toggle.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { api } from '../lib/api.svelte.js';
  import { toast } from '../lib/ui.svelte.js';
  import { showTask } from '../lib/tasks.svelte.js';
  import { dateTime } from '../lib/format.js';

  let st = $state(null);
  let mode = $state('ad');
  let form = $state({ realm: '', workgroup: '', ou: '', uri: '', base_dn: '', bind_dn: '', user_suffix: 'ou=People', group_suffix: 'ou=Groups', start_tls: true });
  let user = $state('Administrator');
  let password = $state('');
  let busy = $state(false);
  let leaveOpen = $state(false);

  async function load() {
    try {
      st = await api.get('/api/directory');
    } catch (e) {
      toast.error(e.message);
    }
  }
  onMount(load);

  async function connect(e) {
    e.preventDefault();
    busy = true;
    try {
      const r = await api.post('/api/directory/connect', { settings: { ...form, mode }, user, password });
      password = '';
      showTask(r.task, { title: mode === 'ad' ? `Join ${form.realm}` : `Connect to ${form.uri}`, onDone: load });
    } catch (err) {
      toast.error(err.message);
    } finally {
      busy = false;
    }
  }

  async function disconnect(e) {
    e.preventDefault();
    try {
      const r = await api.post('/api/directory/disconnect', { user: st.settings.mode === 'ad' ? user : '', password });
      leaveOpen = false;
      password = '';
      showTask(r.task, { title: 'Disconnect the directory', onDone: load });
    } catch (err) {
      toast.error(err.message);
    }
  }
</script>

<PageHeader title="Directory service" description="Use Active Directory or LDAP accounts for the SMB shares of this container" />

{#if st}
  <section class="card mb-4">
    <div class="card-header">
      <h2 class="card-title flex items-center gap-2"><Icon name="building" size={16} />Current state</h2>
      {#if st.settings.mode === 'none'}<Badge>local users</Badge>{:else}<Badge tone={st.connected ? 'ok' : 'bad'} dot>{st.connected ? 'connected' : 'problem'}</Badge>{/if}
    </div>
    <div class="space-y-3 p-4 text-sm">
      {#if st.settings.mode === 'none'}
        <p class="text-zinc-600 dark:text-zinc-300">SMB logins use the local users from <a class="underline" href="#/smb/users">SMB users</a>.</p>
      {:else}
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
          <dt class="text-zinc-500">Type</dt><dd>{st.settings.mode === 'ad' ? 'Active Directory' : 'LDAP (Samba schema)'}</dd>
          {#if st.settings.mode === 'ad'}
            <dt class="text-zinc-500">Domain</dt><dd class="mono">{st.settings.realm} ({st.settings.workgroup})</dd>
          {:else}
            <dt class="text-zinc-500">Server</dt><dd class="mono">{st.settings.uri}</dd>
            <dt class="text-zinc-500">Base DN</dt><dd class="mono">{st.settings.base_dn}</dd>
          {/if}
          <dt class="text-zinc-500">Since</dt><dd>{dateTime(st.since)}</dd>
          <dt class="text-zinc-500">Check</dt><dd class="mono text-xs">{st.detail}</dd>
        </dl>
        <p class="text-xs text-zinc-500">
          On the share pages, grant access to directory accounts like <span class="mono">alice</span> or groups like <span class="mono">@"domain users"</span>.
        </p>
        <button class="btn btn-secondary" onclick={() => ((leaveOpen = true), (password = ''))}>Disconnect</button>
      {/if}
    </div>
  </section>

  {#if st.settings.mode === 'none'}
    <section class="card max-w-3xl">
      <div class="card-header"><h2 class="card-title">Connect</h2></div>
      <form class="space-y-4 p-4 text-sm" onsubmit={connect}>
        <Segmented bind:value={mode} options={[{ value: 'ad', label: 'Active Directory' }, { value: 'ldap', label: 'LDAP' }]} label="Directory type" />
        {#if mode === 'ad'}
          <p class="text-zinc-600 dark:text-zinc-300">
            The container joins the domain as a member server (Samba winbind). Its DNS server must be the domain controller – in Proxmox under
            container → DNS.
          </p>
          <div class="grid gap-3 sm:grid-cols-2">
            <div><label class="label" for="d-realm">Domain (realm)</label><input id="d-realm" class="input mono uppercase" bind:value={form.realm} required placeholder="CORP.EXAMPLE.COM" /></div>
            <div><label class="label" for="d-wg">NetBIOS domain</label><input id="d-wg" class="input mono uppercase" bind:value={form.workgroup} maxlength="15" placeholder="from the realm (CORP)" /></div>
            <div class="sm:col-span-2"><label class="label" for="d-ou">Organizational unit (optional)</label><input id="d-ou" class="input mono" bind:value={form.ou} placeholder="Servers/NAS" /></div>
            <div><label class="label" for="d-user">Domain admin</label><input id="d-user" class="input" bind:value={user} required autocomplete="off" /></div>
            <div><label class="label" for="d-pw">Password</label><input id="d-pw" class="input" type="password" bind:value={password} required autocomplete="off" /><p class="hint">Used once for the join, not stored.</p></div>
          </div>
        {:else}
          <p class="text-zinc-600 dark:text-zinc-300">
            For OpenLDAP with the Samba schema (<span class="mono">sambaSamAccount</span>): SMB passwords come from the directory (ldapsam), Unix
            accounts via nslcd.
          </p>
          <div class="grid gap-3 sm:grid-cols-2">
            <div class="sm:col-span-2"><label class="label" for="l-uri">Server</label><input id="l-uri" class="input mono" bind:value={form.uri} required placeholder="ldaps://ldap.example.com" /></div>
            <div><label class="label" for="l-base">Base DN</label><input id="l-base" class="input mono" bind:value={form.base_dn} required placeholder="dc=example,dc=com" /></div>
            <div><label class="label" for="l-bind">Bind DN</label><input id="l-bind" class="input mono" bind:value={form.bind_dn} required placeholder="cn=admin,dc=example,dc=com" /></div>
            <div><label class="label" for="l-us">User suffix</label><input id="l-us" class="input mono" bind:value={form.user_suffix} /></div>
            <div><label class="label" for="l-gs">Group suffix</label><input id="l-gs" class="input mono" bind:value={form.group_suffix} /></div>
            <div class="sm:col-span-2"><label class="label" for="l-pw">Bind password</label><input id="l-pw" class="input" type="password" bind:value={password} required autocomplete="off" /><p class="hint">Stored only where Samba and nslcd need it (root only).</p></div>
          </div>
          <Toggle bind:checked={form.start_tls} label="STARTTLS" hint="Encrypt ldap:// connections. Not needed with ldaps://." />
        {/if}
        <button class="btn btn-primary" disabled={busy}><Icon name="plug" size={16} />{mode === 'ad' ? 'Join domain' : 'Connect'}</button>
        <p class="hint">Packages are installed and the output is shown live. smb.conf, krb5.conf and nsswitch.conf are backed up first.</p>
      </form>
    </section>
  {/if}
{/if}

<Modal title="Disconnect the directory" bind:open={leaveOpen}>
  <form id="leave-form" class="space-y-3 text-sm" onsubmit={disconnect}>
    <p class="text-zinc-600 dark:text-zinc-300">Samba goes back to local users. Shares that list directory accounts lose them.</p>
    {#if st?.settings.mode === 'ad'}
      <p class="hint">Optional: with domain admin credentials the computer account is removed from the domain as well.</p>
      <div class="grid gap-3 sm:grid-cols-2">
        <input class="input" bind:value={user} placeholder="Administrator" autocomplete="off" />
        <input class="input" type="password" bind:value={password} placeholder="Password" autocomplete="off" />
      </div>
    {/if}
  </form>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (leaveOpen = false)}>Cancel</button>
    <button class="btn btn-danger" form="leave-form">Disconnect</button>
  {/snippet}
</Modal>
