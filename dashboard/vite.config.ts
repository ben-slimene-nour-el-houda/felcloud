/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The dev server proxies /api to the Go control plane (:8081).
const proxyTarget = process.env.VITE_DEV_PROXY_TARGET ?? 'http://localhost:8081';
const devToken = process.env.ADMIN_TOKEN ?? 'felcloud-admin-token-cstam-2026-very-secure';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: proxyTarget,
        changeOrigin: true,
        headers: devToken
          ? {
              Authorization: `Bearer ${devToken}`,
            }
          : undefined,
      },
      '/metrics': {
        target: proxyTarget,
        changeOrigin: true,
      },
    },
  },
  build: { sourcemap: false, target: 'es2022' },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./tests/setup.ts'],
    include: ['tests/**/*.test.{ts,tsx}'],
    css: false,
    restoreMocks: true,
  },
});
