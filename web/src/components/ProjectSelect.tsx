/**
 * 页面级项目筛选器（应用包 / 应用 / 概览 的头部）。
 * 职责：展示当前项目并切换；切换即改写 URL 的 /p/:key 段，其余路径与查询串保持不变。
 * 输入：projects（由页面传入，避免组件自己取数）、currentKey；输出：一个下拉。
 * 边界：不取数、不写 localStorage（"最近访问"由 useCurrentProject 负责记忆）。
 */
import { useLocation, useNavigate } from 'react-router-dom';
import type { Project } from '../api/projects';
import { projectRelativePath } from './navigation';

interface ProjectSelectProps {
  projects: ReadonlyArray<Project>;
  currentKey: string;
}

export function ProjectSelect({ projects, currentKey }: ProjectSelectProps): JSX.Element {
  const navigate = useNavigate();
  const { pathname, search } = useLocation();

  // 切项目保留当前板块与环境查询串，不把人踢回首页。
  const switchTo = (key: string): void => {
    navigate(`/p/${key}${projectRelativePath(pathname)}${search}`);
  };

  return (
    <label className="project-filter">
      <span>项目</span>
      <select className="select-input" value={currentKey} onChange={(event) => switchTo(event.target.value)}>
        {projects.map((project) => (
          <option key={project.key} value={project.key}>
            {project.name}
          </option>
        ))}
      </select>
    </label>
  );
}
