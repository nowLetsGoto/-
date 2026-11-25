/**
 * 职责：展示需求交付四段证据、历史快照及不可逆归档确认。
 * 输入：列表选择的需求 ID；输出：详情抽屉与成功后列表刷新通知。
 * 边界：终态隐藏所有写入口；归档最终前置仍由服务端事务复核。
 */
import { useEffect, useState } from 'react';
import { getDeliveryDetail, archiveDelivery } from '../../api/rdcDelivery';
import type { DeliveryDetail } from '../../api/rdcDelivery';
import { errorMessage } from '../../api/client';
import { Button } from '../../components/Controls';
import { DetailDrawer } from '../../components/DetailDrawer';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { ErrorState, Loading } from '../../components/States';
import {
  ACCEPTANCE_STATUS_LABELS,
  formatDate,
  projectLabel,
  TEST_RESULT_LABELS,
} from '../../utils/format';
import { splitAcceptanceCriteria } from '../../utils/rdcAcceptance';
import { DeliveryGitPanel } from './DeliveryGitPanel';
import { DeliveryEvidenceForms } from './DeliveryEvidenceForms';

export function DeliveryDetailDrawer({
  requirementID,
  onClose,
  onChanged,
}: {
  requirementID: string | null;
  onClose: () => void;
  onChanged: () => void;
}): JSX.Element {
  const [detail, setDetail] = useState<DeliveryDetail | null>(null);
  const [error, setError] = useState('');
  const [reload, setReload] = useState(0);
  const [confirm, setConfirm] = useState(false);
  const [archiving, setArchiving] = useState(false);
  useEffect(() => {
    if (!requirementID) return;
    let active = true;
    setDetail(null);
    setError('');
    getDeliveryDetail(requirementID)
      .then((value) => {
        if (active) setDetail(value);
      })
      .catch((err: unknown) => {
        if (active) setError(errorMessage(err));
      });
    return () => {
      active = false;
    };
  }, [requirementID, reload]);
  const changed = (): void => {
    setReload((value) => value + 1);
    onChanged();
  };
  const archive = async (): Promise<void> => {
    if (!requirementID) return;
    setArchiving(true);
    setError('');
    try {
      await archiveDelivery(requirementID);
      setConfirm(false);
      changed();
    } catch (err) {
      setError(errorMessage(err));
      setConfirm(false);
    } finally {
      setArchiving(false);
    }
  };
  const current = detail?.current;
  const canArchive =
    detail &&
    current &&
    !current.archived &&
    current.acceptanceStatus === 'passed' &&
    splitAcceptanceCriteria(detail.acceptanceCriteria).length > 0;
  return (
    <DetailDrawer
      open={requirementID !== null}
      title={detail ? `${detail.reqKey} · ${detail.title}` : '交付详情'}
      onClose={onClose}
      width={720}
    >
      {error ? (
        <ErrorState message={error} onRetry={() => setReload((value) => value + 1)} />
      ) : !detail ? (
        <Loading text="读取交付详情中…" />
      ) : (
        <div className="rdc-delivery-detail">
          <dl className="kv-list">
            <div>
              <dt>项目</dt>
              <dd>{projectLabel(detail.projectName)}</dd>
            </div>
            <div>
              <dt>需求状态</dt>
              <dd>{detail.requirementStatus}</dd>
            </div>
            <div>
              <dt>验收标准</dt>
              <dd className="rdc-evidence-text">{detail.acceptanceCriteria || '尚未填写'}</dd>
            </div>
          </dl>
          <DeliveryGitPanel requirementID={detail.requirementId} />
          <DeliveryEvidenceForms detail={detail} onChanged={changed} />
          <section className="rdc-delivery-section">
            <div className="rdc-delivery-heading">
              <h3>需求归档</h3>
              {canArchive ? (
                <Button
                  type="button"
                  className="button button-primary"
                  onClick={() => setConfirm(true)}
                >
                  归档需求
                </Button>
              ) : null}
            </div>
            <p>
              {current?.archived
                ? `已归档 · ${current.archivedAt ? formatDate(current.archivedAt) : ''}`
                : current?.acceptanceStatus === 'failed'
                  ? '验收不通过，不能归档。'
                  : '验收全部通过后可归档。'}
            </p>
          </section>
          <section className="rdc-delivery-section">
            <h3>交付历史</h3>
            {detail.history.length ? (
              <ol className="rdc-history">
                {detail.history.map((row) => (
                  <li key={row.id}>
                    <time>{formatDate(row.createdAt)}</time>
                    <span>
                      测试 {TEST_RESULT_LABELS[row.testResult]} · 验收{' '}
                      {ACCEPTANCE_STATUS_LABELS[row.acceptanceStatus]}
                      {row.archived ? ' · 已归档' : ''}
                    </span>
                  </li>
                ))}
              </ol>
            ) : (
              <p>暂无登记记录</p>
            )}
          </section>
          <ConfirmDialog
            open={confirm}
            title="归档这条需求？"
            description="归档后关联需求状态将标记为已归档，当前版本无法撤销，也不能继续登记测试或验收。"
            confirmText="确认归档"
            danger
            loading={archiving}
            onConfirm={() => void archive()}
            onCancel={() => setConfirm(false)}
          />
        </div>
      )}
    </DetailDrawer>
  );
}
