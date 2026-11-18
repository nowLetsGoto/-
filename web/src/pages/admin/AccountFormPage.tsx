/**
 * 账号新建 / 编辑页（/admin/accounts/new 与 /admin/accounts/:id/edit）。
 * 职责：新建时填账号名、初始密码、绑定人员与超管标记；编辑时改绑定人员、角色与超管标记。
 * 输入：URL 参数 id（有则编辑）、人员全量选项、角色全量选项、当前会话模式。
 * 输出：createUser / updateUser 请求。
 * 边界：不做字段校验（服务端统一判，返回可读提示）；模式在两个页面都只读展示——
 *       它由登录时选定的模式决定，令牌里带着它，页面上让用户切换只会建出自己看不见的账号。
 */
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import type { UserInput } from '../../api/users';
import { createUser, getUser, updateUser } from '../../api/users';
import type { Role } from '../../api/roles';
import { listRoles } from '../../api/roles';
import { errorMessage } from '../../api/client';
import { readSession } from '../../api/session';
import { Checkbox, Input } from '../../components/Controls';
import { FormPage } from '../../components/FormPage';
import { Select } from '../../components/Select';
import { ErrorState, Loading } from '../../components/States';
import { useAllPeople } from '../../hooks/useDirectory';
import { MODE_LABELS } from '../../utils/format';

// 新建表单的初值：角色留空（角色挂在人员身上，没绑人员就没有角色可选）；
// 管理身份默认 member——层级提升属于 P5 的前端能力，当前后端按调用者层级校验，
// 传 member 总是安全的（不会因为表单缺字段而意外造出高权限账号）。
const EMPTY: UserInput = {
  username: '',
  password: '',
  personId: '',
  isSuperAdmin: false,
  accountLevel: 'member',
  teamIds: [],
};

const LIST_PATH = '/admin/accounts';

export function AccountFormPage(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  const editing = Boolean(id);
  const navigate = useNavigate();

  const {
    items: people,
    loading: peopleLoading,
    error: peopleError,
    reload: reloadPeople,
  } = useAllPeople();
  const [roles, setRoles] = useState<Role[]>([]);
  const [form, setForm] = useState<UserInput>(EMPTY);
  const [roleKey, setRoleKey] = useState('');
  const [loading, setLoading] = useState(editing);
  const [loadError, setLoadError] = useState('');
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');

  // 模式由登录决定，页面只展示。读本地会话而不是再发一个请求：
  // 它就是令牌里那一段，服务端算账号归属时用的也是它。
  const mode = readSession()?.mode ?? 'solo';

  useEffect(() => {
    let cancelled = false;
    listRoles()
      .then((result) => {
        if (!cancelled) {
          setRoles(result.items);
        }
      })
      .catch(() => {
        // 角色取不到不该挡住编辑：下拉留空，保存时服务端仍会校验 roleKey。
        if (!cancelled) {
          setRoles([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!id) {
      setForm(EMPTY);
      setRoleKey('');
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    getUser(id)
      .then((user) => {
        if (cancelled) {
          return;
        }
        setForm({
          username: user.username,
          // 密码不可回显：编辑密码走「重置密码」，这里留空且不提交。
          password: '',
          personId: user.personId,
          isSuperAdmin: user.isSuperAdmin,
          accountLevel: user.accountLevel,
          teamIds: user.teamIds,
        });
        setRoleKey(user.roleKey);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setLoadError(errorMessage(err));
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  const submit = async (): Promise<void> => {
    setSaving(true);
    setNotice('');
    try {
      if (id) {
        await updateUser(id, {
          personId: form.personId,
          roleKey: form.personId ? roleKey : '',
          isSuperAdmin: form.isSuperAdmin,
        });
      } else {
        await createUser({
          username: form.username.trim(),
          password: form.password,
          personId: form.personId,
          isSuperAdmin: form.isSuperAdmin,
          accountLevel: form.accountLevel,
          teamIds: form.teamIds,
        });
      }
      navigate(LIST_PATH, { replace: true });
    } catch (err) {
      setNotice(errorMessage(err));
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <section className="page">
        <Loading text="正在加载账号…" />
      </section>
    );
  }
  if (loadError) {
    return (
      <section className="page">
        <div className="page-head">
          <div>
            <h1>编辑账号</h1>
            <p className="sub">加载失败</p>
          </div>
        </div>
        <ErrorState message={loadError} onRetry={() => navigate(LIST_PATH)} />
      </section>
    );
  }
  if (peopleLoading || peopleError) {
    return (
      <section className="page">
        {peopleLoading ? <Loading text="正在加载人员选项…" /> : null}
        {peopleError ? <ErrorState message={peopleError} onRetry={reloadPeople} /> : null}
      </section>
    );
  }

  return (
    <FormPage
      title={editing ? '编辑账号' : '新建账号'}
      subtitle={
        editing
          ? '修改绑定人员、角色与超管身份；账号名与模式不可改'
          : '创建后把账号与初始密码转交对方；建议对方首次登录后改密码'
      }
      backTo={LIST_PATH}
      saving={saving}
      notice={notice}
      onSubmit={() => void submit()}
    >
      <div className="field-grid">
        <label className="field">
          <span>账号名 *</span>
          <Input
            className="text-input"
            maxLength={32}
            placeholder="字母、数字、点、下划线、短横线，3-32 位"
            value={form.username}
            // 账号名会进令牌载荷与审计记录，改名等于换一个身份——创建后不再开放修改。
            disabled={editing}
            onChange={(event) => setForm({ ...form, username: event.target.value })}
          />
        </label>
        {editing ? null : (
          <label className="field">
            <span>初始密码 *</span>
            <Input
              className="text-input"
              type="text"
              maxLength={64}
              placeholder="8-64 位，创建后请立即转交"
              value={form.password}
              onChange={(event) => setForm({ ...form, password: event.target.value })}
            />
          </label>
        )}
      </div>

      <div className="field-grid">
        <label className="field">
          <span>归属模式</span>
          {/* 只读展示，不是下拉：模式随登录固定，令牌里带着它，
              这里换成另一个值只会得到一个自己列表里看不见的账号。 */}
          <div className="readonly-value">
            <span className={`mode-tag mode-${mode}`}>{MODE_LABELS[mode]}</span>
          </div>
          <span className="settings-hint">
            跟随登录时选择的模式，创建后不可修改；账号列表只显示与当前登录模式相同的账号。
          </span>
        </label>
        <label className="field">
          <span>绑定人员</span>
          <Select
            value={form.personId}
            options={[
              { value: '', label: '未绑定' },
              ...people.map((person) => ({ value: person.id, label: person.name })),
            ]}
            onChange={(personId) => setForm({ ...form, personId })}
          />
          <span className="settings-hint">绑定人员后，该账号以这个人的身份与角色工作。</span>
        </label>
      </div>

      {/* 角色挂在人员身上：没绑人员就没有可写角色的人，此时禁用并说明原因，
          而不是让用户选完再被服务端默默忽略。 */}
      <div className="field">
        <span>角色</span>
        <Select
          value={roleKey}
          disabled={!form.personId}
          options={[
            { value: '', label: '未分配（普通用户）' },
            ...roles.map((role) => ({ value: role.key, label: role.name })),
          ]}
          onChange={setRoleKey}
        />
        <span className="settings-hint">
          {form.personId
            ? '角色挂在绑定的人员身上：这里改完，人员管理里看到的是同一个结果。'
            : '先在上方绑定一个人员，才能为它分配角色。'}
        </span>
      </div>

      <Checkbox
        className="field-check field-check-admin"
        checked={form.isSuperAdmin}
        onChange={(event) => setForm({ ...form, isSuperAdmin: event.target.checked })}
      >
        设为超级管理员
        <span className="settings-hint">
          可进入「超级管理员」专区：管理账号与平台配置。请谨慎授予。
        </span>
      </Checkbox>
    </FormPage>
  );
}
