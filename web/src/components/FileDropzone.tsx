/**
 * 文件选择区：拖拽 / 点击 / 回显 / 移除 / 扩展名预检 / 平台预选。
 * 职责：把合法的 .apk / .ipa / .hap 交给页面，页面据此触发 stage。
 * 输入：当前 file、禁用态、onSelect / onRemove 回调。
 * 输出：交互区域 JSX；不发起任何请求。
 * 边界：只做前端类型预检，大小与内容合法性由服务端判定；组件不 import 任何 API 模块。
 *       Upload 仅负责选文件；合法文件返回 false、非法文件返回 LIST_IGNORE，禁止库自动上传。
 */
import { useState } from 'react';
import { Upload } from '@seakoi/console-kit';
import { Button } from './Controls';
import type { Platform } from '../api/builds';
import { acceptFile, formatBytes, platformFromFileName } from '../utils/format';

interface FileDropzoneProps {
  file: File | null;
  disabled?: boolean;
  onSelect: (file: File, platformHint: Platform | null) => void;
  onRemove: () => void;
}

export function FileDropzone({
  file,
  disabled = false,
  onSelect,
  onRemove,
}: FileDropzoneProps): JSX.Element {
  const [error, setError] = useState('');

  const handleFile = (candidate: File): false | typeof Upload.LIST_IGNORE => {
    if (!acceptFile(candidate.name)) {
      setError('请选择受支持的安装包（.apk / .ipa / .hap）');
      return Upload.LIST_IGNORE;
    }
    setError('');
    onSelect(candidate, platformFromFileName(candidate.name));
    return false;
  };

  const clear = (): void => {
    setError('');
    onRemove();
  };

  return (
    <div className="dropzone-wrap">
      <Upload.Dragger
        className="package-dropzone"
        accept=".apk,.ipa,.hap"
        multiple={false}
        maxCount={1}
        disabled={disabled}
        showUploadList={false}
        beforeUpload={handleFile}
        aria-label="选择安装包"
      >
        <p className="dropzone-title">拖拽 .apk / .ipa / .hap 到此处，或点击选择文件</p>
        <p className="dropzone-hint">单个包上限 200 MB，选文件后会自动识别包名与版本号</p>
      </Upload.Dragger>

      {file ? (
        <div className="selected-file">
          <div>
            <strong>{file.name}</strong>
            <span className="muted"> · {formatBytes(file.size)}</span>
          </div>
          <Button
            className="button button-ghost button-small"
            type="button"
            onClick={clear}
            disabled={disabled}
          >
            移除
          </Button>
        </div>
      ) : null}
      {error ? <p className="field-error">{error}</p> : null}
    </div>
  );
}
