/**
 * 前端入口。
 * 职责：引入全局样式、挂载 React、提供 BrowserRouter 与 antd 主题。
 * 输入：index.html 的 #root 节点；输出：运行中的 SPA。
 * 边界：只做挂载与全局 Provider，不放任何业务逻辑或请求代码。
 */
import React from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import 'dayjs/locale/zh-cn';
import { App } from './App';
import { antdTheme } from './theme/antd';
import './styles.css';

const container = document.getElementById('root');
if (!container) {
  throw new Error('缺少 #root 挂载点');
}

createRoot(container).render(
  <React.StrictMode>
    {/* antd 只在 components/DataTable 等薄封装里使用，主题与中文语言包统一在这里注入 */}
    <ConfigProvider theme={antdTheme} locale={zhCN}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </ConfigProvider>
  </React.StrictMode>,
);
