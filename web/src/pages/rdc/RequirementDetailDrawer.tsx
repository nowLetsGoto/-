/**
 * 需求详情抽屉。
 * 职责：按需求编号取详情并只读展示全部字段、项目名、状态标签、创建人、时间与图片缩略图网格。
 * 输入：reqKey（null 表示关闭）；输出：DetailDrawer + 详情内容与可点击预览的大图。
 * 边界：图片接口受保护（需 Bearer），因此走 getBlob + createObjectURL 渲染，裸 img src 会 401；
 *       关闭或切换需求时 revoke 全部 objectURL；单张图片失败只显示错误格，不影响其余字段；
 *       本版不提供编辑/删除按钮（避免假操作）。
 */
import { useEffect, useState } from 'react';
import type { RequirementDetail } from '../../api/rdc';
import { getRequirement, getRequirementAttachment } from '../../api/rdc';
import { errorMessage } from '../../api/client';
import { DetailDrawer } from '../../components/DetailDrawer';
import { ErrorState, Loading } from '../../components/States';
import { formatDate, projectLabel, requirementStatusLabel } from '../../utils/format';

interface RequirementDetailDrawerProps {
  /** null 表示抽屉关闭 */
  reqKey: string | null;
  onClose: () => void;
}

interface DrawerState {
  loading: boolean;
  data: RequirementDetail | null;
  error: string;
}

const EMPTY: DrawerState = { loading: false, data: null, error: '' };

export function RequirementDetailDrawer({
  reqKey,
  onClose,
}: RequirementDetailDrawerProps): JSX.Element {
  const [state, setState] = useState<DrawerState>(EMPTY);
  const [imageURLs, setImageURLs] = useState<Record<string, string>>({});
  const [failedImages, setFailedImages] = useState<Set<string>>(new Set());
  const [previewID, setPreviewID] = useState<string | null>(null);

  useEffect(() => {
    if (!reqKey) {
      setState(EMPTY);
      setImageURLs({});
      setFailedImages(new Set());
      setPreviewID(null);
      return;
    }
    let cancelled = false;
    // 本轮创建的所有 objectURL：effect 清理（关闭 / 切换需求）时统一回收。
    const created: string[] = [];
    setState({ loading: true, data: null, error: '' });
    setImageURLs({});
    setFailedImages(new Set());
    setPreviewID(null);

    getRequirement(reqKey)
      .then(async (data) => {
        if (cancelled) {
          return;
        }
        setState({ loading: false, data, error: '' });
        const urls: Record<string, string> = {};
        for (const attachment of data.attachments) {
          try {
            const blob = await getRequirementAttachment(attachment.id);
            if (cancelled) {
              break;
            }
            const url = URL.createObjectURL(blob);
            created.push(url);
            urls[attachment.id] = url;
          } catch {
            if (!cancelled) {
              setFailedImages((current) => new Set(current).add(attachment.id));
            }
          }
        }
        if (!cancelled) {
          setImageURLs(urls);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ loading: false, data: null, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
      created.forEach((url) => URL.revokeObjectURL(url));
    };
  }, [reqKey]);

  const item = state.data;
  const close = (): void => {
    setPreviewID(null);
    onClose();
  };

  return (
    <DetailDrawer open={Boolean(reqKey)} title={item?.title ?? '需求详情'} onClose={close}>
      {state.loading ? <Loading text="正在加载需求详情…" /> : null}
      {!state.loading && state.error ? <ErrorState message={state.error} /> : null}

      {!state.loading && !state.error && item ? (
        <div className="drawer-body">
          <dl className="kv-list">
            <div>
              <dt>需求编号</dt>
              <dd>
                <code>{item.reqKey}</code>
              </dd>
            </div>
            <div>
              <dt>所属项目</dt>
              <dd>{projectLabel(item.projectName)}</dd>
            </div>
            <div>
              <dt>标题</dt>
              <dd>{item.title}</dd>
            </div>
            <div>
              <dt>状态</dt>
              <dd>
                <span className={`badge rdc-status rdc-status-${item.status}`}>
                  {requirementStatusLabel(item.status)}
                </span>
              </dd>
            </div>
            <div>
              <dt>创建人</dt>
              <dd>{item.creatorName || '—'}</dd>
            </div>
            <div>
              <dt>创建时间</dt>
              <dd>{formatDate(item.createdAt)}</dd>
            </div>
          </dl>

          <section className="drawer-section">
            <h3>需求描述</h3>
            <p className="rdc-long-text">{item.description || '—'}</p>
          </section>

          <section className="drawer-section">
            <h3>验收标准</h3>
            <p className="rdc-long-text">{item.acceptanceCriteria || '未填写'}</p>
          </section>

          {/* 存量需求没有图片时不渲染空网格，保持旧数据只读可用。 */}
          {item.attachments.length > 0 ? (
            <section className="drawer-section">
              <h3>图片附件（{item.attachments.length}）</h3>
              <div className="rdc-image-grid">
                {item.attachments.map((attachment) => {
                  const url = imageURLs[attachment.id];
                  return (
                    <button
                      type="button"
                      className="rdc-image-cell"
                      key={attachment.id}
                      disabled={!url}
                      title={url ? '点击查看大图' : '图片暂不可用'}
                      onClick={() => setPreviewID(attachment.id)}
                    >
                      {url ? (
                        <img src={url} alt={attachment.fileName} />
                      ) : (
                        <span className="cell-sub">
                          {failedImages.has(attachment.id) ? '图片加载失败' : '加载中…'}
                        </span>
                      )}
                      <span className="rdc-image-name">{attachment.fileName}</span>
                    </button>
                  );
                })}
              </div>
            </section>
          ) : null}

          <section className="drawer-section">
            <h3>后续能力</h3>
            <p className="settings-hint">
              需求编辑、图片替换与 AI 需求分析将在后续迭代开放；本版需求详情为只读。
            </p>
          </section>
        </div>
      ) : null}

      {previewID && imageURLs[previewID] ? (
        <div className="rdc-image-preview" role="presentation" onClick={() => setPreviewID(null)}>
          <img src={imageURLs[previewID]} alt="图片预览" />
          <span className="cell-sub">点击任意位置关闭</span>
        </div>
      ) : null}
    </DetailDrawer>
  );
}
