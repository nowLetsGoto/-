/**
 * 前端入口。
 * 职责：引入全局样式、挂载 React、提供 BrowserRouter。
 * 输入：index.html 的 #root 节点；输出：运行中的 SPA。
 * 边界：只做挂载，不放任何业务逻辑或请求代码。
 */
import React from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { App } from './App';
import './styles.css';

const container = document.getElementById('root');
if (!container) {
  throw new Error('缺少 #root 挂载点');
}

createRoot(container).render(
  <React.StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </React.StrictMode>,
);
