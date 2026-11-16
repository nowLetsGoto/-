/**
 * 顶栏：面包屑 + 全局环境切换 + 全局主行动。
 * 职责：显示「板块 / 环境」，切换环境（写进 URL），提供「上传包」入口。
 * 输入：当前路径与环境；输出：顶栏 JSX。
 * 边界：不发请求、不做页面内的筛选（平台等局部筛选留在页面里）；
 *       环境对所有板块成立，所以放在这里而不是页面里。
 */
import { Link, useLocation } from 'react-router-dom';
import { useEnvironment } from '../hooks/useEnvironment';
import { ENVIRONMENTS, environmentLabel } from '../utils/format';
import { Icon } from './icons';
import { boardLabel } from './navigation';

export function Topbar(): JSX.Element {
  const { pathname } = useLocation();
  const [environment, setEnvironment] = useEnvironment();

  return (
    <header className="topbar">
      <div className="crumbs">
        <span>{boardLabel(pathname)}</span>
        <span className="crumb-sep" aria-hidden="true">
          /
        </span>
        <b>{environmentLabel(environment)}</b>
      </div>

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

      <div className="topbar-actions">
        <Link className="button button-primary" to="/upload">
          <Icon name="plus" />
          上传包
        </Link>
      </div>
    </header>
  );
}
