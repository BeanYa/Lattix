# Chain Canvas Rebuild Implementation Plan

> Implement inline using executing-plans, TDD and a final independent code review.

**Goal:** Rebuild the topology editor interactions while retaining the accepted server and terminal design.
**Architecture:** Explicit ingress/server/Internet edges in one reducer; pure graph traversal and validation; independent camera transform for pan/zoom. Existing React, requester, UI controls and form submission contracts remain in use. No new dependency.
**Tech Stack:** React / TypeScript / SVG / Pointer Events / Vitest.
**Spec:** User request and screenshots in this chat: free viewport dragging; manipulating HK's outgoing connection must preserve ingress; rebuild functionality with current visual design.

## Behavior

- Ingress and Internet are first-class terminal endpoints, excluded from four-server hop limit. The ingress connection is stored, never inferred from dangling servers.
- Empty canvas and reset explicitly store ingress → Internet. Removing the final server returns to this empty state. First server creates ingress → server → Internet. Additional servers are initially unconnected. All ports, including ingress, support click source then click target.
- Clicking an output starts a connection/reconnection. Existing edge remains until a valid replacement is committed. Escape, background click or same-source click cancels without changing edges. Explicit edge action disconnects only that edge.
- Each output and input accepts one edge. Reject unknown endpoints, occupied targets, ingress → Internet with servers present, self-connections, and cycles, with feedback and no graph mutation.
- Free background dragging pans the camera; wheel pans, Ctrl/Meta-wheel and controls zoom around a stable point. Node dragging remains independent. Coordinates may be negative; no fixed world boundary or world scrollbars.
- Both terminals can move. Resize preserves scene positions and camera. Fit view and zoom controls recover an off-screen scene.
- Drops convert viewport to world using camera offset and zoom. Plus-add uses the current visible scene. Card controls and portaled Select options never initiate pan/drag.
- Roles derive from the explicit ingress path. Removing a node deletes only its adjacent edges. Reset and edit prefill are atomic, and prefill includes ingress.
- Existing address/port editing, topology form mapping, NAT validation, duplicate-server validation, and submit payload remain intact.

## Tasks

### 1. Explicit graph and connection reducer
Files: chain-canvas.ts, chain-canvas-state.ts, use-chain-canvas.ts and tests.
- [x] Add failing tests for retained ingress during outgoing disconnection/reconnection, cancellation, invalid targets, cycles, occupied inputs, remove/reset/prefill, rapid adds, and roles in partial graphs.
- [x] Replace inferred traversal with ingress-rooted traversal; keep form mapping and server validation.
- [x] Implement atomic reducer transitions and adapt controller.
- [x] Run graph and controller tests.

### 2. Camera and interactions
Files: canvas-viewport.ts, use-canvas-viewport.ts, ChainCanvas.tsx, CanvasTerminal.tsx, ChainNodeCard.tsx, ChainWizardPage.tsx, chain-wizard.css and tests.
- [x] Add failing camera conversion, zoom anchoring, fit and DOM interaction regressions.
- [x] Replace scroll-bound world with transformed scene and shared coordinate conversion.
- [x] Add background pan, wheel/zoom, draggable terminals, fit and reset controls, safe event routing, and drop handling.
- [x] Preserve full-card native drag preview and existing design.

### 3. Acceptance
- [x] Run full frontend tests, lint, formatting, build and independent review.
- [x] Verify actual native drag, background pan, output reconnect with retained ingress, cancellation, explicit disconnect, address/port editing, zoom/drop and fit in browser.
- [x] Capture and embed fresh screenshots; keep screenshots outside Git. Leave changes uncommitted.

## Review focus
- Disconnected fragments cannot steal entry role or restore a deleted ingress edge.
- Cancelled/invalid rewires never delete any edge.
- Pan/zoom cannot alter topology or shift coordinates on viewport resize.
- Controls and portaled options remain interactive; pointer capture is released on cancellation.
- Four-hop, duplicate-server and NAT constraints still gate form progression.

## Acceptance record

- 2026-10-30: Full frontend suite 197/197; oxlint, Prettier and production build passed. Existing >500 kB shared-chunk advisory remains.
- Independent review found returning-step camera reset, concurrent-pointer drag ownership, and direct exit-only NAT validation. Each was reproduced with a failing regression, fixed, and the full suite rerun.
- Actual browser acceptance: initial ingress → Internet; native server drops after pan and at 78% zoom; draggable ingress terminal; HK → SG → Internet; explicit HK edge removal preserved ingress; reconnection preserved ingress and old downstream edge until commit; Esc cancellation; IPv6 address selection; port 443; next/back preserved the exact viewport transform.
- Screenshots live outside Git under /tmp/lattix-accept/canvas-rebuilt-*.jpg.
- No new dependencies or request implementation; original form payloads retained. Work remains uncommitted.
