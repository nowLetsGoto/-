/**
 * 审核详情抽屉。
 * 职责：展示任务/子任务状态与审核历史，提供审核通过、驳回、提交确认三个操作，
 *       并按子任务挂载四类 AI 交互面板（审批徽标 / 计划确认 / Diff 审查 / 检查点时间线）。
 * 输入：taskID（null 表示关闭）、抽屉关闭与数据变更回调。
 * 输出：DetailDrawer + 子任务审核卡片 + 四类交互面板 + 审核历史 + 两个输入对话框。
 * 边界：只做平台编排，不触发真实 Git / 编辑器动作；驳回原因与 commit SHA 必填；
 *       四类面板拿到真实空态时不渲染可点控件；交互面板只写证据，不改子任务状态。
 */
import { useEffect, useState } from 'react';
import { errorMessage } from '../../api/client';
import type { DevSubtask, ReviewDetail } from '../../api/rdcReview';
import { approveSubtask, commitSubtask, getReviewTask, rejectSubtask } from '../../api/rdcReview';
import { Button, Input } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { DetailDrawer } from '../../components/DetailDrawer';
import { Empty, ErrorState, Loading } from '../../components/States';
import {
  devKindLabel,
  devStatusLabel,
  formatDate,
  projectLabel,
  reviewActionLabel,
} from '../../utils/format';
import { DiffReviewPanel } from './DiffReviewPanel';
import { ApprovalBadge, CheckpointTimeline, PlanConfirmationPanel } from './InteractionPanels';

interface ReviewDetailDrawerProps {
  taskID: string | null;
  onClose: () => void;
  onChanged: () => void;
}

interface DrawerState {
  loading: boolean;
  data: ReviewDetail | null;
  error: string;
}

/** 需要审核动作的当前态：审核中可「通过/驳回」，待提交可「提交确认」，已驳回仅提示。 */
type ReviewableStatus = 'reviewing' | 'waiting_commit' | 'rejected';

/** 打开的输入对话框：驳回填原因，提交填 commit SHA。 */
type ReviewDialog =
  | { kind: 'reject'; subtask: DevSubtask; reason: string }
  | { kind: 'commit'; subtask: DevSubtask; commitSha: string }
  | null;

const EMPTY: DrawerState = { loading: false, data: null, error: '' };

function isReviewable(status: string): status is ReviewableStatus {
  return status === 'reviewing' || status === 'waiting_commit' || status === 'rejected';
}

export function ReviewDetailDrawer({
  taskID,
  onClose,
  onChanged,
}: ReviewDetailDrawerProps): JSX.Element {
  const [state, setState] = useState<DrawerState>(EMPTY);
  const [reloadKey, setReloadKey] = useState(0);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState('');
  const [dialog, setDialog] = useState<ReviewDialog>(null);

  useEffect(() => {
    if (!taskID) {
      setState(EMPTY);
      setNotice('');
      setDialog(null);
      return;
    }
    let cancelled = false;
    setState({ loading: true, data: null, error: '' });
    setNotice('');
    setDialog(null);
    getReviewTask(taskID)
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

  const runApprove = async (subtask: DevSubtask): Promise<void> => {
    if (!taskID || busy) {
      return;
    }
    setBusy(true);
    setNotice('');
    try {
      await approveSubtask(taskID, subtask.id);
      reload();
    } catch (err) {
      setNotice(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const submitDialog = async (): Promise<void> => {
    if (!taskID || !dialog || busy) {
      return;
    }
    setBusy(true);
    setNotice('');
    try {
      if (dialog.kind === 'reject') {
        await rejectSubtask(taskID, dialog.subtask.id, dialog.reason);
      } else {
        await commitSubtask(taskID, dialog.subtask.id, dialog.commitSha);
      }
      setDialog(null);
      reload();
    } catch (err) {
      setNotice(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const body = (): JSX.Element => {
    if (state.loading) {
      return <Loading text="正在读取审核详情…" />;
    }
    if (state.error) {
      return <ErrorState message={state.error} onRetry={() => setReloadKey((v) => v + 1)} />;
    }
    if (!taskID) {
      return <Empty title="无审核内容" />;
    }
    const data = state.data;
    if (!data) {
      return <Empty title="无审核内容" />;
    }
    return (
      <div className="rdc-review-detail">
        <div className="rdc-review-task-meta">
          <p>
            <strong>{data.task.title}</strong>（{data.task.reqKey}）
          </p>
          <p className="cell-sub">
            所属项目：{projectLabel(data.task.projectName)} · 整体状态：
            {devStatusLabel(data.task.overallStatus)}
          </p>
        </div>

        {notice ? <p className="rdc-inline-error">{notice}</p> : null}

        <div className="rdc-dev-subtask-grid">
          {data.subtasks.map((subtask) => (
            <section className="rdc-dev-subtask-card" key={subtask.id}>
              <header>
                <strong>{devKindLabel(subtask.kind)}子任务</strong>
                <span className={`badge rdc-status rdc-dev-status-${subtask.status}`}>
                  {devStatusLabel(subtask.status)}
                </span>
              </header>
              <p className="cell-sub">最近更新：{formatDate(subtask.updatedAt)}</p>
              <div className="rdc-dev-subtask-actions">
                {subtask.status === 'reviewing' ? (
                  <>
                    <Button
                      className="button button-primary"
                      type="button"
                      disabled={busy}
                      onClick={() => void runApprove(subtask)}
                    >
                      审核通过
                    </Button>
                    <Button
                      className="button button-ghost"
                      type="button"
                      disabled={busy}
                      onClick={() => setDialog({ kind: 'reject', subtask, reason: '' })}
                    >
                      驳回
                    </Button>
                  </>
                ) : null}
                {subtask.status === 'waiting_commit' ? (
                  <Button
                    className="button button-primary"
                    type="button"
                    disabled={busy}
                    onClick={() => setDialog({ kind: 'commit', subtask, commitSha: '' })}
                  >
                    确认人工提交
                  </Button>
                ) : null}
                {subtask.status === 'rejected' ? (
                  <p className="cell-sub">已驳回，请在 AI 研发中心对该子任务「重新研发」。</p>
                ) : null}
                {!isReviewable(subtask.status) ? (
                  <p className="cell-sub">当前状态无需人工审核操作。</p>
                ) : null}
              </div>

              <ApprovalBadge
                taskId={taskID}
                subtaskId={subtask.id}
                kind={subtask.kind}
                onNotice={setNotice}
              />
              <PlanConfirmationPanel
                taskId={taskID}
                subtaskId={subtask.id}
                kind={subtask.kind}
                onNotice={setNotice}
              />
              <DiffReviewPanel
                taskId={taskID}
                subtaskId={subtask.id}
                kind={subtask.kind}
                onNotice={setNotice}
              />
              <CheckpointTimeline
                taskId={taskID}
                subtaskId={subtask.id}
                kind={subtask.kind}
                onNotice={setNotice}
              />
            </section>
          ))}
        </div>

        <section className="rdc-review-history">
          <h4>审核历史</h4>
          {data.records.length === 0 ? (
            <p className="cell-sub">暂无审核记录。</p>
          ) : (
            <ul>
              {data.records.map((record) => {
                const subtask = data.subtasks.find((item) => item.id === record.subtaskId);
                return (
                  <li key={record.id}>
                    {formatDate(record.createdAt)} ·{' '}
                    {subtask ? devKindLabel(subtask.kind) : '子任务'}
                    {' · '}
                    {reviewActionLabel(record.action)}
                    {record.reason ? ` · 原因：${record.reason}` : ''}
                    {record.commitSha ? ` · commit：${record.commitSha}` : ''}
                  </li>
                );
              })}
            </ul>
          )}
        </section>
      </div>
    );
  };

  const dialogReason = dialog?.kind === 'reject';
  return (
    <>
      <DetailDrawer open={taskID !== null} title="人工审核" onClose={onClose} width={1040}>
        {body()}
      </DetailDrawer>
      <ConfirmDialog
        open={dialog !== null}
        title={dialogReason ? '确认驳回该子任务？' : '确认人工提交？'}
        description={
          dialog?.kind === 'reject' ? (
            <>
              <p>驳回后子任务进入「已驳回」阻断态，需在 AI 研发中心重新研发才能继续。</p>
              <Input
                value={dialog.reason}
                onChange={(event) =>
                  setDialog((prev) =>
                    prev?.kind === 'reject' ? { ...prev, reason: event.target.value } : prev,
                  )
                }
                placeholder="请填写驳回原因（必填）"
              />
            </>
          ) : (
            <>
              <p>确认后子任务进入「已提交」。此处仅记录 commit SHA，不触发真实 Git 操作。</p>
              <Input
                value={dialog?.kind === 'commit' ? dialog.commitSha : ''}
                onChange={(event) =>
                  setDialog((prev) =>
                    prev?.kind === 'commit' ? { ...prev, commitSha: event.target.value } : prev,
                  )
                }
                placeholder="请填写 commit SHA（必填）"
              />
            </>
          )
        }
        confirmText={dialogReason ? '驳回' : '确认提交'}
        danger={dialogReason}
        loading={busy}
        onConfirm={() => void submitDialog()}
        onCancel={() => setDialog(null)}
      />
    </>
  );
}
