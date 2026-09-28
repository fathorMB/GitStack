// Config ESLint (flat config) minima per lo scheletro di web/. Il progetto
// React/Vite vero e proprio, con le regole per JSX/hooks, arriva con
// M-01 T-07 (vedi web/README.md).
import js from '@eslint/js';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  {
    ignores: ['dist/**', 'node_modules/**'],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
);
