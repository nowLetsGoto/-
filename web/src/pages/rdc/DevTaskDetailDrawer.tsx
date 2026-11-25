/**
 * 研发任务详情抽屉。
 * 职责：展示绑定需求信息与两张子任务卡片，提供手动推进 / 标记跳过 / 编辑 / 删除入口，
 *       并在底部列出 5 个后续能力的纯文字说明。
 * 输入：taskID（null 表示关闭）、抽屉关闭与数据变更回调。
 * 输出：DetailDrawer + 子任务卡片 + 两个二次确认对话框。
 * 边界：推进按钮文案是"推进状态到 …"，只改平台状态、不触发研发动作；
 *       SKIPPED 无恢复入口；删除与跳过都写清不可恢复的影响面；不渲染任何占位功能按钮。
 */
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { errorMessage } from '../../api/client';
import type { DevSubtask, DevTaskDetail } from '../../api/rdcDev';
import {
  advanceDevSubtask,
  deleteDevTask,
  getDevTask,
  resubmitDevSubtask,
  skipDevSubtask,
} from '../../api/rdcDev';
import { Button } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { DetailDrawer } from '../../components/DetailDrawer';
import { ErrorState, Loading } from '../../components/States';
import {
  DEV_NEXT_STATUS,
  canSkipDevStatus,
  devKindLabel,
  devStatusLabel,
  formatDate,
  projectLabel,
} from '../../utils/format';
import { DevPlaceholderFeatures } from './DevPlaceholderFeatures';

interface DevTaskDetailDrawerProps {
  taskID: string | null;
  onClose: () => void;
  onChanged: () => void;
}

interface DrawerState {
  loading: boolean;
  data: DevTaskDetail | null;
  error: string;
}

type PendingAction = { kind: 'skip'; subtask: DevSubtask } | { kind: 'delete' } | null;

const EMPTY: DrawerState = { loading: false, data: null, error: '' };

export function DevTaskDetailDrawer({
  taskID,
  onClose,
  onChanged,
}: DevTaskDetailDrawerProps): JSX.Element {
  const [state, setState] = useState<DrawerState>(EMPTY);
  const [reloadKey, setReloadKey] = useState(0);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState('');
  const [pending, setPending] = useState<PendingAction>(null);

  useEffect(() => {
    if (!taskID) {
      setState(EMPTY);
      setNotice('');
      setPending(null);
      return;
    }
    let cancelled = false;
    setState({ loading: true, data: null, error: '' });
    setNotice('');
    setPending(null);
    getDevTask(taskID)
      .then((data) => {
        if (!cancelled) {
          setState({ loading: false, data, error: '' });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ loading: false, data: null, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [taskID, reloadKey]);

  const reload = (): void => {
    setReloadKey((value) => value + 1);
    onChanged();
  };

  const runAdvance = async (subtask: DevSubtask): Promise<void> => {
    if (!taskID || busy) {
      return;
    }
    setBusy(true);
    setNotice('');
    try {
      await advanceDevSubtask(taskID, subtask.id);
      reload();
    } catch (err) {
      setNotice(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const runResubmit = async (subtask: DevSubtask): Promise<void> => {
    if (!taskID || busy) {
      return;
    }
    setBusy(true);
    setNotice('');
    try {
      await resubmitDevSubtask(taskID, subtask.id);
      reload();
    } catch (err) {
      setNotice(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const confirmAction = async (): Promise<void> => {
    if (!taskID || !pending || busy) {
      return;
    }
    setBusy(true);
    setNotice('');
    try {
      if (pending.kind === 'skip') {
        await skipDevSubtask(taskID, pending.subtask.id);
        setPending(null);
        reload();
      } else {
        await deleteDevTask(taskID);
        setPending(null);
        onClose();
        onChanged();
      }
    } catch (err) {
      setNotice(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const data = state.data;
  const subtasks = data?.subtasks ?? [];

  return (
    <DetailDrawer
      open={taskID !== null}
      title={data ? `研发任务 · ${data.title}` : '研发任务详情'}
      onClose={onClose}
      width={720}
    >
      {state.loading ? <Loading text="正在读取任务详情…" /> : null}
      {state.error ? <ErrorState message={state.error} /> : null}
      {data ? (
        <div className="rdc-dev-detail">
          <div className="rdc-dev-meta">
            <div>
              <span className="cell-sub">关联需求</span>
              <p>
                {data.reqKey} · {data.requirementTitle || '—'}
              </p>
            </div>
            <div>
              <span className="cell-sub">所属项目</span>
              <p>{projectLabel(data.projectName)}</p>
            </div>
            <div>
              <span className="cell-sub">整体状态</span>
              <p>
                <span className={`badge rdc-status rdc-dev-status-${data.overallStatus}`}>
                  {devStatusLabel(data.overallStatus)}
                </span>
              </p>
            </div>
            <div>
              <span className="cell-sub">创建人 / 时间</span>
              <p>
                {data.creatorName || '—'} · {formatDate(data.createdAt)}
              </p>
            </div>
          </div>

          {notice ? <p className="field-error">{notice}</p> : null}

          <div className="rdc-dev-actions">
            <Link className="button button-ghost" to={`/rdc/dev/${data.id}/edit`}>
              编辑资料
            </Link>
            <Button
              className="button button-danger"
              type="button"
              disabled={busy}
              onClick={() => setPending({ kind: 'delete' })}
            >
              删除任务
            </Button>
          </div>
          <p className="cell-sub rdc-dev-hint">
            编辑页只改任务名、绑定需求与子任务备注；状态请在这里用「推进状态到 …」按钮变更。
          </p>

          <div className="rdc-dev-subtask-grid">
            {subtasks.map((subtask) => {
              const next = DEV_NEXT_STATUS[subtask.status];
              return (
                <section className="rdc-dev-subtask-card" key={subtask.id}>
                  <header>
                    <strong>{devKindLabel(subtask.kind)}子任务</strong>
                    <span className={`badge rdc-status rdc-dev-status-${subtask.status}`}>
                      {devStatusLabel(subtask.status)}
                    </span>
                  </header>
                  <p className="cell-sub">最近更新：{formatDate(subtask.updatedAt)}</p>
                  <p className="rdc-dev-note">{subtask.note || '暂无备注'}</p>
                  <div className="rdc-dev-subtask-actions">
                    {next ? (
                      <Button
                        className="button button-primary"
                        type="button"
                        disabled={busy}
                        onClick={() => void runAdvance(subtask)}
                      >
                        推进状态到 {devStatusLabel(next)}
                      </Button>
                    ) : null}
                    {canSkipDevStatus(subtask.status) ? (
                      <Button
                        className="button button-ghost"
                        type="button"
                        disabled={busy}
                        onClick={() => setPending({ kind: 'skip', subtask })}
                      >
                        标为跳过
                      </Button>
                    ) : null}
                    {subtask.status === 'rejected' ? (
                      <Button
                        className="button button-primary"
                        type="button"
                        disabled={busy}
                        onClick={() => void runResubmit(subtask)}
                      >
                        重新研发（回到 AI 已完成）
                      </Button>
                    ) : null}
                  </div>
                  {subtask.status === 'skipped' ? (
                    <p className="cell-sub">已跳过，永久不可恢复，也不计入整体状态。</p>
                  ) : null}
                  {subtask.status === 'rejected' ? (
                    <p className="cell-sub">
                      审核已驳回，重新研发后将回到「AI 已完成」等待再次提交审核。
                    </p>
                  ) : null}
                </section>
              );
            })}
          </div>

          <DevPlaceholderFeatures />
        </div>
      ) : null}

      <ConfirmDialog
        open={pending?.kind === 'skip'}
        title="确认标记为已跳过？"
        description={
          pending?.kind === 'skip' ? (
            <>
              将把「{devKindLabel(pending.subtask.kind)}子任务」标记为已跳过。
              <strong>该操作永久不可恢复</strong>：不能回退状态，该子任务也不再计入整体状态。
            </>
          ) : null
        }
        confirmText="确认跳过"
        danger
        loading={busy}
        onConfirm={() => void confirmAction()}
        onCancel={() => setPending(null)}
      />
      <ConfirmDialog
        open={pending?.kind === 'delete'}
        title="确认删除研发任务？"
        description={
          <>
            任务与前端 / 后端两条子任务将<strong>一并删除且不可恢复</strong>
            ，状态历史与备注也会消失。 如果只是不再开发某个子任务，请改用「标为跳过」。
          </>
        }
        confirmText="确认删除"
        danger
        loading={busy}
        onConfirm={() => void confirmAction()}
        onCancel={() => setPending(null)}
      />
    </DetailDrawer>
  );
}
