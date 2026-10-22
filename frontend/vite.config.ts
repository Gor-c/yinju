import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  optimizeDeps: {
    // The Office renderers ship UMD bundles that require("vue-demi"). Vite must
    // pre-bundle them so the browser receives ESM; excluding them breaks the
    // preview at runtime with "defineComponent of undefined".
    include: ['@vue-office/docx', '@vue-office/excel', '@vue-office/pdf'],
  },
  build: {
    // 拆出 UI 库与运行时，避免首屏一个 2.7MB 的巨型 chunk；
    // Element Plus 目前仍是全量引入，后续可换成按需自动导入。
    rollupOptions: {
      output: {
        manualChunks: {
          'element-plus': ['element-plus'],
          'vue-vendor': ['vue', 'vue-router', 'axios'],
        },
      },
    },
  },
  server: {
    // '::' 双栈监听：同时接受 IPv4 与 IPv6（Tailscale 的 AAAA 记录也能直连）
    host: '::',
    // true = 跳过 Host 校验，任意域名/IP 均可访问（仅开发环境使用；['*'] 不是通配符）
    allowedHosts: true,
    proxy: {
      '/api': 'http://localhost:8081',
      '/health': 'http://localhost:8081',
      '/mcp': 'http://localhost:8081',
    },
  },
})
