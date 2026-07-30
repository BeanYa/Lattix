# Repository Instructions

## Git Worktrees

- Create manually managed worktrees under `<repo>/.worktree/<name>`.
- Do not create new worktrees as sibling directories of the repository.
- Keep `/.worktree/` ignored by Git.

## Infrastructure Reuse

- Use the existing `requester` as the request client for all request-related code.
- Reuse existing project infrastructure wherever possible before introducing new implementations, helpers, or dependencies.

## State Machines

- `panel` and `agent` each have their own state machine.
- For changes involving state transitions or state actions, prefer reusing or extending the corresponding state machine so the `agent` and `panel` state-machine patterns remain coherent and complete.
- When necessary, it is acceptable to bypass an existing state machine or introduce an additional state machine, provided the overall state-management design remains clear and consistent with the existing patterns.
