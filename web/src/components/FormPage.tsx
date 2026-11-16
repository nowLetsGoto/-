/**
 * 新增 / 编辑页的公共外壳。
 * 职责：统一页头（标题 + 返回列表）、表单卡片、保存 / 取消按钮与错误提示。
 * 输入：标题、返回地址、提交回调、字段 children、保存态与提示；输出：整页表单。
 * 边界：不取数、不校验、不认识任何业务字段——字段由调用方以 children 传入。
 */
import type { FormEvent, ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Button } from './Controls';

interface FormPageProps {
  title: string;
  subtitle: string;
  /** 保存成功或点取消后回到的列表页 */
  backTo: string;
  saving: boolean;
  notice: string;
  onSubmit: () => void;
  /** 只读模式：只展示字段，不给保存按钮（例如查看内置角色的权限） */
  readOnly?: boolean;
  children: ReactNode;
}

export function FormPage({
  title,
  subtitle,
  backTo,
  saving,
  notice,
  onSubmit,
  readOnly = false,
  children,
}: FormPageProps): JSX.Element {
  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    if (!saving) {
      onSubmit();
    }
  };

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>{title}</h1>
          <p className="sub">{subtitle}</p>
        </div>
        <Link className="button button-ghost" to={backTo}>
          返回列表
        </Link>
      </div>

      <form className="card settings-card" onSubmit={handleSubmit}>
        {children}

        {notice ? <p className="field-error">{notice}</p> : null}

        <div className="settings-actions">
          {readOnly ? null : (
            <Button className="button button-primary" type="submit" disabled={saving}>
              {saving ? '保存中…' : '保存'}
            </Button>
          )}
          <Link className="button button-ghost" to={backTo}>
            {readOnly ? '返回列表' : '取消'}
          </Link>
        </div>
      </form>
    </section>
  );
}
