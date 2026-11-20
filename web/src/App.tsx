/**
 * 路由表。
 * 职责：声明登录页、项目内页面（/p/:key/*）、平台级页面（/settings）与公开下载页；
 *       把业务页面收进「登录守卫 + 骨架」这一层，并按路由做代码分割。
 * 输入：react-router 的当前 location；输出：对应页面。
 * 边界：/d/:id 是对外长期可分享地址，既不套守卫也不套后台骨架（访客不该看到内部导航）；
 *       项目内页面必须带 /p/:key 前缀，平台级页面不带——这条边界决定了面包屑与导航高亮；
 *       页面一律懒加载，加载态与失败态由 PageBoundary 统一兜住，骨架本身不懒加载。
 */
import { lazy, Suspense } from 'react';
import type { ReactNode } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom';
import { readSession } from './api/session';
import { AdminGate } from './components/AdminGate';
import { Layout } from './components/Layout';
import { LevelGate } from './components/LevelGate';
import { PermissionGate } from './components/PermissionGate';
import { NoProjectYet } from './components/ProjectGate';
import { ErrorState, Loading } from './components/States';
import { recentProjectKey, useProjects } from './hooks/useProjects';
import { useMe } from './hooks/useMe';

/** 页面组件均为 named export，转成 lazy 需要的 default 形态。 */
const LoginPage = lazy(() =>
  import('./pages/login/LoginPage').then((m) => ({ default: m.LoginPage })),
);
const DownloadPage = lazy(() =>
  import('./pages/public/DownloadPage').then((m) => ({ default: m.DownloadPage })),
);
const OverviewPage = lazy(() =>
  import('./pages/overview/OverviewPage').then((m) => ({ default: m.OverviewPage })),
);
const PackagesPage = lazy(() =>
  import('./pages/distribution/PackagesPage').then((m) => ({ default: m.PackagesPage })),
);
const AppsPage = lazy(() =>
  import('./pages/distribution/AppsPage').then((m) => ({ default: m.AppsPage })),
);
const UploadPage = lazy(() =>
  import('./pages/distribution/UploadPage').then((m) => ({ default: m.UploadPage })),
);
const SettingsPage = lazy(() =>
  import('./pages/platform/SettingsPage').then((m) => ({ default: m.SettingsPage })),
);
const PeoplePage = lazy(() =>
  import('./pages/directory/PeoplePage').then((m) => ({ default: m.PeoplePage })),
);
const PersonFormPage = lazy(() =>
  import('./pages/directory/PersonFormPage').then((m) => ({ default: m.PersonFormPage })),
);
const ProjectsPage = lazy(() =>
  import('./pages/directory/ProjectsPage').then((m) => ({ default: m.ProjectsPage })),
);
const ProjectFormPage = lazy(() =>
  import('./pages/directory/ProjectFormPage').then((m) => ({ default: m.ProjectFormPage })),
);
const TeamsPage = lazy(() =>
  import('./pages/directory/TeamsPage').then((m) => ({ default: m.TeamsPage })),
);
const TeamFormPage = lazy(() =>
  import('./pages/directory/TeamFormPage').then((m) => ({ default: m.TeamFormPage })),
);
const RolesPage = lazy(() =>
  import('./pages/directory/RolesPage').then((m) => ({ default: m.RolesPage })),
);
const RoleFormPage = lazy(() =>
  import('./pages/directory/RoleFormPage').then((m) => ({ default: m.RoleFormPage })),
);
const AccountsPage = lazy(() =>
  import('./pages/admin/AccountsPage').then((m) => ({ default: m.AccountsPage })),
);
const AccountFormPage = lazy(() =>
  import('./pages/admin/AccountFormPage').then((m) => ({ default: m.AccountFormPage })),
);
const AuditLogsPage = lazy(() =>
  import('./pages/admin/AuditLogsPage').then((m) => ({ default: m.AuditLogsPage })),
);
const TenantsPage = lazy(() =>
  import('./pages/admin/TenantsPage').then((m) => ({ default: m.TenantsPage })),
);
const TenantFormPage = lazy(() =>
  import('./pages/admin/TenantFormPage').then((m) => ({ default: m.TenantFormPage })),
);

/**
 * 页面异步边界。
 * 职责：给懒加载页面套上统一的加载占位与 chunk 失败兜底。
 * 输入：children（懒加载页面）、可选加载文案。
 * 输出：ErrorBoundary + Suspense 包裹的子树。
 * 边界：只处理 chunk 级异步态；页面自身的请求三态仍由叶子页面负责。
 */
function PageBoundary({
  children,
  text = '正在加载页面…',
}: {
  children: ReactNode;
  text?: string;
}): JSX.Element {
  const location = useLocation();
  return (
    <ErrorBoundary
      // 切换路由即重置错误态，避免同一个 ShellLayout 下失败后切页仍卡在 fallback。
      resetKeys={[location.pathname]}
      fallbackRender={() => (
        <ErrorState
          message="页面暂时无法显示，请重试"
          // chunk 加载失败后 lazy 会缓存 rejected promise，必须整页重载才能重新拉取。
          onRetry={() => window.location.reload()}
        />
      )}
    >
      <Suspense fallback={<Loading text={text} />}>{children}</Suspense>
    </ErrorBoundary>
  );
}

/** 后台骨架路由：把异步边界放在 Outlet 外，切页时只替换内容区，侧边栏与顶栏保持不动。 */
function ShellLayout(): JSX.Element {
  return (
    <Layout>
      <PageBoundary>
        <Outlet />
      </PageBoundary>
    </Layout>
  );
}

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

  // 没有项目时留在骨架里，侧栏导航仍可用；引导文案复用守卫里的同一张卡，避免两处说法不一致。
  if (!target) {
    return (
      <Layout>
        <NoProjectYet />
      </Layout>
    );
  }
  return <Navigate to={`/p/${target.key}/overview`} replace />;
}

export function App(): JSX.Element {
  return (
    <Routes>
      <Route
        path="/login"
        element={
          <PageBoundary text="正在进入登录页…">
            <LoginPage />
          </PageBoundary>
        }
      />

      {/* 公开下载页：独立居中，不套后台骨架 */}
      <Route
        path="/d/:id"
        element={
          <div className="public-shell public-download-shell">
            <PageBoundary text="正在打开下载页…">
              <DownloadPage />
            </PageBoundary>
          </div>
        }
      />

      <Route element={<RequireAuth />}>
        <Route path="/" element={<ProjectHome />} />

        {/* 项目内页面：切换项目就是换 :key，页面组件完全复用 */}
        <Route path="/p/:key" element={<ShellLayout />}>
          <Route index element={<Navigate to="overview" replace />} />
          <Route path="overview" element={<OverviewPage />} />
          <Route path="packages" element={<PackagesPage />} />
          <Route path="apps" element={<AppsPage />} />
          <Route path="upload" element={<UploadPage />} />
        </Route>

        {/* 平台级页面：与项目无关（基础资料 + 系统设置） */}
        <Route element={<ShellLayout />}>
          {/* 列表与表单刻意拆成不同路由：列表只读、表单独立成页，避免一个页面两副面孔。
              表单页另加目录写权限守卫：列表页只是不渲染入口，直接输 URL 仍能进来。 */}
          <Route path="/directory/people" element={<PeoplePage />} />
          <Route
            path="/directory/people/new"
            element={
              <PermissionGate permission="directory.write">
                <PersonFormPage />
              </PermissionGate>
            }
          />
          <Route
            path="/directory/people/:id/edit"
            element={
              <PermissionGate permission="directory.write">
                <PersonFormPage />
              </PermissionGate>
            }
          />
          <Route path="/directory/teams" element={<TeamsPage />} />
          <Route
            path="/directory/teams/new"
            element={
              <PermissionGate permission="directory.write">
                <TeamFormPage />
              </PermissionGate>
            }
          />
          <Route
            path="/directory/teams/:id/edit"
            element={
              <PermissionGate permission="directory.write">
                <TeamFormPage />
              </PermissionGate>
            }
          />
          <Route path="/directory/projects" element={<ProjectsPage />} />
          <Route path="/directory/projects/new" element={<ProjectFormPage />} />
          <Route path="/directory/projects/:key/edit" element={<ProjectFormPage />} />
          {/* 管理页面统一按权限点守卫：普通账号凭角色配置进入，平台管理员固有对应权限。
              能看哪些数据由服务端按 scope 收敛，前端守卫只挡"没配权限就别进门"。 */}
          <Route
            path="/directory/roles"
            element={
              <PermissionGate permission="role.read">
                <RolesPage />
              </PermissionGate>
            }
          />
          <Route
            path="/directory/roles/new"
            element={
              <PermissionGate permission="role.write">
                <RoleFormPage />
              </PermissionGate>
            }
          />
          <Route
            path="/directory/roles/:id/edit"
            element={
              <PermissionGate permission="role.write">
                <RoleFormPage />
              </PermissionGate>
            }
          />
          <Route
            path="/settings"
            element={
              <PermissionGate permission="settings.read">
                <SettingsPage />
              </PermissionGate>
            }
          />
          {/* 账号列表：account.read（角色可配）。
              表单页：平台管理员专属——写账号是治理动作，不走角色配置。
              列表里的按钮再由页面按层级隐藏，这里的路由拦截只是防手输 URL。 */}
          <Route
            path="/admin/accounts"
            element={
              <PermissionGate permission="account.read">
                <AccountsPage />
              </PermissionGate>
            }
          />
          <Route
            path="/admin/accounts/new"
            element={
              <LevelGate minLevel="platform_admin">
                <AccountFormPage />
              </LevelGate>
            }
          />
          {/* 新建与编辑复用同一个页面组件：字段几乎相同，拆两份只会让两处慢慢走偏 */}
          <Route
            path="/admin/accounts/:id/edit"
            element={
              <LevelGate minLevel="platform_admin">
                <AccountFormPage />
              </LevelGate>
            }
          />
          <Route
            path="/admin/audit-logs"
            element={
              <PermissionGate permission="audit.read">
                <AuditLogsPage />
              </PermissionGate>
            }
          />
          {/* 公司管理：平台最高权限动作，连查看也只给超管（后端 RequireSuperAdmin）。
              新建后会在原地展示平台管理员的一次性初始密码，所以新建是独立路由。 */}
          <Route
            path="/admin/tenants"
            element={
              <AdminGate>
                <TenantsPage />
              </AdminGate>
            }
          />
          <Route
            path="/admin/tenants/new"
            element={
              <AdminGate>
                <TenantFormPage />
              </AdminGate>
            }
          />
        </Route>
      </Route>

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
