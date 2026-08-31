---
version: 1
slug: "frontend-src-themes-patchbay-dashboardpatchbay-tsx"
primary_target: "src/frontend/src/themes/patchbay/DashboardPatchbay.tsx"
related_targets: ["src/frontend/src/themes/patchbay/dashboard.css"]
---

# Dashboard / Signal Rack

- Scope: `src/frontend/src/themes/patchbay/DashboardPatchbay.tsx`; visitor mode: Operate.
- Audience and job: self-hosted proxy infrastructure administrators scanning fleet health, selecting an existing chain, tracing its ordered topology, and recovering degraded paths.
- Preserve: existing API calls, demo-data behavior, Chinese product language, theme/light-dark switching, keyboard access, and every existing route.
- Direction: Command Rail — a compact health strip above a route-led operating bay, with a narrow incident dock and a selected-entry-server instrument rail. The memorable moment is selecting a real chain in the left rail and watching its truthful hop topology take over the central bay.
- Approved comp: `.impeccable/mocks/dashboard-command-rail.png` (approved with chain-driven left rail; the comp's server-driven rail must not be literalized).

## Fidelity inventory

| Ingredient | Commitment | Medium |
| --- | --- | --- |
| App shell | 190px charcoal equipment rail; full-height; compact engraved navigation states | semantic HTML/CSS + Lucide icons |
| Master strip | aluminum title/status strip with visible refresh action | semantic HTML/CSS |
| Readouts | three grouped operational totals plus issue count, tabular numerals, label + state detail | semantic HTML/CSS |
| Chain rail | existing non-deleted chains, chain name, hop count and redundant state; selection drives the workbench | semantic buttons + CSS |
| Routing focus | selected chain only, with ordered real hops, server identity, hop role/status and one truthful active-signal pulse | semantic ordered list + CSS |
| Incident dock | current unavailable-server and degraded-chain exceptions; no invented recovery action | semantic list + CSS |
| Runtime instruments | selected chain's entry server identity plus CPU, TX, RX, disk, uptime and Agent | semantic `dl` + CSS meters |
| Material | subtle brushed aluminum and charcoal enamel only where the comp shows physical surface | generated seamless raster tiles |
| Type | Barlow Condensed for equipment labels; Segoe UI/PingFang for dense body copy | locally bundled font + system CJK fallbacks |
| Responsive | desktop uses chain rail + topology + incident dock; mobile stacks a horizontally scrollable chain selector, compact topology, incidents and two-column instruments | CSS media/container queries |

Unresolved: none. Core controls and text must never be rasterized; sockets, routes and meter geometry remain vector/code.
