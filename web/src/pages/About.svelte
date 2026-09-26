<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import logo from '../assets/logo.png';
  import { api } from '../lib/api.svelte.js';

  let about = $state(null);
  onMount(async () => {
    try {
      about = await api.get('/api/about');
    } catch {
      about = { version: '', repo: 'https://github.com/Phydran6/LocoStor' };
    }
  });

  let links = $derived(
    about
      ? [
          { href: about.repo, icon: 'github', title: 'Source code', text: 'The repository on GitHub' },
          { href: `${about.repo}#readme`, icon: 'info', title: 'Documentation', text: 'Features, configuration, commands' },
          { href: `${about.repo}/blob/main/docs/proxmox.md`, icon: 'server', title: 'Proxmox guide', text: 'Container setup, disks, troubleshooting' },
          { href: `${about.repo}/blob/main/CHANGELOG.md`, icon: 'layers', title: 'Changelog', text: 'What changed in each version' },
          { href: `${about.repo}/releases`, icon: 'download', title: 'Releases', text: 'All versions and downloads' },
          { href: `${about.repo}/issues/new`, icon: 'alert', title: 'Report a problem', text: 'Open an issue on GitHub' },
        ]
      : [],
  );
</script>

<PageHeader title="About" description="Documentation and source code" />

<div class="grid max-w-5xl gap-4 lg:grid-cols-3">
  <section class="card flex flex-col items-center justify-center gap-3 overflow-hidden bg-[#05060c] p-6 text-center dark:bg-[#05060c]">
    <img src={logo} alt="LocoStor" class="w-44" />
    <p class="text-sm text-zinc-400">Version <span class="mono text-brand-300">{about?.version ?? '…'}</span></p>
    <p class="text-xs text-zinc-500">MIT License</p>
  </section>

  <section class="card lg:col-span-2">
    <ul class="grid divide-zinc-200 sm:grid-cols-2 dark:divide-ink-800">
      {#each links as l}
        <li>
          <a
            href={l.href}
            target="_blank"
            rel="noopener noreferrer"
            class="group flex items-start gap-3 border-b border-zinc-100 px-4 py-4 hover:bg-zinc-50 sm:odd:border-r dark:border-ink-800 dark:hover:bg-ink-850"
          >
            <Icon name={l.icon} size={18} class="mt-0.5 text-zinc-400 group-hover:text-brand-500" />
            <span class="flex-1">
              <span class="flex items-center gap-1 text-sm font-medium text-zinc-900 dark:text-zinc-100">
                {l.title}
                <Icon name="external" size={12} class="text-zinc-400" />
              </span>
              <span class="text-xs text-zinc-500">{l.text}</span>
            </span>
          </a>
        </li>
      {/each}
    </ul>
  </section>
</div>
