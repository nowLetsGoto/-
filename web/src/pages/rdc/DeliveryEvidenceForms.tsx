/**
 * 职责：人工登记外部测试结论与逐条验收结果。
 * 输入：当前需求标准与最新快照；输出：严格契约输入，成功后回读详情。
 * 边界：不执行测试、不上传附件；服务端重算条目并决定能否归档。
 */
import { useEffect, useState } from 'react';
import type { DeliveryDetail, DeliveryAcceptInput, TestRegisterInput } from '../../api/rdcDelivery';
import { acceptDelivery, registerDeliveryTest } from '../../api/rdcDelivery';
import { errorMessage } from '../../api/client';
import { Button, Input, Segmented, TextArea } from '../../components/Controls';
import { splitAcceptanceCriteria } from '../../utils/rdcAcceptance';
import { ACCEPTANCE_STATUS_LABELS, TEST_RESULT_LABELS } from '../../utils/format';

export function DeliveryEvidenceForms({
  detail,
  onChanged,
}: {
  detail: DeliveryDetail;
  onChanged: () => void;
}): JSX.Element {
  const record = detail.current;
  const criteria = splitAcceptanceCriteria(detail.acceptanceCriteria);
  const [test, setTest] = useState<TestRegisterInput>({
    result: 'passed',
    summary: '',
    reportRef: '',
  });
  const [acceptance, setAcceptance] = useState<DeliveryAcceptInput>({
    result: 'passed',
    note: '',
    items: [],
  });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    setTest({
      result: record.testResult === 'failed' ? 'failed' : 'passed',
      summary: record.testSummary,
      reportRef: record.testReportRef,
    });
    let saved: DeliveryAcceptInput['items'] = [];
    try {
      saved = JSON.parse(record.acceptanceItems) as DeliveryAcceptInput['items'];
    } catch {
      saved = [];
    }
    setAcceptance({
      result: record.acceptanceStatus === 'failed' ? 'failed' : 'passed',
      note: record.acceptanceNote,
      items: criteria.map(
        (item) =>
          saved.find((entry) => entry.index === item.index) ?? {
            index: item.index,
            result: 'passed',
          },
      ),
    });
    setError('');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detail.requirementId, record.seq, detail.acceptanceCriteria]);
  const submit = async (action: 'test' | 'accept'): Promise<void> => {
    setSaving(true);
    setError('');
    try {
      if (action === 'test') await registerDeliveryTest(detail.requirementId, test);
      else await acceptDelivery(detail.requirementId, acceptance);
      onChanged();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };
  return (
    <>
      <section className="rdc-delivery-section">
        <h3>测试报告</h3>
        <p>
          当前结论：{TEST_RESULT_LABELS[record.testResult]}
          {record.testRegisteredAt ? ` · ${record.testRegisteredAt}` : ''}
        </p>
        {record.testSummary ? <p className="rdc-evidence-text">{record.testSummary}</p> : null}
        {record.testReportRef ? (
          <a href={record.testReportRef} target="_blank" rel="noopener noreferrer">
            查看外部报告
          </a>
        ) : null}
        {!record.archived ? (
          <div className="rdc-evidence-form">
            <Segmented<'passed' | 'failed'>
              value={test.result as 'passed' | 'failed'}
              options={[
                { value: 'passed', label: '通过' },
                { value: 'failed', label: '未通过' },
              ]}
              onChange={(result) => setTest((previous) => ({ ...previous, result }))}
            />
            <TextArea
              value={test.summary}
              maxLength={2000}
              placeholder="外部 CI 测试结论摘要"
              onChange={(event) =>
                setTest((previous) => ({ ...previous, summary: event.target.value }))
              }
            />
            <Input
              value={test.reportRef ?? ''}
              maxLength={500}
              placeholder="外部报告链接（可选）"
              onChange={(event) =>
                setTest((previous) => ({ ...previous, reportRef: event.target.value }))
              }
            />
            <Button
              type="button"
              className="button button-primary"
              disabled={saving || !test.summary.trim()}
              onClick={() => void submit('test')}
            >
              登记测试
            </Button>
          </div>
        ) : null}
      </section>
      <section className="rdc-delivery-section">
        <h3>验收结果</h3>
        <p>
          当前结论：{ACCEPTANCE_STATUS_LABELS[record.acceptanceStatus]}
          {record.acceptedAt ? ` · ${record.acceptedAt}` : ''}
        </p>
        {record.acceptanceNote ? (
          <p className="rdc-evidence-text">{record.acceptanceNote}</p>
        ) : null}
        {!record.archived && criteria.length > 0 ? (
          <div className="rdc-evidence-form">
            <ol className="rdc-acceptance-items">
              {criteria.map((item) => (
                <li key={item.index}>
                  <span>{item.text}</span>
                  <Segmented<'passed' | 'failed'>
                    value={
                      (acceptance.items.find((entry) => entry.index === item.index)?.result as
                        'passed' | 'failed') ?? 'passed'
                    }
                    options={[
                      { value: 'passed', label: '通过' },
                      { value: 'failed', label: '未通过' },
                    ]}
                    onChange={(result) =>
                      setAcceptance((previous) => ({
                        ...previous,
                        items: previous.items.map((entry) =>
                          entry.index === item.index ? { index: item.index, result } : entry,
                        ),
                      }))
                    }
                  />
                </li>
              ))}
            </ol>
            <Segmented<'passed' | 'failed'>
              value={acceptance.result as 'passed' | 'failed'}
              options={[
                { value: 'passed', label: '验收通过' },
                { value: 'failed', label: '验收不通过' },
              ]}
              onChange={(result) => setAcceptance((previous) => ({ ...previous, result }))}
            />
            <TextArea
              value={acceptance.note}
              maxLength={2000}
              placeholder="验收备注"
              onChange={(event) =>
                setAcceptance((previous) => ({ ...previous, note: event.target.value }))
              }
            />
            <Button
              type="button"
              className="button button-primary"
              disabled={saving}
              onClick={() => void submit('accept')}
            >
              提交验收
            </Button>
            <p className="settings-hint">验收不通过可登记，但不能归档。</p>
          </div>
        ) : criteria.length === 0 ? (
          <p>需求尚未填写验收标准，不能验收或归档。</p>
        ) : null}
        {error ? <p className="field-error">{error}</p> : null}
      </section>
    </>
  );
}
