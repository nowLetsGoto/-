/**
 * Vite 配置。
 * 职责：React 开发服务器、API 同源代理、把前端产物输出到 Go 的静态托管目录。
 * 输入：web/src 源码、api/openapi.yaml 生成的前端类型。
 * 输出：开发服务器（0.0.0.0:5173）与 server/public/ 下的生产产物。
 * 边界：不承载业务路由；SPA history fallback 由 Vite dev / Go embed / Nginx 三处各自负责。
 */
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    // 手机与开发机同网段时可打开开发服务器，PUBLIC_BASE_URL 才能指向它。
    host: '192.168.6.14',
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    // 产物落在 server/public/，供 Go 的 //go:embed public/* 打进单二进制。
    outDir: '../server/public',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        // 把 antd 单独拆包：它体积大且极少变动，独立成 chunk 后业务代码更新不会让它重新下载。
        manualChunks: {
          antd: ['antd'],
        },
      },
    },
  },
});
