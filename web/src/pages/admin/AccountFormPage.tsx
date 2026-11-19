/**
 * 账号新建 / 编辑页（/admin/accounts/new 与 /admin/accounts/:id/edit）。
 * 职责：新建时填账号名、初始密码、绑定人员、角色与超管标记；编辑时改绑定人员、角色与超管标记。
 * 下拉选项按调用者权限裁剪：可分配的角色 = 权限点不超出调用者自身的角色，
 * 因此平台管理员既看不到 super-admin，也绑不了带该角色的人员。
 * 输入：URL 参数 id（有则编辑）、人员全量选项、角色全量选项、当前会话模式。
 * 输出：createUser / updateUser 请求。
 * 边界：不做字段校验（服务端统一判，返回可读提示）；模式在两个页面都只读展示——
 *       它由登录时选定的模式决定，令牌里带着它，页面上让用户切换只会建出自己看不见的账号。
 *
 * 关于「设为超级管理员」：只对超管本人渲染。服务端在 canGrantSuperAdmin 里守死了这条
 * （非超管传 isSuperAdmin=true 直接 403），但界面上不该给平台管理员一个注定失败的开关——
 * 那会让人误以为自己真有这个权力，点完才发现被拒。前端隐藏 + 后端拒绝，两层各自负责。
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
import { useMe } from '../../hooks/useMe';
import { MODE_LABELS } from '../../utils/format';

// 新建表单的初值：角色留空（角色挂在人员身上，没绑人员就没有角色可选）；
// 管理身份默认 member——层级提升属于 P5 的前端能力，当前后端按调用者层级校验，
// 传 member 总是安全的（不会因为表单缺字段而意外造出高权限账号）。
const EMPTY: UserInput = {
  username: '',
  password: '',
  personId: '',
  roleKey: '',
  isSuperAdmin: false,
  accountLevel: 'member',
  teamIds: [],
};

const LIST_PATH = '/admin/accounts';

export function AccountFormPage(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  const editing = Boolean(id);
  const navigate = useNavigate();

  const { me } = useMe();
  // 只有超管本人能授予超管标记；判据与服务端 canGrantSuperAdmin 一致。
  const canGrantSuperAdmin = me?.isSuperAdmin === true;
  const myPermissions = me?.permissions ?? [];

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

  /**
   * 可分配角色 = 权限点不超出调用者自身的角色。
   *
   * 这与服务端 authz.ValidateRoleAssignable 是同一条判据，所以界面上能选到的角色
   * 一定分配得动，选不到的一定会被 403——两层不会各说一套。
   * 不需要额外排除"超管角色"：角色体系里没有它（超管是账号身份，见 api/roles.ts 的说明）。
   */
  const assignableRoles = canGrantSuperAdmin
    ? roles
    : roles.filter((role) =>
        role.permissions.every((permission) => myPermissions.includes(permission)),
      );
  const assignableRoleKeys = new Set(assignableRoles.map((role) => role.key));

  /**
   * 绑定人员选项。三条限制，出发点都是同一个事实——**账号的权限就是绑定人员的权限**：
   *
   *   1. 非超管不给「未绑定」，且提交时强制必填（见 submit 里的校验）。
   *      没有人员的账号没有角色也没有团队，登录后什么数据都看不到，
   *      建出来只是一条"看着正常、实际废掉"的记录。
   *      服务端 service/user.ensurePersonAssignable 是同一条规则，两层不会各说一套。
   *   2. 人员当前角色超出调用者范围的不列出来：绑一个权限更高的人，
   *      账号登录后继承的就是那份权限，和直接把角色分配出去等价。
   *   3. 团队负责人只会看到本团队的人——people.List 本身带团队条件，
   *      不需要在这里额外过滤。服务端还会再校验一次「这个人是否归你管」。
   */
  const personOptions = [
    ...(canGrantSuperAdmin ? [{ value: '', label: '未绑定' }] : []),
    ...people
      .filter((person) => !person.roleKey || assignableRoleKeys.has(person.roleKey))
      .map((person) => ({ value: person.id, label: person.name })),
  ];

  // 角色选项：非超管没有「未分配」——未分配等于没角色，等于这个账号建了也没用。
  const roleOptions = [
    ...(canGrantSuperAdmin ? [{ value: '', label: '未分配（普通用户）' }] : []),
    ...assignableRoles.map((role) => ({ value: role.key, label: role.name })),
  ];

  /**
   * 换绑定人员时把角色同步成这个人的现有角色。
   * 角色挂在人身上，下拉里应当显示"这个账号实际会拿到的角色"，
   * 而不是恒为「未分配」——那会让人以为保存后角色被清空了。
   */
  const changePerson = (personId: string): void => {
    const person = people.find((item) => item.id === personId);
    const nextRole = person?.roleKey ?? '';
    setForm({ ...form, personId });
    setRoleKey(nextRole === '' || assignableRoleKeys.has(nextRole) ? nextRole : '');
  };

  const submit = async (): Promise<void> => {
    // 非超管必须绑定人员：角色与权限都从人员来，没有人员的账号登录后什么都做不了。
    // 服务端也拦（personId 为空的高权限角色会被拒），这里先给一句人话。
    if (!canGrantSuperAdmin && !form.personId) {
      setNotice('请先选择要绑定的人员：账号的角色与权限都来自绑定人员');
      return;
    }
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
          roleKey: form.personId ? roleKey : '',
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
            options={personOptions}
            placeholder={canGrantSuperAdmin ? undefined : '请选择人员'}
            onChange={changePerson}
          />
          <span className="settings-hint">
            {canGrantSuperAdmin
              ? '绑定人员后，该账号以这个人的身份与角色工作。'
              : personOptions.length > 0
                ? '账号以这个人的身份与角色工作；角色权限高于你的人员不在可选范围内。'
                : '还没有可绑定的人员：先到「人员管理」新建人员，再回来建账号。'}
          </span>
        </label>
      </div>

      {/* 角色挂在人员身上：没绑人员就没有可写角色的人，此时禁用并说明原因，
          而不是让用户选完再被服务端默默忽略。 */}
      <div className="field">
        <span>角色</span>
        <Select
          value={roleKey}
          disabled={!form.personId}
          options={roleOptions}
          placeholder={canGrantSuperAdmin ? undefined : '请选择角色'}
          onChange={setRoleKey}
        />
        <span className="settings-hint">
          {form.personId
            ? '角色挂在绑定的人员身上：这里改完，人员管理里看到的是同一个结果。'
            : '先在上方绑定一个人员，才能为它分配角色。'}
        </span>
      </div>

      {/*
        仅超管可见。编辑一个「已是超管」的账号时也要显示（哪怕编辑者不是超管），
        否则超管把自己降级后再也看不到这个开关——那种场景下它更需要被看见。
        实际上非超管编辑超管账号本身会被服务端拒绝，这里只需保证不误导。
      */}
      {canGrantSuperAdmin ? (
        <Checkbox
          className="field-check field-check-admin"
          checked={form.isSuperAdmin}
          onChange={(event) => setForm({ ...form, isSuperAdmin: event.target.checked })}
        >
          设为超级管理员
          <span className="settings-hint">
            可进入「超级管理员」专区：管理公司、账号与平台配置。此标记高于公司管理层级，
            只有超级管理员本人可以授予或撤销。
          </span>
        </Checkbox>
      ) : null}
    </FormPage>
  );
}
