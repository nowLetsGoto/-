/**
 * 文件选择区：拖拽 / 点击 / 回显 / 移除 / 扩展名预检 / 平台预选。
 * 职责：把合法的 .apk / .ipa 交给页面，页面据此触发 stage。
 * 输入：当前 file、禁用态、onSelect / onRemove 回调。
 * 输出：交互区域 JSX；不发起任何请求。
 * 边界：只做前端类型预检，大小与内容合法性由服务端判定；组件不 import 任何 API 模块。
 */
import { useRef, useState } from 'react';
import type { DragEvent } from 'react';
import type { Platform } from '../api/builds';
import { acceptFile, formatBytes, platformFromFileName } from '../utils/format';

interface FileDropzoneProps {
  file: File | null;
  disabled?: boolean;
  onSelect: (file: File, platformHint: Platform | null) => void;
  onRemove: () => void;
}

export function FileDropzone({ file, disabled = false, onSelect, onRemove }: FileDropzoneProps): JSX.Element {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const [error, setError] = useState('');

  const handleFile = (candidate: File | undefined): void => {
    if (!candidate) {
      return;
    }
    if (!acceptFile(candidate.name)) {
      setError('请选择 .apk 或 .ipa 文件');
      return;
    }
    setError('');
    onSelect(candidate, platformFromFileName(candidate.name));
  };

  const onDrop = (event: DragEvent<HTMLDivElement>): void => {
    event.preventDefault();
    setDragging(false);
    if (!disabled) {
      handleFile(event.dataTransfer.files[0]);
    }
  };

  const clear = (): void => {
    setError('');
    if (inputRef.current) {
      inputRef.current.value = '';
    }
    onRemove();
  };

  return (
    <div className="dropzone-wrap">
      <div
        className={`dropzone${dragging ? ' dropzone-active' : ''}${disabled ? ' dropzone-disabled' : ''}`}
        onDragOver={(event) => {
          event.preventDefault();
          if (!disabled) {
            setDragging(true);
          }
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
        onClick={() => {
          if (!disabled) {
            inputRef.current?.click();
          }
        }}
        role="button"
        tabIndex={0}
        onKeyDown={(event) => {
          if ((event.key === 'Enter' || event.key === ' ') && !disabled) {
            inputRef.current?.click();
          }
        }}
      >
        <input
          ref={inputRef}
          type="file"
          accept=".apk,.ipa"
          hidden
          disabled={disabled}
          onChange={(event) => handleFile(event.target.files?.[0])}
        />
        <p className="dropzone-title">拖拽 .apk / .ipa 到此处，或点击选择文件</p>
        <p className="dropzone-hint">单个包上限 200 MB，选文件后会自动识别包名与版本号</p>
      </div>

      {file ? (
        <div className="selected-file">
          <div>
            <strong>{file.name}</strong>
            <span className="muted"> · {formatBytes(file.size)}</span>
          </div>
          <button className="button button-ghost button-small" type="button" onClick={clear} disabled={disabled}>
            移除
          </button>
        </div>
      ) : null}
      {error ? <p className="field-error">{error}</p> : null}
    </div>
  );
}
