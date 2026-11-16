/**
 * 多选关联选择器：人员表单选团队/项目、团队表单选人员/项目都用它。
 * 职责：把「可选项 + 已选项」渲染成可点选的标签，回调完整的新选择结果。
 * 输入：label、options（id + 名称）、selected（ID 数组）；输出：onChange(新数组)。
 * 边界：不取数、不校验、不排序；选项为空时给一句可读的提示而不是空白。
 */
import { Checkbox } from './Controls';

export interface MultiSelectOption {
  id: string;
  label: string;
}

interface MultiSelectProps {
  label: string;
  options: ReadonlyArray<MultiSelectOption>;
  selected: ReadonlyArray<string>;
  onChange: (next: string[]) => void;
  emptyText?: string;
}

export function MultiSelect({
  label,
  options,
  selected,
  onChange,
  emptyText,
}: MultiSelectProps): JSX.Element {
  const toggle = (id: string): void => {
    onChange(selected.includes(id) ? selected.filter((item) => item !== id) : [...selected, id]);
  };

  return (
    <div className="field-block">
      <span className="field-label">{label}</span>
      {options.length === 0 ? (
        <p className="settings-hint">{emptyText ?? '暂无可选项'}</p>
      ) : (
        <div className="field-picker">
          {options.map((option) => {
            const checked = selected.includes(option.id);
            return (
              <Checkbox
                key={option.id}
                className={checked ? 'field-check field-check-on' : 'field-check'}
                checked={checked}
                onChange={() => toggle(option.id)}
              >
                {option.label}
              </Checkbox>
            );
          })}
        </div>
      )}
    </div>
  );
}
