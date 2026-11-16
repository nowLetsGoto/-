/**
 * 顶栏：面包屑 + 全局环境切换 + 板块主行动。
 * 职责：显示「板块 / 环境」，切换环境（写进 URL），在需要的板块上提供「上传包」入口。
 * 输入：当前路径与环境；输出：顶栏 JSX。
 * 边界：不发请求、不做页面内的筛选（平台等局部筛选留在页面里）；
 *       环境对所有业务板块成立，所以放在这里；「上传包」只在应用包板块出现，由导航配置决定。
 */
import { Link, useLocation } from 'react-router-dom';
import { useEnvironment } from '../hooks/useEnvironment';
import { ENVIRONMENTS, environmentLabel } from '../utils/format';
import { Icon } from './icons';
import { boardLabel, canUpload, isEnvScoped } from './navigation';

export function Topbar(): JSX.Element {
  const { pathname } = useLocation();
  const [environment, setEnvironment] = useEnvironment();
  // 两块都来自导航配置：系统设置这类全局板块不按环境区分，也不该出现上传入口。
  const envScoped = isEnvScoped(pathname);
  const showUpload = canUpload(pathname);

  return (
    <header className="topbar">
      <div className="crumbs">
        <span>{boardLabel(pathname)}</span>
        {envScoped ? (
          <>
            <span className="crumb-sep" aria-hidden="true">
              /
            </span>
            <b>{environmentLabel(environment)}</b>
          </>
        ) : null}
      </div>

      {envScoped ? (
        <div className="env-switch" role="group" aria-label="环境">
          {ENVIRONMENTS.map((item) => (
            <button
              key={item.value}
              type="button"
              className={item.value === environment ? 'env-option env-option-active' : 'env-option'}
              aria-pressed={item.value === environment}
              onClick={() => setEnvironment(item.value)}
            >
              {item.label}
            </button>
          ))}
        </div>
      ) : null}

      {showUpload ? (
        <div className="topbar-actions">
          <Link className="button button-primary" to="/upload">
            <Icon name="plus" />
            上传包
          </Link>
        </div>
      ) : null}
    </header>
  );
}
