import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig(async ({ command, mode }) => ({
  plugins: [
    svelte(),
    tailwindcss(),
    ...(command === 'serve' && mode === 'mock'
      ? [(await import('./mock/plugin')).mockPlugin()]
      : []),
  ],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    rollupOptions: {
      output: {
        manualChunks: {
          xterm: [
            '@xterm/xterm',
            '@xterm/addon-fit',
            '@xterm/addon-search',
            '@xterm/addon-web-links',
          ],
        },
      },
    },
  },
  server: {
    proxy:
      mode === 'mock'
        ? undefined
        : {
            '/api': { target: 'http://127.0.0.1:17891', ws: true },
          },
  },
}));
