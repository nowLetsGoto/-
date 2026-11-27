/**
 * 公网隧道状态面板：只看状态、做检查、点重启，不提供配置编辑。
 * 职责：渲染隧道状态、触发深度检查与重启。
 * 输入：GET /settings/tunnel、POST /settings/tunnel/check、POST /settings/tunnel/restart。
 * 输出：最新隧道状态。
 * 边界：不展示凭据文件路径、不代做登录、不写 DNS。
 *
 * 为什么没有编辑框：隧道凭据在本机 ~/.cloudflared/ 下，登录要浏览器人工授权，
 * DNS 只能去 Cloudflare 控制台改。做成表单只会出现
 * 「页面改了但文件没改」的不一致，比没有页面更糟。
 */
import { useState } from 'react';
import type { TunnelStatus } from '../../api/settings';
import { checkTunnel, getTunnelStatus, restartTunnel } from '../../api/settings';
import { errorMessage } from '../../api/client';
import { Button } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';

interface TunnelPanelProps {
  data: TunnelStatus;
  canWrite: boolean;
  onUpdate: (status: TunnelStatus) => void;
}

export function TunnelPanel({ data, canWrite, onUpdate }: TunnelPanelProps): JSX.Element {
  const [checking, setChecking] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [notice, setNotice] = useState('');

  const runCheck = async (): Promise<void> => {
    setChecking(true);
    setNotice('');
    try {
      onUpdate(await checkTunnel());
    } catch (err) {
      setNotice(errorMessage(err));
    } finally {
      setChecking(false);
    }
  };

  const runRestart = async (): Promise<void> => {
    setRestarting(true);
    setNotice('');
    try {
      onUpdate(await restartTunnel());
      setConfirmOpen(false);
    } catch (err) {
      setNotice(errorMessage(err));
    } finally {
      setRestarting(false);
    }
  };

  const refresh = async (): Promise<void> => {
    setNotice('');
    try {
      onUpdate(await getTunnelStatus());
    } catch (err) {
      setNotice(errorMessage(err));
    }
  };

  return (
    <div className="card settings-card">
      <div className="settings-block">
        <h2>公网访问</h2>
        <p className="settings-hint">
          隧道让外网能访问这台机器上的服务，页面与 API 请求经它转发； 安装包下载直连
          CDN，不经过隧道。两者互不依赖。
        </p>
        <p className="settings-hint">
          这里只展示状态、支持检查与重启。配置凭据、登录授权和 DNS 记录需要在本机与 Cloudflare
          控制台完成，不能从页面修改。
        </p>
      </div>

      <dl className="kv-list">
        <div>
          <dt>连接状态</dt>
          <dd>
            <span
              className={data.reachable ? 'status-dot status-online' : 'status-dot status-failed'}
            >
              {data.reachable ? '公网可访问' : '不可访问'}
            </span>
          </dd>
        </div>
        <div>
          <dt>隧道进程</dt>
          <dd>{data.running ? '运行中' : '未运行'}</dd>
        </div>
        <div>
          <dt>活跃连接器</dt>
          <dd>
            {data.connectors < 0 ? (
              <span className="settings-hint">未检查（点「检查连接」获取）</span>
            ) : (
              data.connectors
            )}
          </dd>
        </div>
        <div>
          <dt>公网地址</dt>
          <dd>
            {data.hostname ? (
              <a href={data.hostname} target="_blank" rel="noreferrer">
                {data.hostname}
              </a>
            ) : (
              '（未配置）'
            )}
          </dd>
        </div>
        <div>
          <dt>隧道 ID</dt>
          <dd className="mono">{data.tunnelId || '（未配置）'}</dd>
        </div>
        <div>
          <dt>本地上游</dt>
          <dd className="mono">{data.upstream || '（未知）'}</dd>
        </div>
      </dl>

      {data.message ? <p className="settings-hint">{data.message}</p> : null}

      <div className="settings-actions">
        <Button
          className="button"
          type="button"
          disabled={checking || restarting || !data.configured}
          onClick={() => void runCheck()}
        >
          {checking ? '检查中…' : '检查连接'}
        </Button>
        {canWrite ? (
          <Button
            className="button"
            type="button"
            disabled={checking || restarting || !data.configured}
            onClick={() => setConfirmOpen(true)}
          >
            重启隧道
          </Button>
        ) : null}
        <Button
          className="button button-ghost"
          type="button"
          disabled={checking || restarting}
          onClick={() => void refresh()}
        >
          刷新状态
        </Button>
        {notice ? <span className="settings-notice">{notice}</span> : null}
      </div>

      <ConfirmDialog
        open={confirmOpen}
        title="重启公网隧道？"
        description={
          <>
            <p>
              重启期间依赖公网的扫码、下载页会<strong>短暂中断</strong>， 通常在几秒内自动恢复。
            </p>
            <p className="settings-hint">本机已下载的安装包不受影响。</p>
          </>
        }
        confirmText="重启"
        loading={restarting}
        onConfirm={() => void runRestart()}
        onCancel={() => setConfirmOpen(false)}
      />
    </div>
  );
}
