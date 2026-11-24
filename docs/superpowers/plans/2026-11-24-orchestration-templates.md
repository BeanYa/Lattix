# Orchestration Templates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Named shared orchestration templates with inherited instance parameters, independent copies, and orchestration during chain creation.
**Architecture:** Persist templates and per-hop bindings while preserving existing runnable hop graphs. Reuse graph validation, chain workers and revision deployments; reuse the orchestration canvas for template, draft and runtime modes. Final chain requests persist topology plus graphs/bindings atomically.
**Tech Stack:** Go/SQLite, React/TypeScript, existing requester and Vitest; no new dependencies.
**Spec:** `docs/superpowers/specs/2026-11-24-orchestration-templates-design.md` (approved in chat).

## Global Constraints

- Reuse requester for every request, the existing orchestration canvas/styles/graph validation, and the panel chain state machine/revision deployment infrastructure.
- Shared template edits propagate only to fields without explicit instance overrides. Explicit address="" and listen_port=0 are valid overrides. Template port0 means auto; numeric defaults follow into instances.
- Independent copies are materialized resolved graphs with no template binding. Do not silently detach on synchronization failure.
- Create/edit topology and submitted graph/binding updates are transactional. Legacy request omission preserves behavior; explicit empty graph clears.
- Reject incompatible shared-template updates as a whole, with affected chain reasons. Preserve current active servers and per-chain deployment serialization; durable synchronization resumes after restart.
- Preserve b5370d8 Agent updating-state and live-Xray behavior: updating is not an ordinary outage and must not spuriously fail template synchronization.
- Do not push/publish. Only this isolated branch is writable. Acceptance screenshots are local and embedded in final report, never committed.

## Review Focus

- Template changes while a chain is deploying or an Agent is updating must defer/reject safely without corrupting revisions or reporting a false outage.
- Field absence versus explicit 0/empty-string overrides must survive save/reload and later template edits.
- Active candidate removal, missing servers, invalid role-boundary triggers and cross-hop candidate reuse must fail before any partial save.
- Reordered/branched canvas paths must map drafts to the correct final seq; deleting/replacing nodes must not retain unrelated graphs.
- Existing runtime editing, template inheritance and detach/cancel paths must preserve unsaved user work and distinguish instance edits from shared changes.

---

### Task 1: Backend template persistence, binding/synchronization, chain request contracts

**Files:** Create focused template store/panel/test files under `src/backend/internal/{store,panel}`; modify existing store schema/migrations/revision/chain-graph code, panel graph validation/chains/orchestrator, and necessary dispatcher integration. Modify `docs/openapi.yaml`, `src/frontend/src/lib/{types.ts,api.ts,api-contract.generated.ts}` for the public contract. Avoid unrelated frontend UI edits.

**Interfaces produced:**
- `OrchestrationGraph`: `{nodes: HopOrchestrationNodeInput[]; edges: {from:number;to:number}[]}` (edges index nodes).
- `OrchestrationParamOverride`: `{server_id:number;address?:string;listen_port?:number}`. Missing field=inherit; 0/""=explicit.
- `OrchestrationDraft extends OrchestrationGraph`: optional `template_id`, `template_revision`, `overrides: OrchestrationParamOverride[]`.
- `OrchestrationTemplate extends OrchestrationGraph`: `{id:number;name:string;revision:number;usage_count:number;created_at:string;updated_at:string}`.
- `OrchestrationTemplateImpact`: `{chain_id:number;chain_name:string;seq:number;redeploy:boolean}`.
- `CreateChainRequest` and `EditChainRequest`: optional `orchestrations: Array<OrchestrationDraft & {seq:number}>`.
- `ChainOrchestration`: optional `template_id`, `template_name`, acknowledged `template_revision`, materialized `template_source_revision`, `overrides`, `template_sync_pending` in addition to existing runtime fields.
- `api.orchestrationTemplates(options?) -> Promise<OrchestrationTemplate[]>`: GET `/api/chain/orchestration-template/list`.
- `api.previewOrchestrationTemplate(body) -> Promise<{revision:number;preview_proof:string;affected_chains:OrchestrationTemplateImpact[]}>`: POST `/api/chain/orchestration-template/preview`.
- `api.saveOrchestrationTemplate(body) -> Promise<OrchestrationTemplate>`: POST `/api/chain/orchestration-template/save`. Body is graph + `{id?:number;name:string;revision?:number;preview_proof?:string}`. Existing-template saves require both the frozen edit revision and a save-preview proof; return conflict before mutation if either is stale or missing.
- `api.deleteOrchestrationTemplate({id,revision,preview_proof}) -> Promise<void>`: POST `/api/chain/orchestration-template/delete` with the revision and proof returned by `preview({id,operation:"delete"})`. Atomically detach existing materialized graphs; deletion preview bypasses graph/update readiness validation.

**Implementation:**
- [ ] Write focused failing store/handler tests for shared propagation, explicit port0/address"" overrides, detach then edit, stale revision, template deletion preserving graph, and rejection without partial writes.
- [ ] Run tests and confirm behavioral failure before implementation.
- [ ] Persist versioned templates/bindings and refactor transaction-internal graph writes. Share existing graph shape validation; role/boundary/cross-hop checks run at binding time and across all affected instances.
- [ ] Extend initial deployment and revision topology replacement transactions to apply optional graph/binding payloads, preserving absent-vs-clear behavior. Validate before producing deployment side effects.
- [ ] Implement durable per-chain synchronization through the existing worker and same-server target reconfiguration through the existing planner/revision/FSM path; do not emit fake A→A failover events. Mark synchronized only once the matching revision is published. Defer while Agent IsUpdating; preserve pending state and normal retry/failure paths.
- [ ] Add regression tests for same-server active-port redeployment, no-op candidate-only update, restart/pending sync, deploying-chain rejection, current-candidate removal and planned Agent update. Use actual store and dispatcher with existing requester fakes.
- [ ] Register RPCs with existing auth/CSRF/idempotency policies; update OpenAPI, public frontend types and requester-backed API methods; regenerate API types.
- [ ] Run `GOTOOLCHAIN=go1.26.11 go test ./src/backend/... ./src/agent/... ./src/shared/...`, `go vet` with same toolchain, frontend `bun run check:api` and `bunx tsc -b`.
- [ ] Commit only Task1 files, self-review and record TDD/commands/results in task report.

### Task 2: Reusable draft/template editor and template library

**Files:** Existing orchestration components/utilities and tests; create `src/frontend/src/pages/chains/orchestration/OrchestrationTemplateEditor.tsx`, `DraftOrchestrationEditor.tsx`, `OrchestrationTemplates.tsx`, `orchestration-draft.ts` (and meaningful tests). Modify `src/frontend/src/pages/Chains.tsx` only to append the manager; reuse CSS/token conventions and preserve updating indicators.

**Consumes:** Task1 types and API.
**Produces:**
- `draftFromTemplate(template:OrchestrationTemplate):OrchestrationDraft` (default linked, clone, no aliasing).
- `draftFromHop(hop:ChainHop):OrchestrationDraft` (runtime node IDs converted to input indexes, preserves bindings/overrides).
- `resolveOrchestrationDraft(draft:OrchestrationDraft, template?:OrchestrationTemplate):OrchestrationGraph` (template then overrides, explicit0 respected).
- `draftOrchestrationDTO(draft:OrchestrationDraft, servers:Server[], activeServerId:number, template?:OrchestrationTemplate):ChainOrchestration` for canvas display.
- `DraftOrchestrationEditor` props: `chain:Chain`, `hop:ChainHop`, `value?:OrchestrationDraft`, `templates:OrchestrationTemplate[]`, `servers:Server[]`, optional `probesByServer`, `probeTitle`, `onClose:()=>void`, `onApply:(value:OrchestrationDraft)=>void`, optional `onTemplatesChanged:()=>void`.
- `OrchestrationTemplateEditor` props: optional `template:OrchestrationTemplate`, `servers:Server[]`, optional probes/title, `onClose`, `onSaved:(template:OrchestrationTemplate)=>void`.
- `OrchestrationTemplates` optional servers/probes; standalone page section that fetches data if not provided.

**Implementation:**
- [ ] Write failing tests for empty template root selection, parameter defaults0/numeric, resolution/copy isolation, explicit0 override persistence/reset, detach, and draft mode applying without chain/network save.
- [ ] Reuse/extract a shared editor core from the current runtime editor. Existing `OrchestrationEditor` runtime public props and polling remain compatible. Draft/template modes neither poll chains nor invoke saved-hop save RPCs.
- [ ] Template mode starts empty; root selection creates the main node, blank address placeholder and port0. Addresses are instance-only. Preserve graph actions/layout styling. Naming, save-as-new, stale/error handling and affected-chain preview use the new RPCs.
- [ ] Shared instance mode permits address/port overrides and reset-to-template; shared structural edits use an explicitly labelled shared-template editor. “另存为独立副本” materializes and detaches to enable local structure editing. Cancel retains no persisted draft effects.
- [ ] Add template management section below chain content, listing main server/counts/usage and create/edit/rename/copy/delete; confirm the concrete affected-chain preview before shared save or detach-on-delete.
- [ ] Run focused tests then complete `bun run test`, `bun run lint`, `bun run check:api`, `bunx tsc -b` and Prettier on changed files.
- [ ] Commit only Task2 files; report exact component/helper signatures and tests to Task3.

### Task 3: Wizard template tab, drag/drop, local orchestration drafts and atomic submission

**Files:** `chain-wizard/{ChainWizardPage,CanvasServerPanel,ChainCanvas,chain-canvas,chain-canvas-state,use-chain-canvas}` and relevant tests/CSS; `use-chain-form.ts` and tests. Avoid changes to Task1 public contracts unless an integration defect is reported.

**Consumes:** Tasks1/2 contracts. Extend `CanvasNode` with optional `orchestration:OrchestrationDraft`. Extend reducer/controller for template-add/apply and per-node draft updates.

**Implementation:**
- [ ] Write failing reducer/form/UI tests: create-time orchestration click enabled; template click/drop adds main plus graph metadata; move preserves/remove deletes/replace clears; multi-path seq binding; submitted creation/edit payload includes correct overrides and independent graphs; legacy no-draft request remains unchanged.
- [ ] Add server/template tabs, template search/name/count/usage cards, click and drag MIME `text/x-lattix-orchestration-template`; canvas handles server MIME and template MIME with current world coordinates. Repeated roots apply to existing node only after intentional replacement confirmation; reject cross-hop candidate overlap with an actionable message.
- [ ] Enable orchestration entry for every actual canvas server during create/edit. Build synthetic chain/hop DTO from current canvas path for DraftOrchestrationEditor; do not require existing chain ID. Track drafts on canvas nodes and preserve parameter override provenance separately from auto-allocated runtime port values.
- [ ] Use Task2 helpers to resolve/display grouped graphs and pass current servers, templates and probes. Apply updates to canvas node address/port and draft through reducer.
- [ ] Prefill persisted graphs/bindings from Chain DTO on edit. Reorder/branch safely; serialize drafts by each final path's seq in useChainForm. Existing no-orchestration callers remain compatible.
- [ ] Ensure instance override editing outside orchestration editor (main node port/address) is included in overrides; restoring inheritance is explicit, not value guessing. Validate all paths before submitting.
- [ ] Run complete frontend tests, format/lint/build, and API contract check. Commit Task3 files; report browser entry points for acceptance.

### Final integration and acceptance

- [ ] Fresh whole-branch review against approved spec, preserving Agent updating semantics from b5370d8. Address substantive findings with focused RED→GREEN tests.
- [ ] Run all frontend checks/build and Go tests/vet/race with CI toolchain; run govulncheck for all modules using CI toolchain (do not rely on the host's newer default).
- [ ] Browser acceptance: empty-root creation, named template with port0 and concrete port, drag/click into unsaved chain, overrides/shared propagation/reset, independent copy, error/invalid save, reload, desktop/narrow layouts. Use actual UI screenshots, not render-only claims.
- [ ] Keep screenshots untracked/ignored and embed the relevant actual captures in final report. Leave feature branch reviewable without publishing or touching unrelated worktrees.
