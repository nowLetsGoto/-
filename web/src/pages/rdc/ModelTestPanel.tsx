/**
 * AI模型实测面板。
 * 职责：二次确认（消耗少量 token）→ 调实测接口 → 展示分类提示 / 状态码 / 耗时 / 脱敏摘要。
 * 输入：当前模型 id、超管代配的公司 id、关闭回调。
 * 输出：抽屉内的结果卡片；失败只提示，不回滚任何已保存配置。
 * 边界：不发 LLM 请求、不显示 Key 或密文；上游原文不回显，仅展示后端已脱敏的摘要。
 */
import { useEffect, useState } from 'react';
import { errorMessage } from '../../api/client';
import { testAIModel } from '../../api/rdcModel';
import type { AIModelTestResult } from '../../api/rdcModel';
import { Button } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { DetailDrawer } from '../../components/DetailDrawer';
import { Empty, ErrorState, Loading } from '../../components/States';
import { aiTestCategoryLabel } from '../../utils/format';

interface ModelTestPanelProps {
  modelID: string | null;
  tenantId?: string;
  backTo: string;
  onClose: () => void;
}

export function ModelTestPanel({
  modelID,
  tenantId,
  backTo,
  onClose,
}: ModelTestPanelProps): JSX.Element {
  const [confirming, setConfirming] = useState(false);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<AIModelTestResult | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    setConfirming(false);
    setLoading(false);
    setResult(null);
    setError('');
  }, [modelID]);

  const runTest = async (): Promise<void> => {
    if (!modelID) {
      return;
    }
    setLoading(true);
    setError('');
    setResult(null);
    setConfirming(false);
    try {
      setResult(await testAIModel(modelID, tenantId));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  };

  return (
    <>
      <DetailDrawer open={Boolean(modelID)} title="模型实测" onClose={onClose} width={560}>
        <p className="rdc-model-test-hint">
          实测会用该模型已保存的 Key 发起一次真实调用，消耗少量 token；响应摘要已脱敏，不会显示
          Key。
        </p>
        <Button
          type="button"
          className="button button-primary"
          disabled={loading}
          onClick={() => setConfirming(true)}
        >
          {loading ? '正在调用…' : '确认测试连接'}
        </Button>
        {loading ? <Loading text="正在等待模型响应（最长 120 秒）…" /> : null}
        {error ? <ErrorState message={error} /> : null}
        {result ? (
          <div className={`rdc-model-result ${result.ok ? 'is-ok' : 'is-failed'}`}>
            <p className="rdc-model-result-title">
              {result.ok ? '调用成功' : aiTestCategoryLabel(result.category)}
            </p>
            <dl>
              <dt>分类</dt>
              <dd>{result.category}</dd>
              <dt>状态码</dt>
              <dd>{result.statusCode || '—'}</dd>
              <dt>耗时</dt>
              <dd>{result.latencyMs} ms</dd>
              <dt>请求摘要</dt>
              <dd className="cell-break">{result.requestSummary || '—'}</dd>
              <dt>响应预览</dt>
              <dd className="cell-break">{result.responsePreview || '—'}</dd>
              <dt>token 用量</dt>
              <dd>
                {result.promptTokens} / {result.completionTokens}
              </dd>
            </dl>
            <p className="settings-hint">
              调用失败不会回滚配置；请回到<a href={backTo}>模型列表</a>检查 Key、模型 ID 或思考 /
              上下文参数。
            </p>
          </div>
        ) : null}
        {!result && !loading && !error ? (
          <Empty
            title="尚未发起实测"
            description="点击上方按钮，用已保存的 Key 验证配置是否生效。"
          />
        ) : null}
      </DetailDrawer>

      <ConfirmDialog
        open={confirming}
        title="确认发起一次实测？"
        description={
          <>
            将用该模型已保存的 Key 调用一次 <code>chat/completions</code>，
            <strong>会消耗少量 token</strong>。
            失败只展示分类提示，不会修改或回滚模型配置；请确认已在表单中录入轮换后的新 Key。
          </>
        }
        confirmText="确认测试连接"
        onConfirm={() => void runTest()}
        onCancel={() => setConfirming(false)}
      />
    </>
  );
}
