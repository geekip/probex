import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import { viteStaticCopy } from 'vite-plugin-static-copy';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig(async ({ command, mode }) => ({
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  plugins: [
    svelte(),
    tailwindcss(),
    viteStaticCopy({
      targets: [
        {
          src: 'node_modules/pdfjs-dist/{cmaps,standard_fonts,wasm,iccs}/**/*',
          dest: 'pdfjs',
          rename: { stripBase: 2 },
        },
      ],
    }),
    ...(command === 'serve' && mode === 'mock'
      ? [(await import('./mock/plugin')).mockPlugin()]
      : []),
  ],
  optimizeDeps: {
    include: [
      'codemirror',
      '@codemirror/state',
      '@codemirror/view',
      '@codemirror/commands',
      '@codemirror/search',
      '@codemirror/language',
      '@codemirror/language-data',
      'pdfjs-dist',
    ],
  },
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
