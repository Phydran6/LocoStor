import { writeFileSync } from 'node:fs';
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

// Keep web/dist/.gitkeep so `go build` works on a fresh clone (go:embed
// needs the directory to exist) and git doesn't report it as deleted.
const keepGitkeep = {
  name: 'keep-gitkeep',
  closeBundle() {
    writeFileSync(new URL('./dist/.gitkeep', import.meta.url), '');
  },
};

export default defineConfig({
  plugins: [svelte(), tailwindcss(), keepGitkeep],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    // `npm run dev` + `go run ./cmd/locostor -demo` for UI development.
    proxy: { '/api': 'http://localhost:8080' },
  },
});
