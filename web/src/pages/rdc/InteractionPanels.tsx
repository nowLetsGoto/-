/**
 * AI 交互层的计划确认 / 检查点时间线 / 分级审批徽标三个纯 UI 组件（挂 03 审核详情抽屉）。
 * 职责：按真实 API 数据显示三类面板；门禁未满足时禁用提交，危险回滚二次确认并写清影响面。
 * 输入：taskId / subtaskId / 子任务类型、通知回调；数据来自 rdcInteraction API。
 * 输出：交互面板与确认弹窗；无产物时只显示真实空态，不渲染假按钮。
 * 边界：只发证据与控制请求，不写真实文件、不改子任务状态；审批强度是业务规则不是权限点。
 */
import { useEffect, useState } from 'react';
import { ApiError, errorMessage } from '../../api/client';
import type {
  ApprovalView,
  Checkpoint,
  Decision,
  PlanConfirmation,
  Question,
} from '../../api/rdcInteraction';
import {
  confirmPlan,
  decideApproval,
  getApproval,
  getPlanConfirmation,
  listCheckpoints,
  rollbackCheckpoint,
} from '../../api/rdcInteraction';
import { Button, Checkbox, Input, Segmented } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { Empty, ErrorState, Loading } from '../../components/States';
import {
  CHECKPOINT_STATUS_LABELS,
  interventionLabel,
  reviewActionLabel,
  riskLabel,
} from '../../utils/format';

interface PanelProps {
  taskId: string;
  subtaskId: string;
  kind: string;
  onNotice: (message: string) => void;
}

interface LoadState<T> {
  loading: boolean;
  error: string;
  data: T | null;
}

const INITIAL: LoadState<unknown> = { loading: false, error: '', data: null };

/** 判断一道题是否已有答案：选项 id / id 数组 / 文本三类形态。 */
function hasAnswer(answer: unknown): boolean {
  if (answer === undefined || answer === null) {
    return false;
  }
  if (Array.isArray(answer)) {
    return answer.length > 0;
  }
  if (typeof answer === 'string') {
    return answer.trim() !== '';
  }
  return true;
}

/** 把题目答案数组按 questionId 建索引，便于渲染时回显。 */
function answersOf(
  questions: Question[],
  loaded: PlanConfirmation | null,
): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  if (loaded) {
    for (const question of loaded.questionnaire) {
      result[question.questionId] = question.answer;
    }
  }
  for (const question of questions) {
    if (question.answer !== undefined) {
      result[question.questionId] = question.answer;
    }
  }
  return result;
}

/**
 * 分级审批徽标：整体风险来自全部 hunk 的最大值，低→免审 / 中→抽查 / 高→强审批。
 * 只有抽查与强审批才出现审批入口；审批只写证据，不产生新状态边。
 */
export function ApprovalBadge({ taskId, subtaskId, kind, onNotice }: PanelProps): JSX.Element {
  const [state, setState] = useState<LoadState<ApprovalView>>(INITIAL as LoadState<ApprovalView>);
  const [checkpointId, setCheckpointId] = useState('');
  const [busy, setBusy] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState({ loading: true, error: '', data: null });
    Promise.all([getApproval(taskId, subtaskId), listCheckpoints(taskId, subtaskId)])
      .then(([view, checkpoints]) => {
        if (!cancelled) {
          setState({ loading: false, error: '', data: view });
          setCheckpointId(checkpoints[0]?.id ?? '');
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ loading: false, error: errorMessage(err), data: null });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [taskId, subtaskId, reloadKey]);

  const decide = async (decision: 'approve' | 'reject'): Promise<void> => {
    setBusy(true);
    onNotice('');
    try {
      await decideApproval(taskId, subtaskId, { checkpointId, decision });
      onNotice('审批结论已记录（证据与审计），未改变子任务状态。');
      setReloadKey((value) => value + 1);
    } catch (err) {
      onNotice(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const view = state.data;
  const body = (): JSX.Element => {
    if (state.loading) {
      return <Loading text="正在读取审批徽标…" />;
    }
    if (state.error) {
      return <ErrorState message={state.error} onRetry={() => setReloadKey((v) => v + 1)} />;
    }
    if (!view) {
      return (
        <Empty
          title="等待 Agent 执行器就绪"
          description="暂无真实风险数据，因此不显示审批结论入口。"
        />
      );
    }
    return (
      <div className="rdc-approval-badges">
        <span className={`badge rdc-risk-badge rdc-risk-${view.overallRisk}`}>
          {riskLabel(view.overallRisk)}
        </span>
        <span className={`badge rdc-intervention-badge rdc-intervention-${view.intervention}`}>
          {interventionLabel(view.intervention)}
        </span>
        <span className="cell-sub">
          {devKindHint(kind)} ·{' '}
          {view.decided ? `已记录：${reviewActionLabel(view.decision)}` : '尚未记录审批结论'}
        </span>
        {!view.decided && view.intervention !== 'exempt' && checkpointId ? (
          <span className="rdc-approval-actions">
            <Button
              type="button"
              className="button button-primary"
              disabled={busy}
              onClick={() => void decide('approve')}
            >
              记录通过
            </Button>
            <Button
              type="button"
              className="button button-ghost"
              disabled={busy}
              onClick={() => void decide('reject')}
            >
              记录驳回
            </Button>
          </span>
        ) : null}
      </div>
    );
  };
  return (
    <section className="rdc-interaction-block">
      <h4>分级审批</h4>
      {body()}
    </section>
  );
}

/** devKindHint 只用于徽标旁的类型提示，不改变任何状态语义。 */
function devKindHint(kind: string): string {
  return kind === 'backend' ? '后端子任务' : '前端子任务';
}

/**
 * 计划确认面板：生成前的结构化问卷 + 可确认任务列表；
 * 必答题未答或任务未确认时"确认计划"禁用并列出缺项。
 */
export function PlanConfirmationPanel({ taskId, subtaskId, onNotice }: PanelProps): JSX.Element {
  const [state, setState] = useState<LoadState<PlanConfirmation>>(
    INITIAL as LoadState<PlanConfirmation>,
  );
  const [answers, setAnswers] = useState<Record<string, unknown>>({});
  const [stepIds, setStepIds] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState({ loading: true, error: '', data: null });
    getPlanConfirmation(taskId, subtaskId)
      .then((plan) => {
        if (!cancelled) {
          setState({ loading: false, error: '', data: plan });
          setAnswers(answersOf(plan?.questionnaire ?? [], plan));
          setStepIds(
            plan?.planTasks.filter((task) => task.confirmed).map((task) => task.stepId) ?? [],
          );
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ loading: false, error: errorMessage(err), data: null });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [taskId, subtaskId, reloadKey]);

  const plan = state.data;
  const missing: string[] = [];
  for (const question of plan?.questionnaire ?? []) {
    if (question.required && !hasAnswer(answers[question.questionId])) {
      missing.push(question.prompt);
    }
  }
  for (const task of plan?.planTasks ?? []) {
    if (!stepIds.includes(task.stepId) && !task.confirmed) {
      missing.push(task.title);
    }
  }

  const confirm = async (): Promise<void> => {
    if (!plan || busy || missing.length > 0) {
      return;
    }
    setBusy(true);
    onNotice('');
    try {
      const payload = plan.questionnaire.map((question) => ({
        ...question,
        answer: answers[question.questionId],
      }));
      await confirmPlan(plan.id, { answers: payload, stepIds });
      onNotice('计划已确认（意图对齐证据），未推进子任务状态、未触发生成。');
      setReloadKey((value) => value + 1);
    } catch (err) {
      onNotice(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const body = (): JSX.Element => {
    if (state.loading) {
      return <Loading text="正在读取计划确认…" />;
    }
    if (state.error) {
      return <ErrorState message={state.error} onRetry={() => setReloadKey((v) => v + 1)} />;
    }
    if (!plan) {
      return <Empty title="等待 Agent 执行器就绪" description="暂无真实计划问卷与任务列表。" />;
    }
    return (
      <div className="rdc-plan-body">
        {plan.questionnaire.length === 0 && plan.planTasks.length === 0 ? (
          <p className="cell-sub">本次动作没有需要确认的问题或步骤。</p>
        ) : null}
        {plan.questionnaire.map((question) => (
          <div className="rdc-plan-question" key={question.questionId}>
            <p>
              {question.prompt}
              {question.required ? <span className="rdc-required">（必答）</span> : null}
            </p>
            {question.type === 'text' ? (
              <Input
                value={String(answers[question.questionId] ?? '')}
                onChange={(event) =>
                  setAnswers((prev) => ({ ...prev, [question.questionId]: event.target.value }))
                }
                placeholder="请填写"
              />
            ) : (
              <div className="rdc-plan-options">
                {question.options.map((option) => {
                  const current = answers[question.questionId];
                  const checked =
                    question.type === 'multiple'
                      ? Array.isArray(current) && current.includes(option.optionId)
                      : current === option.optionId;
                  return (
                    <Checkbox
                      key={option.optionId}
                      checked={checked}
                      onChange={() => {
                        if (question.type === 'multiple') {
                          const values = Array.isArray(current) ? [...current] : [];
                          const next = checked
                            ? values.filter((value) => value !== option.optionId)
                            : [...values, option.optionId];
                          setAnswers((prev) => ({ ...prev, [question.questionId]: next }));
                        } else {
                          setAnswers((prev) => ({
                            ...prev,
                            [question.questionId]: option.optionId,
                          }));
                        }
                      }}
                    >
                      {option.label}
                    </Checkbox>
                  );
                })}
              </div>
            )}
          </div>
        ))}
        {plan.planTasks.map((task) => (
          <div className="rdc-plan-task" key={task.stepId}>
            <Checkbox
              checked={stepIds.includes(task.stepId) || task.confirmed}
              onChange={() =>
                setStepIds((prev) =>
                  prev.includes(task.stepId)
                    ? prev.filter((id) => id !== task.stepId)
                    : [...prev, task.stepId],
                )
              }
            >
              {task.title} · {riskLabel(task.estimatedRisk)}
            </Checkbox>
            <p className="cell-sub">{task.description}</p>
          </div>
        ))}
      </div>
    );
  };

  return (
    <section className="rdc-interaction-block">
      <h4>计划确认</h4>
      {body()}
      {plan ? (
        <div className="rdc-plan-footer">
          <span className="cell-sub">
            {missing.length > 0 ? `门禁未满足：${missing.join('、')}` : '门禁已满足'}
          </span>
          <Button
            type="button"
            className="button button-primary"
            disabled={busy || missing.length > 0 || plan.gateStatus === 'confirmed'}
            onClick={() => void confirm()}
          >
            {plan.gateStatus === 'confirmed' ? '计划已确认' : '确认计划'}
          </Button>
        </div>
      ) : null}
    </section>
  );
}

/**
 * 检查点时间线：按粒度回滚并校验哈希；二次确认写清影响面。
 * 本版只做证据层：不写真实文件、不回退子任务状态；哈希不匹配由后端 409 阻断。
 */
export function CheckpointTimeline({ taskId, subtaskId, onNotice }: PanelProps): JSX.Element {
  const [state, setState] = useState<LoadState<Checkpoint[]>>(INITIAL as LoadState<Checkpoint[]>);
  const [selected, setSelected] = useState<Checkpoint | null>(null);
  const [scope, setScope] = useState<'hunk' | 'file' | 'checkpoint'>('checkpoint');
  const [hashes, setHashes] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState({ loading: true, error: '', data: null });
    listCheckpoints(taskId, subtaskId)
      .then((items) => {
        if (!cancelled) {
          setState({ loading: false, error: '', data: items });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ loading: false, error: errorMessage(err), data: null });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [taskId, subtaskId, reloadKey]);

  const rollback = async (): Promise<void> => {
    if (!selected || busy) {
      return;
    }
    setBusy(true);
    onNotice('');
    try {
      await rollbackCheckpoint(selected.id, {
        rollbackToken: selected.rollbackToken,
        scope,
        targets:
          scope === 'file'
            ? selected.files.map((file) => ({
                filePath: file.filePath,
                hunkId: '',
              }))
            : [],
        currentHashes: hashes,
      });
      onNotice('回滚已记录（证据与检查点状态），未回退子任务状态、未写真实文件。');
      setSelected(null);
      setReloadKey((value) => value + 1);
    } catch (err) {
      const hint = err instanceof ApiError && err.code === 409 ? ' ' : '';
      onNotice(`${errorMessage(err)}${hint}请核对冲突文件，本系统不提供强制覆盖。`);
    } finally {
      setBusy(false);
    }
  };

  const body = (): JSX.Element => {
    if (state.loading) {
      return <Loading text="正在读取检查点时间线…" />;
    }
    if (state.error) {
      return <ErrorState message={state.error} onRetry={() => setReloadKey((v) => v + 1)} />;
    }
    if (!state.data || state.data.length === 0) {
      return (
        <Empty
          title="等待 Agent 执行器就绪"
          description="暂无真实检查点快照，因此不显示回滚入口。"
        />
      );
    }
    return (
      <ul className="rdc-checkpoint-list">
        {state.data.map((checkpoint) => (
          <li key={checkpoint.id}>
            <span className="badge">{CHECKPOINT_STATUS_LABELS[checkpoint.status]}</span>
            <span className="cell-sub">
              {checkpoint.aiAction} · {checkpoint.files.length} 个文件 · {checkpoint.createdAt}
            </span>
            {checkpoint.status === 'active' ? (
              <Button
                type="button"
                className="button button-ghost"
                onClick={() => {
                  setSelected(checkpoint);
                  setScope('checkpoint');
                  setHashes({});
                }}
              >
                回滚
              </Button>
            ) : null}
          </li>
        ))}
      </ul>
    );
  };

  return (
    <section className="rdc-interaction-block">
      <h4>检查点时间线</h4>
      {body()}
      <ConfirmDialog
        open={selected !== null}
        title="确认回滚该检查点？"
        description={
          <>
            <p>
              影响面：将记录一条回滚证据，并把检查点标记为已回滚 / 部分回滚。
              子任务状态不会回退，真实文件不会被本操作改写。
            </p>
            <Segmented<'hunk' | 'file' | 'checkpoint'>
              aria-label="回滚粒度"
              value={scope}
              options={[
                { value: 'checkpoint', label: '整个检查点' },
                { value: 'file', label: '按文件' },
                { value: 'hunk', label: '按 hunk' },
              ]}
              onChange={setScope}
            />
            {(selected?.files ?? []).map((file) => (
              <label className="rdc-hash-field" key={file.filePath}>
                <span>{file.filePath} 当前内容 hash</span>
                <Input
                  value={hashes[file.filePath] ?? ''}
                  onChange={(event) =>
                    setHashes((prev) => ({ ...prev, [file.filePath]: event.target.value }))
                  }
                  placeholder="由执行器读取当前工作区后填写"
                />
              </label>
            ))}
          </>
        }
        confirmText="确认回滚"
        danger
        loading={busy}
        onConfirm={() => void rollback()}
        onCancel={() => setSelected(null)}
      />
    </section>
  );
}

/** Decision 类型在此重导出，供面板内部与未来表单复用，避免各处手写字符串。 */
export type { Decision };
