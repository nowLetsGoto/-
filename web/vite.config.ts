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
  css: {
    // 用 Dart Sass 的现代编译器 API，避开 legacy-js-api 弃用警告。
    preprocessorOptions: { scss: { api: 'modern-compiler' } },
  },
  server: {
    // 监听本机所有 IPv4 网卡，局域网 IP 变化后仍可启动并供同网段手机访问。
    host: '0.0.0.0',
    proxy: {
      '/api': {
        target: 'http://localhost:10088',
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
        // 组件库单独拆包：antd 与 console-kit 体积大且极少变动，业务代码更新不会让它们重新下载。
        manualChunks: {
          antd: ['antd'],
          'console-kit': ['@seakoi/console-kit'],
        },
      },
    },
  },
});
