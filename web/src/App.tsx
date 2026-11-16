/**
 * 路由表。
 * 职责：声明登录页、四个业务页面与公开下载页；把业务页面收进「登录守卫 + 骨架」这一层。
 * 输入：react-router 的当前 location；输出：对应页面。
 * 边界：/d/:id 是对外长期可分享地址，任何人扫码都能看，既不套守卫也不套后台骨架
 *       （访客不该看到内部导航）；无动态路由加载、无角色判断。
 */
import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom';
import { readSession } from './api/session';
import { Layout } from './components/Layout';
import { AppsPage } from './pages/AppsPage';
import { DownloadPage } from './pages/DownloadPage';
import { OverviewPage } from './pages/OverviewPage';
import { PackagesPage } from './pages/PackagesPage';
import { SettingsPage } from './pages/SettingsPage';
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

      {/* 公开下载页：独立居中，不套后台骨架 */}
      <Route
        path="/d/:id"
        element={
          <div className="public-shell">
            <DownloadPage />
          </div>
        }
      />

      <Route element={<RequireAuth />}>
        <Route element={<Layout><Outlet /></Layout>}>
          <Route path="/" element={<OverviewPage />} />
          <Route path="/packages" element={<PackagesPage />} />
          <Route path="/apps" element={<AppsPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/upload" element={<UploadPage />} />
        </Route>
      </Route>

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
