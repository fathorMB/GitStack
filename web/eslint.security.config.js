// Config ESLint dedicata all'analisi statica di sicurezza (GIT-114): separata
// da eslint.config.js perché `pnpm run lint` (CI "ts") non deve cambiare. Gira
// con `pnpm run lint:security`; il gate di CI (.github/workflows/security.yml,
// scripts/security/gate.mjs) la legge in formato JSON.
//
// Le regole di eslint-plugin-security stanno a "warn" (gravità MEDIUM nel
// gate, mai bloccanti). Quelle con segnale alto, più le regole core contro
// l'esecuzione di codice dinamico, sono "error" (gravità HIGH: bloccano solo
// sui tag di release, e solo se non coperte da un'eccezione).
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import security from 'eslint-plugin-security';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  { ignores: ['dist/**', 'node_modules/**', 'src/**/*.test.{ts,tsx}', 'src/test/**'] },
  {
    files: ['src/**/*.{ts,tsx}'],
    extends: [security.configs.recommended],
    languageOptions: { parser: tseslint.parser },
    // Plugin registrati senza regole attive, solo perché i commenti
    // `eslint-disable` del codice che li nominano non diano "definition not found".
    plugins: { 'react-hooks': reactHooks, 'react-refresh': reactRefresh },
    linterOptions: { reportUnusedDisableDirectives: 'off' },
    rules: {
      'security/detect-eval-with-expression': 'error',
      'security/detect-unsafe-regex': 'error',
      'security/detect-child-process': 'error',
      'security/detect-non-literal-require': 'error',
      'security/detect-bidi-characters': 'error',
      'no-eval': 'error',
      'no-implied-eval': 'error',
      'no-new-func': 'error',
    },
  },
);
