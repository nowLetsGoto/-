/**
 * 前端入口。
 * 职责：引入全局样式、挂载 React、提供 BrowserRouter 与 console-kit 全局主题。
 * 输入：index.html 的 #root 节点；输出：运行中的 SPA。
 * 边界：只做挂载与全局 Provider，不放任何业务逻辑或请求代码。
 */
import React from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { ConfigProvider, zhCN } from '@seakoi/console-kit';
import 'dayjs/locale/zh-cn';
import { App } from './App';
import { antdTheme } from './theme/antd';
import './styles.scss';

const container = document.getElementById('root');
if (!container) {
  throw new Error('缺少 #root 挂载点');
}

createRoot(container).render(
  <React.StrictMode>
    {/* console-kit 组件共用主题与中文语言包；业务页面不重复配置 Provider。 */}
    <ConfigProvider theme={antdTheme} locale={zhCN}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </ConfigProvider>
  </React.StrictMode>,
);
