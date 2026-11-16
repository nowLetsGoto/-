/**
 * 路由表。
 * 职责：声明登录页、项目内页面（/p/:key/*）、平台级页面（/settings）与公开下载页；
 *       把业务页面收进「登录守卫 + 骨架」这一层。
 * 输入：react-router 的当前 location；输出：对应页面。
 * 边界：/d/:id 是对外长期可分享地址，既不套守卫也不套后台骨架（访客不该看到内部导航）；
 *       项目内页面必须带 /p/:key 前缀，平台级页面不带——这条边界决定了面包屑与导航高亮。
 */
import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom';
import { readSession } from './api/session';
import { AdminGate } from './components/AdminGate';
import { Layout } from './components/Layout';
import { Empty, ErrorState, Loading } from './components/States';
import { recentProjectKey, useProjects } from './hooks/useProjects';
import { useMe } from './hooks/useMe';
import { AppsPage } from './pages/distribution/AppsPage';
import { DownloadPage } from './pages/public/DownloadPage';
import { OverviewPage } from './pages/overview/OverviewPage';
import { PackagesPage } from './pages/distribution/PackagesPage';
import { SettingsPage } from './pages/platform/SettingsPage';
import { UploadPage } from './pages/distribution/UploadPage';
import { AccountFormPage } from './pages/admin/AccountFormPage';
import { AccountsPage } from './pages/admin/AccountsPage';
import { AuditLogsPage } from './pages/admin/AuditLogsPage';
import { PeoplePage } from './pages/directory/PeoplePage';
import { PersonFormPage } from './pages/directory/PersonFormPage';
import { ProjectFormPage } from './pages/directory/ProjectFormPage';
import { ProjectsPage } from './pages/directory/ProjectsPage';
import { TeamFormPage } from './pages/directory/TeamFormPage';
import { TeamsPage } from './pages/directory/TeamsPage';
import { LoginPage } from './pages/login/LoginPage';

/** 未登录时拦到登录页，并记下来访地址，登录成功后原路返回。 */
function RequireAuth(): JSX.Element {
  const location = useLocation();
  if (!readSession()) {
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  }
  return <Outlet />;
}

/** 根路径没有项目上下文：默认进「我关联的第一个项目」，其次最近访问，最后全局第一个。 */
function ProjectHome(): JSX.Element {
  const { projects, loading, error } = useProjects();
  const { me } = useMe();

  if (loading) {
    return (
      <div className="public-shell">
        <Loading text="正在读取项目…" />
      </div>
    );
  }
  if (error) {
    return (
      <div className="public-shell">
        <ErrorState message={error} />
      </div>
    );
  }

  const preferred = me?.projectKeys[0];
  const target =
    (preferred ? projects.find((item) => item.key === preferred) : undefined) ??
    projects.find((item) => item.key === recentProjectKey()) ??
    projects[0];

  // 没有项目时留在骨架里，侧栏导航仍可用，去「项目管理」新建即可。
  if (!target) {
    return (
      <Layout>
        <Empty title="还没有项目" description="先到「基础资料 → 项目管理」建一个项目，再上传第一个包。" />
      </Layout>
    );
  }
  return <Navigate to={`/p/${target.key}/overview`} replace />;
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
        <Route path="/" element={<ProjectHome />} />

        {/* 项目内页面：切换项目就是换 :key，页面组件完全复用 */}
        <Route path="/p/:key" element={<Layout><Outlet /></Layout>}>
          <Route index element={<Navigate to="overview" replace />} />
          <Route path="overview" element={<OverviewPage />} />
          <Route path="packages" element={<PackagesPage />} />
          <Route path="apps" element={<AppsPage />} />
          <Route path="upload" element={<UploadPage />} />
        </Route>

        {/* 平台级页面：与项目无关（基础资料 + 系统设置） */}
        <Route element={<Layout><Outlet /></Layout>}>
          {/* 列表与表单刻意拆成不同路由：列表只读、表单独立成页，避免一个页面两副面孔 */}
          <Route path="/directory/people" element={<PeoplePage />} />
          <Route path="/directory/people/new" element={<PersonFormPage />} />
          <Route path="/directory/people/:id/edit" element={<PersonFormPage />} />
          <Route path="/directory/teams" element={<TeamsPage />} />
          <Route path="/directory/teams/new" element={<TeamFormPage />} />
          <Route path="/directory/teams/:id/edit" element={<TeamFormPage />} />
          <Route path="/directory/projects" element={<ProjectsPage />} />
          <Route path="/directory/projects/new" element={<ProjectFormPage />} />
          <Route path="/directory/projects/:key/edit" element={<ProjectFormPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          {/* 超级管理员专区：AdminGate 只做体验层拦截，接口层还有 403 兜底 */}
          <Route
            path="/admin/accounts"
            element={
              <AdminGate>
                <AccountsPage />
              </AdminGate>
            }
          />
          <Route
            path="/admin/accounts/new"
            element={
              <AdminGate>
                <AccountFormPage />
              </AdminGate>
            }
          />
          <Route
            path="/admin/audit-logs"
            element={
              <AdminGate>
                <AuditLogsPage />
              </AdminGate>
            }
          />
        </Route>
      </Route>

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
