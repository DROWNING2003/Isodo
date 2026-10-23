---
name: git-commit
description: View modified contents, generate a git commit message based on the diff, and commit the changes.
---

# Git 提交技能 (Git Commit Skill)

仅在用户明确要求提交时才执行。流程如下：

## 1. 并行审查变更
在同一次工具调用里并行执行（互不依赖）：
- `git status`（**不要**使用 `-uall`，大仓库会爆内存）
- `git diff`（未暂存）
- `git diff --staged`（已暂存）
- `git log --oneline -10`（学习本仓库的提交风格）

分析 diff，明确变更的**意图**与范围（feat / fix / refactor / docs / chore / test / style）。

## 2. 暂存变更
- **优先**按文件名显式 `git add <files>`，避免把无关或敏感文件（`.env`、`*.key`、`credentials.json`、大型二进制）一起带入。
- 仅在用户明确说"提交所有变更"时才使用 `git add -A` / `git add .`，且在执行前确认 `git status` 中没有可疑文件。
- 若发现疑似密钥/凭证类文件，**先停下来提醒用户**，不要自行提交。

## 3. 撰写提交信息
遵循 **Conventional Commits**，并对齐本仓库已有风格（参考第 1 步的 `git log`）：

- **语言**：默认使用**英文**撰写 commit message（标题与 body）。仅当用户明确要求使用中文（或其他语言）时才切换。
- **格式**：`<type>(<scope>): <description>`
- **type**：`feat` `fix` `docs` `style` `refactor` `perf` `test` `chore` `build` `ci`
- **scope**：受影响的包/模块，如 `api`、`orchestrator`、`db`、`shared`、`templates`、`docs`
- **description**：祈使语气、小写开头、不加句号，控制在 ~72 字以内
- **body（可选）**：解释**为什么**这样改（动机、约束、与旧行为的差异），而不是复述 diff

## 4. 执行提交
对于多行信息**必须**使用 HEREDOC 以保证格式（避免多个 `-m` 拼接或换行被吞）：

```bash
git commit -m "$(cat <<'EOF'
feat(scope): short description

Longer motivation and background go here.
EOF
)"
```

单行信息可直接 `git commit -m "type(scope): description"`。

## 5. 安全红线（不要触碰）
- **绝不**使用 `--amend` 修改已有提交（除非用户明确要求）；预提交钩子失败时，修复后**新建**提交，而不是 amend。
- **绝不**使用 `--no-verify` / `--no-gpg-sign` 跳过钩子或签名（除非用户明确要求）。
- **绝不**修改 `git config`，**绝不**未经允许执行破坏性操作（`reset --hard`、`push --force`、`clean -f`、`branch -D` 等）。
- **绝不**主动 `git push`，除非用户明确要求。
- 若无任何变更（无 untracked、无 modified），不要创建空提交，直接告知用户。

## 6. 汇报
提交后用一两句话告诉用户：提交了哪些文件、提交信息是什么。不要复述 diff。
