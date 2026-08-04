---
name: commit-push
description: Use when committing and pushing local changes, bumping the version tag, or when a GitHub Actions build failed after a push. Triggers: dirty working tree, unmerged branches or worktrees to reconcile, files to stage, new version tag needed, build failure to fix.
---

# Commit-Push

## Overview

Commit everything, tag the latest commit with the next patch version, push, and keep pushing fixes until the GitHub Actions build passes. The tag always points at the commit that produced a green build.

## Workflow

### 1. Check for unmerged branches/worktrees

- Run `git branch --no-merged main` for unmerged branches, and `git worktree list` for worktrees; check each worktree's branch against the same unmerged list.
- If any exist, **ask the user whether to merge them**. If yes: `git checkout main && git merge <branch>` (resolve conflicts if any), then push main before continuing.
- Never merge without asking. If the user declines, leave them untouched.

### 2. Drain the working tree

- Run `git status --porcelain` to detect staged, unstaged, and untracked files.
- If non-empty: review the diff (`git diff`, `git diff --cached`, and untracked contents), group changes logically, and commit with a descriptive Conventional Commits message.
- Repeat until `git status --porcelain` returns nothing. Never tag or push with a dirty tree.

### 3. Bump the version tag

- Find the latest tag: `git tag -l 'v*' --sort=-v:refname | head -n 1`.
- If none exists, start at `v0.0.0`.
- **Milestone release** (explicitly indicated by the user): bump minor, reset patch: `vX.Y.Z` -> `vX.Y+1.0`.
- Otherwise bump only the patch segment: `vX.Y.Z` -> `vX.Y.Z+1` (never major).
- Tag HEAD: `git tag vX.Y.Z`.

### 4. Push

- `git push` and `git push origin vX.Y.Z` (plain `git push` does not push tags).

### 5. Wait for the build

- Find the run for the pushed commit: `gh run list --commit <sha> --limit 1`.
- Watch it: `gh run watch <run-id> --exit-status` (non-zero exit means failure).
- Success -> report and stop.

### 6. Fix on failure (loop)

- Pull failed logs: `gh run view <run-id> --log-failed`, diagnose the failure, fix the code.
- Commit the fix (workflow step 1), then **move the tag to the fixed commit**:
  - `git tag -f vX.Y.Z <fixed-sha>` (or HEAD after the fix commit)
  - `git push origin :refs/tags/vX.Y.Z && git push origin vX.Y.Z` (or `git push --force origin vX.Y.Z`)
- Push commits, then repeat step 5. Loop until the build passes.

## Common Mistakes

| Mistake | Fix |
|---------|-----|
| Merging branches without asking | Ask first; only merge on confirmation (step 1) |
| Missing worktree branches | Check each worktree's branch in `git worktree list` too |
| Tagging while files remain uncommitted | Drain the tree first (step 2) |
| Forgetting untracked files | `git status --porcelain` includes `??` entries — stage them |
| Bumping major/minor | Only patch: `vX.Y.Z` -> `vX.Y.Z+1`; minor only for explicitly marked milestone releases (`vX.Y+1.0`) |
| Milestone release got patch bump | User said milestone -> bump minor and reset patch: `vX.Y+1.0` |
| Tag not pushed | `git push origin vX.Y.Z` explicitly |
| Watching an unrelated run | Filter by the pushed commit sha |
| Old tag left on failed commit | Force-move the tag before the next build |
| Generic commit messages | Message must describe the actual diff (Conventional Commits) |
