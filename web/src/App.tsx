/**
 * 路由表。
 * 职责：声明登录页与三条业务路由，把「需登录」的路由收进同一个守卫，并用 Layout 包裹全站（登录页除外）。
 * 输入：react-router 的当前 location；输出：对应页面。
 * 边界：/d/:id 是对外长期可分享地址，任何人扫码都能看，不能套登录守卫；
 *       无动态路由加载、无角色判断（只有「已登录 / 未登录」两种状态）。
 */
import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom';
import { readSession } from './api/session';
import { Layout } from './components/Layout';
import { DownloadPage } from './pages/DownloadPage';
import { ListPage } from './pages/ListPage';
import { UploadPage } from './pages/UploadPage';
import { LoginPage } from './pages/login/LoginPage';

/** 未登录时拦到登录页，并记下来访地址，登录成功后原路返回。 */
function RequireAuth(): JSX.Element {
  const location = useLocation();
  if (!readSession()) {
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  }
  return <Outlet />;
}

export function App(): JSX.Element {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />

      <Route element={<RequireAuth />}>
        <Route element={<Layout><Outlet /></Layout>}>
          <Route path="/" element={<ListPage />} />
          <Route path="/upload" element={<UploadPage />} />
        </Route>
      </Route>

      {/* 下载页保持公开：扫码转发的对象不会登录，Layout 也不会为他们渲染账号区。 */}
      <Route path="/d/:id" element={<Layout><DownloadPage /></Layout>} />

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
