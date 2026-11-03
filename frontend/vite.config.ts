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
     port: 5177,
    // '::' 表示双栈通配（IPv4 0.0.0.0 + IPv6 ::），Tailscale 的 v4/v6 地址都能连；
    // 不要写成 'localhost'，那会退化成只监听 127.0.0.1。
    host: '::',
    // 端口被占用时直接报错，而不是自动跳到 5174——否则外部访问固定地址会失败。
    strictPort: true,
    // true = 跳过 Host 校验，任意域名/IP 均可访问（仅开发环境使用；['*'] 不是通配符）
    allowedHosts: true,
    proxy: {
      // 后端默认监听 127.0.0.1:8081（LISTEN_ADDR），显式写 IPv4：
      // localhost 在 Node 里可能优先解析成 ::1，而后端只绑 IPv4，会 ECONNREFUSED。
      '/api': 'http://127.0.0.1:8081',
      '/health': 'http://127.0.0.1:8081',
      '/mcp': 'http://127.0.0.1:8081',
    },
  },
})
