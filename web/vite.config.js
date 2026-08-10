import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// Build output is written directly into the Go embed directory so `go build`
// picks it up with no copy step.
export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../internal/ui/dist',
    emptyOutDir: true,
    target: 'es2022',
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
});
