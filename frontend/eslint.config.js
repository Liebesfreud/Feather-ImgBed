import eslint from '@eslint/js'
import vue from 'eslint-plugin-vue'
import globals from 'globals'
import tseslint from 'typescript-eslint'

export default [
  {
    ignores: [
      'node_modules/**',
      'playwright-report/**',
      'test-results/**',
      '../internal/app/web/dist/**',
    ],
  },
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  ...vue.configs['flat/essential'],
  {
    files: ['**/*.{ts,vue}'],
    languageOptions: {
      globals: {
        ...globals.browser,
        ...globals.node,
      },
    },
    rules: {
      'vue/multi-word-component-names': 'off',
    },
  },
  {
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: {
        parser: tseslint.parser,
      },
    },
  },
]
