/**
 * eslint.config.js · 前端 ESLint flat config。
 * 职责：对 TS/TSX 源码做静态检查；格式类规则交给 Prettier，本文件不重复。
 * 输入：web/src 下的 TS/TSX 源码、vite.config.ts。
 * 输出：npm run lint 的检查结果。
 * 边界：不检查生成物 schema.d.ts 与构建产物；不做格式化。
 */
import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import globals from 'globals';
import prettier from 'eslint-config-prettier';

export default tseslint.config(
  // 生成物与构建产物不进检查范围。
  { ignores: ['src/api/schema.d.ts', 'dist/**', 'coverage/**'] },

  // JS 基础规则 + TS 推荐规则。
  js.configs.recommended,
  ...tseslint.configs.recommended,

  // 浏览器全局与 React 生态规则。
  {
    files: ['src/**/*.{ts,tsx}'],
    languageOptions: {
      globals: { ...globals.browser },
    },
    rules: {
      // TS 自己会查未定义标识符，避免与 no-undef 重复误报。
      'no-undef': 'off',
      // 现有代码在 state 初始化处常用 `'' as ''` 固定字面量类型，不强制改 `as const`。
      '@typescript-eslint/prefer-as-const': 'off',
    },
  },
  ...reactHooks.configs['flat/recommended'],

  // 只约束 TSX 的组件导出；允许同文件导出常量，避免与现有写法冲突。
  {
    files: ['src/**/*.tsx'],
    plugins: { 'react-refresh': reactRefresh },
    rules: {
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
    },
  },

  // 关闭所有与 Prettier 冲突的格式规则，必须放最后。
  prettier,
);
