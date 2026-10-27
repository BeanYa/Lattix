---
version: 1
slug: "src-frontend-src-pages-dashboard-tsx"
primary_target: "src/frontend/src/pages/Dashboard.tsx"
related_targets: ["src/frontend/src/components/Layout.tsx","src/frontend/src/index.css","src/frontend/src/styles/panel.css","src/frontend/src/pages/dashboard.css"]
---

# Whole panel / High-Density Operations Desk

- Scope: the entire Lattix SPA (shell, login, dashboard, every route); visitor mode: Operate.
- Audience and job: self-hosted proxy infrastructure administrators scanning fleet health, chains, users, subscriptions and runtime state over long desktop sessions, then acting on exceptions.
- Preserve: all routes, API and data contracts, Chinese product copy, existing polling and operation-progress behavior, keyboard access, reduced-motion, light and dark appearance, self-hosted assets only (no external font or asset CDN).
- Direction: liquid glass — a warm dusk gradient field (night copper haze dark / warm paper light), frosted translucent glass cards with 1px edge highlights and 20px radii, Manrope type with tabular numerals, copper accent reserved for current item, primary action, selection and in-progress. Default appearance is dark; light is the daylight rendition of the same world.
- Memorable moment: selecting a server in the dense readout table updates a compact runtime module in place, while the chain bench shows the selected chain's ordered hops as a single hairline strip that pulses once when data changes.

## Direction contract

THESIS: One warm glass desk floating over a dusk field — every fleet fact sits on a frosted translucent module; depth comes from translucency and soft shadows, not hairlines.

OWN-WORLD: Warm dusk gradient ground (dark: #14100d with copper haze; light: #e8d2c4 warm paper); frosted glass modules (blur(28px) saturate(140%), translucent surface, 1px white edge highlight, 20px radii); Manrope type, tabular numerals; copper (#c9804f dark / #8f5329 light) only for selection, primary action, and current item; state green/amber/clay as the only other chromatic signals.

STORY: The operator reads the fleet totals, sees which agents, chains and servers are degraded, selects a server to inspect its runtime, and follows the chain bench into the chain page; no scrolling through decorative cards.

FIRST VIEWPORT: 200px grouped navigation rail on the left, thin command bar on top, then a full-width mosaic: 机群总计 spanning two columns, Agent 状态, 链路总览; below it the dense 服务器读数 table spanning three columns beside 事件; below that 实时吞吐, the selected server's runtime module, and 用户/订阅 totals. Every module is a ruled rectangle with a small red index tab in its header; the selected row and active chain are the red fill.

FORM: Japanese contemporary high-density web (dealt catalog challenger japanese-high-density-web, chosen by the user over the roll's assigned direction and pick; not on the ordered grounding list); seed:831c104c712a269b.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance.
