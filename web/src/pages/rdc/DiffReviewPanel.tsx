/**
 * Diff 审查三栏面板（挂 03 审核详情抽屉，仅 team）。
 * 职责：左栏文件 tab 与处理进度、中栏逐 hunk 三态决策与风险标签、右栏「应用后完整文本」纯预览。
 * 输入：taskId / subtaskId、通知回调；数据来自 rdcInteraction API。
 * 输出：三栏审查面板与提交条；无真实产物时只渲染真实空态，不渲染接受/拒绝按钮。
 * 边界：右栏由 applyHunks 纯函数实时重算，不发请求、不改真实文件、不渲染运行效果；
 *       前端只发决策证据请求，绝不触碰子任务状态机。
 */
import { useEffect, useMemo, useState } from 'react';
import { errorMessage } from '../../api/client';
import type { Decision, DiffReview } from '../../api/rdcInteraction';
import { getDiffReview, listCheckpoints, submitDiffReview } from '../../api/rdcInteraction';
import { Button, Segmented } from '../../components/Controls';
import { Empty, ErrorState, Loading } from '../../components/States';
import {
  decisionLabel,
  devKindLabel,
  INTERACTION_FILE_STATUS_LABELS,
  riskLabel,
} from '../../utils/format';
import { applyHunks } from '../../utils/applyHunks';

interface DiffReviewPanelProps {
  taskId: string;
  subtaskId: string;
  kind: string;
  onNotice: (message: string) => void;
}

interface PanelState {
  loading: boolean;
  error: string;
  review: DiffReview | null;
}

const INITIAL: PanelState = { loading: false, error: '', review: null };

/** 把服务端决策投影成前端三态字典；缺省视为 pending。 */
function toDecisions(review: DiffReview | null): Record<string, Decision> {
  const result: Record<string, Decision> = {};
  if (!review) {
    return result;
  }
  for (const decision of review.decisions) {
    result[decision.hunkId] = decision.decision;
  }
  return result;
}

export function DiffReviewPanel({
  taskId,
  subtaskId,
  kind,
  onNotice,
}: DiffReviewPanelProps): JSX.Element {
  const [state, setState] = useState<PanelState>(INITIAL);
  const [fileIndex, setFileIndex] = useState(0);
  const [decisions, setDecisions] = useState<Record<string, Decision>>({});
  const [bases, setBases] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState({ loading: true, error: '', review: null });
    setDecisions({});
    setBases({});
    setFileIndex(0);
    Promise.all([getDiffReview(taskId, subtaskId), listCheckpoints(taskId, subtaskId)])
      .then(([review, checkpoints]) => {
        if (!cancelled) {
          setState({ loading: false, error: '', review });
          setDecisions(toDecisions(review));
          // base 只取真实检查点里的 beforeContent 快照；没有快照时才退回 hunk 原始行重建。
          const snapshotBases: Record<string, string> = {};
          for (const checkpoint of checkpoints) {
            for (const file of checkpoint.files) {
              if (snapshotBases[file.filePath] === undefined) {
                snapshotBases[file.filePath] = file.beforeContent;
              }
            }
          }
          setBases(snapshotBases);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ loading: false, error: errorMessage(err), review: null });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [taskId, subtaskId, reloadKey, setBases]);

  const review = state.review;
  const file = review?.files[Math.min(fileIndex, Math.max(review.files.length - 1, 0))];
  const progress = useMemo(() => {
    if (!review) {
      return { total: 0, processed: 0 };
    }
    let total = 0;
    let processed = 0;
    for (const item of review.files) {
      for (const hunk of item.hunks) {
        total += 1;
        if (decisions[hunk.hunkId] && decisions[hunk.hunkId] !== 'pending') {
          processed += 1;
        }
      }
    }
    return { total, processed };
  }, [review, decisions]);

  const preview = useMemo(() => {
    if (!file || !review) {
      return { content: '', conflicts: [] as string[] };
    }
    // added 文件 base 为空；其余优先用检查点 beforeContent 真实快照，缺失才按 hunk 原始行重建。
    const base =
      file.fileStatus === 'added' ? '' : (bases[file.filePath] ?? baseFromHunks(file.hunks));
    return applyHunks(base, file.hunks, decisions);
  }, [file, review, decisions, bases]);

  const changeDecision = (hunkId: string, decision: Decision): void => {
    setDecisions((prev) => ({ ...prev, [hunkId]: decision }));
  };

  const submit = async (): Promise<void> => {
    if (!review || busy || progress.total === 0 || progress.processed < progress.total) {
      return;
    }
    setBusy(true);
    onNotice('');
    try {
      const all = review.files.flatMap((item) =>
        item.hunks.map((hunk) => ({
          reviewId: review.id,
          hunkId: hunk.hunkId,
          decision: decisions[hunk.hunkId] ?? 'pending',
          decidedBy: '',
          decidedAt: '',
        })),
      );
      await submitDiffReview(taskId, subtaskId, { decisions: all });
      onNotice('审查已提交，等待执行器应用；本次操作未修改真实文件，也未改变子任务状态。');
      setReloadKey((value) => value + 1);
    } catch (err) {
      onNotice(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const body = (): JSX.Element => {
    if (state.loading) {
      return <Loading text="正在读取 Diff 审查…" />;
    }
    if (state.error) {
      return <ErrorState message={state.error} onRetry={() => setReloadKey((v) => v + 1)} />;
    }
    if (!review || review.files.length === 0) {
      return (
        <Empty
          title="等待 Agent 执行器就绪"
          description="暂无真实 Diff 产物。执行器就绪并产出后，这里才会出现逐 hunk 审查控件。"
        />
      );
    }
    const allDeleted =
      file?.fileStatus === 'deleted' &&
      file.hunks.length > 0 &&
      file.hunks.every((hunk) => decisions[hunk.hunkId] === 'accepted');
    return (
      <div className="rdc-diff-grid">
        <aside className="rdc-diff-files">
          <h5>
            {devKindLabel(kind)}文件（{review.files.length}）
          </h5>
          <Segmented<string>
            aria-label="选择 Diff 文件"
            value={String(fileIndex)}
            options={review.files.map((item, index) => {
              const itemDone = item.hunks.filter(
                (hunk) => decisions[hunk.hunkId] && decisions[hunk.hunkId] !== 'pending',
              ).length;
              return {
                value: String(index),
                label: `${item.filePath} · ${itemDone}/${item.hunks.length}`,
              };
            })}
            onChange={(value) => setFileIndex(Number(value))}
          />
        </aside>
        <section className="rdc-diff-hunks">
          {file?.hunks.map((hunk) => {
            const decision = decisions[hunk.hunkId] ?? 'pending';
            return (
              <article className="rdc-diff-hunk" key={hunk.hunkId}>
                <header>
                  <span className="badge rdc-risk-badge">{riskLabel(hunk.riskLabel)}</span>
                  <span className="cell-sub">
                    {INTERACTION_FILE_STATUS_LABELS[file.fileStatus]} · 第 {hunk.oldStart} 行
                  </span>
                  <span className="badge">{decisionLabel(decision)}</span>
                </header>
                {hunk.contextBefore.length > 0 ? (
                  <pre className="rdc-diff-context">{hunk.contextBefore.join('\n')}</pre>
                ) : null}
                <pre className="rdc-diff-old">{hunk.oldContent.join('\n')}</pre>
                <pre className="rdc-diff-new">{hunk.newContent.join('\n')}</pre>
                {hunk.contextAfter.length > 0 ? (
                  <pre className="rdc-diff-context">{hunk.contextAfter.join('\n')}</pre>
                ) : null}
                <div className="rdc-diff-hunk-actions">
                  <Button
                    type="button"
                    className={
                      decision === 'accepted' ? 'button button-primary' : 'button button-ghost'
                    }
                    onClick={() => changeDecision(hunk.hunkId, 'accepted')}
                  >
                    接受
                  </Button>
                  <Button
                    type="button"
                    className={
                      decision === 'rejected' ? 'button button-primary' : 'button button-ghost'
                    }
                    onClick={() => changeDecision(hunk.hunkId, 'rejected')}
                  >
                    拒绝
                  </Button>
                  <Button
                    type="button"
                    className="button button-ghost"
                    onClick={() => changeDecision(hunk.hunkId, 'pending')}
                  >
                    重置
                  </Button>
                </div>
              </article>
            );
          })}
        </section>
        <section className="rdc-diff-preview">
          <h5>应用后文件内容（纯文本预览）</h5>
          {preview.conflicts.length > 0 ? (
            <p className="rdc-inline-error">重叠 hunk 已跳过：{preview.conflicts.join('、')}</p>
          ) : null}
          {allDeleted ? <p className="cell-sub">（文件已删除）</p> : <pre>{preview.content}</pre>}
          <p className="cell-sub">此处仅重算文本，不改真实文件、不运行代码。</p>
        </section>
      </div>
    );
  };

  return (
    <section className="rdc-interaction-block">
      <h4>Diff 审查</h4>
      {body()}
      {review && review.files.length > 0 ? (
        <div className="rdc-diff-submit-bar">
          <span className="cell-sub">
            {progress.processed}/{progress.total} 已处理
            {progress.processed < progress.total ? '，全部处理后才能提交' : ''}
          </span>
          <Button
            type="button"
            className="button button-primary"
            disabled={busy || progress.processed < progress.total}
            onClick={() => void submit()}
          >
            提交审查
          </Button>
        </div>
      ) : null}
    </section>
  );
}

/**
 * baseFromHunks 用已接受之外的原始行拼出可视基线：
 * hunk 的 oldContent 是被替换的原始行；对未覆盖行按 newStart/oldStart 位置留空占位。
 * 真实执行器就绪后产物会携带完整 before 快照，届时此处改读快照字段即可，纯函数口径不变。
 */
function baseFromHunks(hunks: DiffReview['files'][number]['hunks']): string {
  if (hunks.length === 0) {
    return '';
  }
  const maxLine = Math.max(...hunks.map((hunk) => hunk.oldStart + hunk.oldLines - 1));
  const lines: string[] = Array.from({ length: Math.max(maxLine, 0) }, () => '');
  for (const hunk of hunks) {
    for (let index = 0; index < hunk.oldContent.length; index += 1) {
      lines[hunk.oldStart - 1 + index] = hunk.oldContent[index] ?? '';
    }
  }
  return lines.join('\n');
}
