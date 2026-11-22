module.exports = {
  extends: ["@commitlint/config-conventional"],
  rules: {
    "type-enum": [
      2,
      "always",
      [
        "fix",
        "build",
        "revert",
        "wip",
        "feat",
        "chore",
        "ci",
        "docs",
        "style",
        "refactor",
        "perf",
        "test",
        "instr",
        "deps"
      ],
    ],
    "scope-enum": [
      2,
      "always",
      [],
    ],
  },
};
