# Delivery: branch → PR → linked issue

How agents land work that **implements or closes** a solving issue (bug, feature, or task with code/config impact).

This is separate from [issue-tracker.md](./issue-tracker.md) (tickets, wayfinder, triage). Do not commit solving-issue work directly to `main` / the default branch.

## When this applies

**Mandatory** for any work that implements or closes a solving issue.

**Carve-outs:**

- Pure discussion or research that does not change the repo
- Tiny fixes with **no** linked issue, only when a human explicitly says to proceed without an issue

## Cardinality

**One solving issue → one branch → one PR.**

- Incidental work required to finish that issue stays in the same PR (still one issue).
- A distinct second solving issue gets its own issue (if needed), branch, and PR. Do not silently expand scope.

## Starting gate

Before the first edit:

1. Have a concrete issue number (from the human, or by creating/finding the issue first).
2. If there is no issue yet and the change is a solving change, **create the issue first**, then branch — do not code on `main` and invent the ticket later.
3. Claim the issue: `gh issue edit <n> --add-assignee @me`.
4. Create the branch from an up-to-date default branch (`main` unless the repo default differs):

   `issue-<n>-<short-slug>`

   - `<n>` is required and must match the linked issue
   - `<short-slug>` is lowercase kebab-case from the issue title (a few words)

## Landing the work

1. Implement and commit on the issue branch only. Every commit uses [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) — see [Commits](#commits).
2. When the work is ready for review, **push and open a pull request** — that is part of finishing solving-issue work (no extra “please make a PR”). Still **do not merge** unless asked.
3. PR body **must** include a closing keyword that links the issue, e.g. `Fixes #<n>` or `Closes #<n>`. That is the canonical link (GitHub auto-closes on merge).
4. Mentions of `#n` in commit messages are optional once the PR body is correctly linked.
5. Open a **ready-for-review** (non-draft) PR when the agent believes the work is complete and locally runnable checks have been run. Use **draft** only if the human asked for WIP delivery, or the agent hit a blocker and needs eyes before finishing.

Use `gh pr create` with a heredoc body that includes the closing keyword. Infer the repo from `git remote` / cwd as with other `gh` operations.

## Commits

Always use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).

```
<type>[optional scope][optional !]: <description>
```

- Description is imperative, lowercase, no trailing period: `docs: require conventional commits`, not `Updated delivery.md.`
- Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`, `ci`, `build`, `revert`
- Breaking change: `!` after type/scope and/or a `BREAKING CHANGE:` footer
- Issue numbers belong in the PR body (`Fixes #<n>`), not in the subject unless the human asks

## Human override

If a human says to work on `main` or skip the PR for a solving issue, **follow the human for that session**, but state the policy conflict in one line before proceeding. Silent drift without that acknowledgment is not allowed. Default when there is no override remains branch → PR → `Fixes #<n>`.

## Mid-flight recovery

If work for `#n` was already edited or committed on `main`:

1. Stop landing further commits on `main`.
2. Move the changes onto `issue-<n>-…` (stash, branch from current HEAD, or cherry-pick — whichever preserves the work safely).
3. Open the linked PR from that branch.
4. If commits already reached `main` / `origin/main`, tell the human and ask how to unwind — do not silently rewrite shared history.

## Scope creep

- Needed to finish the current issue → keep it in the current PR.
- Distinct other solving issue → new issue (if needed), new branch, new PR. If the current PR is blocked without that other work, say so on the PR/issue and wait for human direction.
