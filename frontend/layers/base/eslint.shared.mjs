// Flat ESLint config (build-phases.txt 3i-c: `pnpm lint`). Deliberately the
// stock recommended sets - eslint, typescript-eslint, eslint-plugin-vue - with
// no-undef off (typescript-eslint's own advice: the type checker does it, and
// Nuxt auto-imports ref/computed/useApi/... so a name-list would rot) - `pnpm
// typecheck` is what catches an undefined name. No style opinions here.
import js from "@eslint/js";
import tseslint from "typescript-eslint";
import vue from "eslint-plugin-vue";
import globals from "globals";

export default tseslint.config(
  { ignores: [".nuxt/**", ".output/**", "node_modules/**", "coverage/**", "public/**"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...vue.configs["flat/recommended"],
  {
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
      parserOptions: { parser: tseslint.parser },
    },
    rules: {
      "no-undef": "off",
      // Style is not this config's job (the repo has no formatter wired in).
      "vue/max-attributes-per-line": "off",
      "vue/singleline-html-element-content-newline": "off",
      "vue/multiline-html-element-content-newline": "off",
      "vue/html-self-closing": "off",
      "vue/html-indent": "off",
      "vue/html-closing-bracket-newline": "off",
      "vue/first-attribute-linebreak": "off",
      "vue/attributes-order": "off",
      "vue/multi-word-component-names": "off",
      "vue/require-default-prop": "off",
      "vue/block-lang": "off",
      "@typescript-eslint/no-explicit-any": "warn",
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_", caughtErrors: "none" }],
    },
  },
);
