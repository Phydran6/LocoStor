<script>
  import { onMount } from 'svelte';
  import Sidebar from './components/Sidebar.svelte';
  import Topbar from './components/Topbar.svelte';
  import Toasts from './components/Toasts.svelte';
  import ConfirmHost from './components/ConfirmHost.svelte';
  import Login from './pages/Login.svelte';
  import Dashboard from './pages/Dashboard.svelte';
  import SmbShares from './pages/SmbShares.svelte';
  import SmbUsers from './pages/SmbUsers.svelte';
  import NfsExports from './pages/NfsExports.svelte';
  import Raid from './pages/Raid.svelte';
  import Smart from './pages/Smart.svelte';
  import Update from './pages/Update.svelte';
  import Settings from './pages/Settings.svelte';
  import { api, session } from './lib/api.svelte.js';
  import { route } from './lib/router.svelte.js';
  import { loadUpdateStatus } from './lib/ui.svelte.js';

  const nav = [
    { label: 'Overview', items: [{ path: '/', title: 'Dashboard', icon: 'dashboard', component: Dashboard }] },
    {
      label: 'Shares',
      items: [
        { path: '/smb/shares', title: 'SMB shares', icon: 'folder', component: SmbShares },
        { path: '/smb/users', title: 'SMB users', icon: 'users', component: SmbUsers },
        { path: '/nfs', title: 'NFS exports', icon: 'network', component: NfsExports },
      ],
    },
    {
      label: 'Storage',
      items: [
        { path: '/raid', title: 'RAID', icon: 'layers', component: Raid },
        { path: '/smart', title: 'SMART', icon: 'disk', component: Smart },
      ],
    },
    {
      label: 'System',
      items: [
        { path: '/update', title: 'Update', icon: 'download', component: Update },
        { path: '/settings', title: 'Settings', icon: 'settings', component: Settings },
      ],
    },
  ];
  const pages = Object.fromEntries(nav.flatMap((g) => g.items).map((i) => [i.path, i]));

  let sidebarOpen = $state(false);
  let page = $derived(pages[route.path] ?? pages['/']);

  onMount(async () => {
    try {
      Object.assign(session, await api.get('/api/auth/me'));
    } catch {
      // server unreachable - login screen shows the error on submit
    }
    session.ready = true;
  });

  $effect(() => {
    if (session.logged_in) loadUpdateStatus();
  });

  $effect(() => {
    document.title = session.logged_in ? `${page.title} · LocoStor` : 'LocoStor';
  });
</script>

{#if !session.ready}
  <div class="flex min-h-screen items-center justify-center">
    <span class="h-6 w-6 animate-spin rounded-full border-2 border-zinc-300 border-t-brand-500 dark:border-zinc-700"></span>
  </div>
{:else if !session.logged_in}
  <Login />
{:else}
  <Sidebar bind:open={sidebarOpen} {nav} />
  <div class="flex min-h-screen min-w-0 flex-col lg:pl-60">
    <Topbar title={page.title} onmenu={() => (sidebarOpen = true)} />
    <main class="mx-auto w-full max-w-7xl flex-1 p-4 sm:p-6">
      {#key page.path}
        <page.component />
      {/key}
    </main>
  </div>
{/if}

<Toasts />
<ConfirmHost />
