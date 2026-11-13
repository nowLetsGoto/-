/**
 * 路由表。
 * 职责：声明三条页面路由与兜底重定向，并用 Layout 包裹全站。
 * 输入：react-router 的当前 location；输出：对应页面。
 * 边界：/d/:id 是对外长期可分享地址，未来板块化也不能改；无路由守卫、无动态路由加载。
 */
import { Navigate, Route, Routes } from 'react-router-dom';
import { Layout } from './components/Layout';
import { ListPage } from './pages/ListPage';
import { UploadPage } from './pages/UploadPage';
import { DownloadPage } from './pages/DownloadPage';

export function App(): JSX.Element {
  return (
    <Layout>
      <Routes>
        <Route path="/" element={<ListPage />} />
        <Route path="/upload" element={<UploadPage />} />
        <Route path="/d/:id" element={<DownloadPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Layout>
  );
}
