---
name: Lattix Operations Desk
description: A flat, hairline-ruled high-density operations desk for self-hosted proxy infrastructure, dark by default.
colors:
  ground: "#0c0d0e"
  surface: "#111214"
  raised: "#17181b"
  inset: "#0a0b0c"
  rule: "#26272a"
  rule-strong: "#3a3c40"
  ink: "#f1f1ec"
  ink-2: "#a6a7a1"
  ink-3: "#92938e"
  control: "#5f6368"
  accent: "#ff3b2f"
  accent-ink: "#111111"
  accent-soft: "rgba(255, 59, 47, 0.12)"
  selection: "rgba(255, 59, 47, 0.1)"
  focus: "#ff3b2f"
  success: "#5fbf7f"
  warning: "#d9a43b"
  danger: "#ff6b7a"
  overlay: "rgba(0, 0, 0, 0.62)"
  brand-ink: "#101508"
  brand-yellow: "#bdf33b"
  brand-mint: "#78d69e"
typography:
  display:
    fontFamily: "Geist Variable, Noto Sans SC, PingFang SC, Hiragino Sans GB, Microsoft YaHei, Noto Sans CJK SC, sans-serif"
    fontSize: "28px"
    fontWeight: 900
    lineHeight: 1
    letterSpacing: "-0.025em"
  headline:
    fontFamily: "Barlow Condensed, Geist Variable, PingFang SC, sans-serif"
    fontSize: "30px"
    fontWeight: 800
    lineHeight: 1
    letterSpacing: "-0.04em"
  title:
    fontFamily: "Geist Variable, Noto Sans SC, PingFang SC, Hiragino Sans GB, Microsoft YaHei, Noto Sans CJK SC, sans-serif"
    fontSize: "15px"
    fontWeight: 750
    lineHeight: 1.2
    letterSpacing: "-0.01em"
  body:
    fontFamily: "Geist Variable, PingFang SC, Hiragino Sans GB, Microsoft YaHei, Noto Sans CJK SC, sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Geist Variable, Noto Sans SC, PingFang SC, Hiragino Sans GB, Microsoft YaHei, Noto Sans CJK SC, sans-serif"
    fontSize: "10.5px"
    fontWeight: 650
    lineHeight: 1.2
    letterSpacing: "0.06em"
  control:
    fontFamily: "Geist Variable, Noto Sans SC, PingFang SC, Hiragino Sans GB, Microsoft YaHei, Noto Sans CJK SC, sans-serif"
    fontSize: "12px"
    fontWeight: 600
    lineHeight: 1
    letterSpacing: "0.01em"
  numeric:
    fontFamily: "Barlow Condensed, Geist Variable, PingFang SC, sans-serif"
    fontSize: "16px"
    fontWeight: 750
    lineHeight: 1.4
    fontFeature: "\"tnum\" 1"
  mono:
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace"
    fontSize: "11px"
    fontWeight: 400
    lineHeight: 1.4
    fontFeature: "\"tnum\" 1"
rounded:
  none: "0"
  hairline: "1px"
  control: "2px"
spacing:
  hairline: "1px"
  gap: "6px"
  cell: "8px"
  panel: "10px"
  page: "16px"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-ink}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "30px"
    typography: "{typography.control}"
  button-outline:
    backgroundColor: "transparent"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "30px"
    typography: "{typography.control}"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0 10px"
    height: "32px"
    typography: "{typography.body}"
  module:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.hairline}"
    padding: "8px"
  module-index-tab:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-ink}"
    rounded: "{rounded.none}"
    padding: "0 8px"
    height: "30px"
    typography: "{typography.label}"
  status-chip:
    backgroundColor: "transparent"
    textColor: "{colors.ink-2}"
    rounded: "{rounded.hairline}"
    padding: "1px 6px"
    typography: "{typography.label}"
  nav-item-active:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-ink}"
    rounded: "{rounded.hairline}"
    padding: "0 8px"
    height: "30px"
    typography: "{typography.control}"
  row-selected:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-ink}"
    rounded: "{rounded.none}"
    padding: "0 8px"
    height: "27px"
---

# Design System: Lattix Operations Desk

## Overview

**Creative North Star: "The Hairline Operations Desk"**

Lattix is one continuous ruled surface, not a page of cards. A near-black ground is divided by 1px rules into a mosaic of rectangular modules that tile edge to edge; the operator reads fleet totals, exceptions, server readings, throughput and runtime in one glance, then selects a server or chain and watches that selection take the one functional red. Nothing floats, nothing glows, and no decoration interrupts the scan. The world is Japanese contemporary high-density information web — small gothic type, dense rows, tabular numerals, and red used as a signal rather than a mood. Chinese headings speak in a self-hosted Noto Sans SC Black, Latin labels in Geist Variable, and changing metric figures in Barlow Condensed.

Dark is the default appearance and the primary rendering of the world; light is the same token system in daylight, not a second design language. Both appearances resolve identical slots (`--panel-*`), carry identical geometry (0–2px), identical rules (1px) and identical semantics, so a screen documented here is true in both. Both appearances add the same material concession: a 128px paper-grain tile on the page field — multiplied into the paper ground in daylight, screened faintly over the near-black ground at 8% peak in dark. It is atmosphere on the field only; modules stay flat. In both appearances module heads close with a doubled hairline — a 1px rule plus a 50%-opacity inner rule — rather than a shaded band. The old `hig` / `cream` / `patchbay` themes and their cream paper, lime accents and equipment-metal metaphors are gone; there is a single visual language.

Hierarchy comes from three things only: rule weight (`--panel-rule` vs. `--panel-rule-strong`), tonal steps of the same hue (#0c0d0e ground → #111214 surface → #17181b raised → #0a0b0c inset), and the red index tab that names each module. Density is generous where it aids scanning (26–30px row heights, 8–10px cell padding) and never padded out for breathing room. Chinese copy stays Chinese; English uppercase appears only where it is a machine identifier — the module index tab, a column head, a panel state code.

**Key Characteristics:**

- Flat near-black ground with a faint screened 128px grain in dark, paper-white with a multiplied 128px grain in light; no gradients, no glass, no colored side stripes.
- Every boundary is a 1px hairline rule, doubled at module heads; modules are rectangles with 0–2px corners and a red index tab.
- One functional red, used only for primary action, selection, and live exception.
- Chinese page and dashboard headings in self-hosted Noto Sans SC Black at 900, Latin in Geist Variable, metric figures in Barlow Condensed, data rows in tabular mono.
- Dense rectangular modules tiled edge to edge on a 6px gutter, not floating cards or hero metrics.
- Zero shadows at rest; only overlays lift.
- Motion is a single 180ms entrance and a one-shot hop reveal; reduced motion removes both.

## Colors

The palette is a near-monochrome field with one functional red family, plus green and amber as the only other chromatic UI signals (the preserved Lattix mark's lime and mint are brand material, never UI colour). The utility red and the danger crimson are two steps of that one red family, each with a single meaning; neither is decoration. Values below are the dark appearance (default); the daylight appearance resolves the same slots (see The Daylight Equivalence Rule).

### Primary

- **Utility Red** (#ff3b2f, daylight #e60012): the only primary color. It appears as the module index tab, the selected row/chain fill, the active navigation item, the primary button, and the focus ring. It is a signal, not a surface treatment, and it is rare by design.
- **Red Washes** (`--panel-accent-soft`, `--panel-selection`): 6–12% alpha red for transient highlights only — the hop reveal's opening frame, a highlighted combobox row, a checked option. Selection itself is never a wash; it is a solid fill.

### Secondary

- **State Green** (#5fbf7f, daylight #1f7a4a): healthy agent, chain and link states, success notices, and positive deltas. Always paired with a text label or a state word, never color alone.

### Tertiary

- **State Amber** (#d9a43b, daylight #966000): degraded, stale or unconfirmed state — the warning layer between red exception and green health.
- **Exception Red** (#ff6b7a, daylight #b3122f): a crimson-pink step deliberately separated from the utility red, so a fault never reads as a primary action. Failure text, danger borders, destructive actions, error chips and invalid fields. It is not a second accent and never appears on ordinary action.

### Neutral

- **Near-Black Ground** (#0c0d0e): the page/body ground behind the mosaic; the daylight version is #f4f4f2.
- **Surface** (#111214 / #ffffff): every module body, the command bar, the navigation rail.
- **Raised** (#17181b / #fafaf8): hover fill and secondary chrome steps.
- **Inset** (#0a0b0c / #ededea): recessed strips — table heads, the chain bench bay, metric value wells.
- **Rule** (#26272a / #dededa): the standard 1px boundary between every module, row and section.
- **Rule Strong** (#3a3c40 / #bdbdb7): the heavier boundary reserved for chips, comboboxes, drawers and the hop strip.
- **Ink** (#f1f1ec / #111111): primary text and numerals.
- **Ink 2** (#a6a7a1 / #5a5b57): secondary labels, module meta, muted values, the info tone.
- **Ink 3** (#92938e / #6b6c66): tertiary annotations, placeholders, `dt` captions.
- **Control** (#5f6368 / #8f8f89): the default outline-button border and input border.

**Charts.** Cost series are not categorized by hue: the bar and donut charts draw a grayscale ramp — ink → ink-2 → ink-3 → rule-strong — with the utility red as the single highlight. State green and amber never appear as data categories; they remain reserved for infrastructure state.

### Named Rules

**The One Functional Red Rule.** Red is a signal: primary action, current selection, live exception (carried by the danger step of the same family), and the module index tab. If red is not carrying one of those four meanings it is a defect — including decorative accents, section markers, gradients, and colored stripes.

**The Daylight Equivalence Rule.** Dark is the default appearance; light is the same token set in daylight, never a second palette. Any new surface must be built from `--panel-*` slots so it renders in both, and no component may be designed against one appearance only.

**The Two-State Rule.** Green and amber describe infrastructure state, red describes operator intent and exceptions. State colors are always carried by a word as well as a hue; a colored square alone is never the message.

## Typography

**Display Font:** Geist Variable for Latin, Noto Sans SC Black for Chinese (self-hosted subset; stacked Geist Variable → Noto Sans SC → PingFang SC, Hiragino Sans GB, Microsoft YaHei, Noto Sans CJK SC)
**Body Font:** Geist Variable (with PingFang SC, Hiragino Sans GB, Microsoft YaHei, Noto Sans CJK SC, sans-serif)
**Metric Font:** Barlow Condensed (600/700 bundled via @fontsource, tabular figures)
**Label/Mono Font:** ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas (tabular figures)

**Character:** Three faces divide the work. Chinese display headings are set in Noto Sans SC Black — self-hosted as a simplified-Chinese subset and declared over a 700–900 weight range — so 仪表盘 and 机群总计 carry the same heavy gothic weight as the English `FLEET` machine labels in Geist Variable. Metric figures — fleet totals, throughput, subscription counts — switch to Barlow Condensed, whose narrow figures read as readout instrumentation rather than prose. Chinese body copy stays on the system PingFang/Hiragino/YaHei/Noto CJK stack, and data-row latency, addresses and counters stay in mono with tabular figures. A slightly tighter tracking at display sizes keeps dense Chinese titles from opening up.

### Hierarchy

- **Display** (900, 28px, 1 / -0.025em): dashboard title (22px below 768px); other page titles at 24px/900, the login statement at 34px/900 (26px below 768px) and the login form heading at 20px/900.
- **Headline** (800, 30px, 1 / -0.04em): the largest readout — the 订阅用户 total — plus 22px/780 机群总计 values; Barlow Condensed, tabular.
- **Title** (750, 15px, 1.2 / -0.01em): card and drawer headings; module heads run smaller at 11.5px/700.
- **Body** (400, 13px, 1.5): descriptions, form copy, notices; page descriptions cap at 72ch.
- **Label** (650, 10.5px, 1.2 / +0.06em): module index tabs, table heads, status chips, `dt` captions — the uppercase machinery layer.
- **Control** (600, 12px, 1 / +0.01em): buttons, navigation items, tabs, segmented controls.
- **Numeric** (750, 16px, Barlow Condensed, tabular): throughput values and inline metric figures.
- **Mono** (400, 11px, tabular): latency, addresses, byte counters, IDs in data rows.

### Named Rules

**The Black Display Rule.** Chinese display headings are set in the self-hosted Noto Sans SC Black subset (page and dashboard titles at 900, the login statement at 900; the face declares a 700–900 range), stacked after Geist Variable and before the system CJK faces. It is the only Chinese display voice — no CDN face, no system serif, no faux-bold; Chinese body copy stays on the PingFang/Hiragino/YaHei/Noto CJK stack.

**The Tabular Numeral Rule.** Every figure that can change, or that sits in a column, is tabular — totals, throughput, latency, quota, cost, timestamps. Proportional figures are not used for data anywhere in the panel.

**The Chinese Copy Rule.** Interface copy stays Chinese. Uppercase Latin is reserved for machine identifiers and classification codes (module index tabs such as `FLEET` / `SERVERS` / `RUNTIME`, column heads, `ACTIVE` / `FAULTED` panel states, `NETWORK CONTROL PANEL` in the command bar) — never as an English restatement of a Chinese heading, never as a decorative eyebrow above a title. The access screen's headings now stand alone: the former `ACCESS` / `SIGN IN` eyebrow tabs are gone and do not return.

**The Dense Label Rule.** Labels and annotations may run as small as 9–10.5px because they are short and set in the gothic face; body copy never goes below 12px and table cells never below 11px, so density never costs legibility.

## Layout

The shell is a two-row grid: a 46px command bar over a body split into a 200px navigation rail and the main column (`--panel-bar`, `--panel-rail`). The rail carries a 1px right rule and is the only persistent chrome; the command bar carries the Lattix mark, the panel state badge, the date, appearance switch and account. The main column is capped at 1920px with 14px / 16px / 20px padding and scrolls internally on desktop, so the command bar and rail stay put.

Content is a mosaic, not a stack. The dashboard is a 12-column grid with a 6px gutter and three desktop rows (1fr / 1.35fr / 1fr); modules span 3, 4, 6 or 8 columns so 机群总计, Agent 状态 and 链路总览 fill the first row and the 服务器读数 table (8 columns) sits beside 事件 (4). Modules are `flex` columns whose header is fixed at 30px min-height and whose body absorbs the remaining space with its own scroll — so the desk never scrolls as a page to reach a module.

Spacing is a tight fixed rhythm: 6px between modules and page sections, 8px inside module bodies and table cells, 10px in module headers and panel padding, 1px for grid gaps that stand in for rules (the summary and runtime grids draw their internal lines with a 1px gap over the rule color rather than borders). Dense rows are 26–30px high: table heads 26px, dashboard table rows 27px, server readings rows 26–27px, navigation items and module headers 30px, status chips ~20–24px.

Responsive behavior is a staged collapse of the mosaic, never a shrunken desktop. At 1279px the 3- and 4-span modules become half-width and the 6- and 8-span modules go full width; at 1023px the mosaic drops to 6 columns and the rail is replaced by a sheet opened from the command bar; at 767px everything spans the full 6 columns, the summary and runtime grids drop to two columns, and the mosaic abandons fixed row ratios (`grid-auto-rows: auto`). Extra table columns are hidden from 767px; below 480px the actions row takes full width.

### Named Rules

**The Edge-To-Edge Mosaic Rule.** Information tiles: modules share gutters and rules, align to one 12-column grid, and fill their rows. Floating cards with outer margins, centered hero metrics, and decorative empty space between modules are not part of this system.

**The Module Owns Its Scroll Rule.** A module's body scrolls inside its own ruled rectangle. A page-level scroll that hides a module's header — the tab and title that identify it — is a layout defect.

## Elevation & Depth

There are no shadows at rest. Modules, cards, panels, tables, inputs and buttons are flat rectangles separated by 1px rules; `box-shadow: none` is declared on the container primitives and on shadcn's card slot. Depth is expressed tonally instead: ground is the darkest step, surface sits one step above it, raised one more, and inset recesses below (table heads, the hop bench bay, metric wells). In the daylight appearance the same four steps read as #f4f4f2 / #ffffff / #fafaf8 / #ededea. Overlays are the only exception: dialogs, sheets, dropdown menus, select popovers and the server drawer take a border plus the single overlay shadow token, and the scrim sits at `rgba(0,0,0,0.62)` in dark.

Material is nearly absent, and what exists belongs to the page field rather than to components. Both appearances carry a 128px tiled paper-grain PNG on the page field: multiplied into the paper white in daylight (`background-blend-mode: multiply`, values 228–255), and screened faintly over the near-black ground in dark (`background-blend-mode: screen`, values 0–20). The grain covers the admin shell, login and public subscription field; modules stay flat. Module heads carry a doubled hairline instead of a shadow: the 1px `--panel-rule` bottom edge plus a 1px `--panel-rule-strong` inner rule at 50% opacity, set 2px above the header's bottom edge, on dashboard module heads and shared `.cg-panel` headers. It registers the header edge; it does not lift it.

### Shadow Vocabulary

- **Overlay (dark, `0 22px 48px -20px rgba(0, 0, 0, 0.8)`)**: dialogs, sheets, menus, combobox popovers, drawers. Never used on an in-page module.
- **Overlay (daylight, `0 18px 40px -18px rgba(20, 20, 20, 0.32)`)**: the same role in the light appearance.
- **Focus ring (not a shadow)**: `outline: 2px solid` the focus color, `outline-offset: 1px`; inputs and comboboxes shift their border to the focus color instead of glowing.

### Named Rules

**The Flat-At-Rest Rule.** Surfaces are flat until they are an overlay. No hover lift, no glow, no elevation animation; hover is a fill change (#17181b raised or the 4.5% hover wash) and active is a 1px downward shift on buttons.

**The No-Fake-Depth Rule.** No gradients, no glass, no backdrop blur, no inset highlight/shadow used to imitate material. If a boundary is needed, it is a 1px rule; if a step is needed, it is a tonal slot.

**The Paper Grain Rule.** Texture belongs only to the page field — a 128px paper-grain tile, multiplied in daylight and screened faintly in dark; the shell, modules and components stay flat. Never on a module, never as decoration on a component. Module heads close with the doubled hairline (1px `--panel-rule` outer edge plus a 50%-opacity 1px `--panel-rule-strong` inner rule), never with a fill or a shadow. Grain is atmosphere, not a surface treatment: never inside a module, never as decoration on a component.

## Shapes

Geometry is rectangular: 0px for index tabs, status squares, avatars and the brand mark; 1px for modules, rows, nav items, chips and tab triggers; 2px for buttons, inputs, tables, tab lists and the shared `--radius` token. Tailwind's whole radius scale is flattened to 1px/2px so a stray `rounded-lg` still lands in the system. Circles are rare and functional: 6px status dots beside a state word, and the rounded progress track. Bars are rules with a fill — 2px runtime meters, 3px throughput bars — never rounded capsules.

Borders carry the structure: 1px `--panel-rule` between modules and rows, 1px `--panel-rule-strong` for chips, comboboxes, drawers and the hop strip, and a top-border color shift (green/amber/red) as the only per-hop decoration. Selected rows and chains invert to a solid red fill with `--panel-accent-ink` text; selected cells never add a border, a checkmark or a left stripe.

### Named Rules

**The Two-Pixel Ceiling Rule.** Nothing in the working surface exceeds a 2px corner radius. Pills, capsules, 8px cards and circular buttons are outside the world; if a control asks for a rounder shape, it asks for the wrong control.

**The Rule-Not-Stripe Rule.** Separation is a hairline on an edge, not a colored side bar, a left accent stripe, or a gradient edge. The only filled edge in the system is the red index tab at a module's top-left.

## Components

### Buttons

- **Shape:** Rectangular control, 30px high, 2px radius, 12px/600 label, 6px icon gap. The shadcn primitive (`data-slot="button"`) runs 32px on the same 2px radius, so both land in the system.
- **Primary:** Solid utility red with `--panel-accent-ink` text (`is-primary`), used once per local action group. Hover keeps the fill and applies a 5% brightness step; there is no lift.
- **Outline (default):** Transparent ground with a 1px `--panel-control` border; hover moves the border to ink and the fill to raised; active drops to inset; disabled is 42% opacity with `not-allowed`.
- **Icon buttons:** 28px square, transparent 1px border that only appears on hover (`--panel-rule-strong`), 15px stroke icon.
- **Focus:** A 2px outline in the focus color at 1px offset (buttons additionally get a 1px ring in the shadcn primitive); keyboard focus is never removed, only restyled.

### Chips

- **Style:** Rectangular tags with a 1px current-color border, 1–2px radius, 1px 6px padding, 10–10.5px/650 label, transparent fill. The module-level status chip carries a 5px square before its text (`::before` at `currentColor`); chips are never pills.
- **Tone:** Neutral (`ink-2` on rule), healthy (`--panel-success` border and text over a 12% success wash), warning (amber), danger (red). Active/selected chips invert to an ink or red fill.
- **State:** Status chips are read-only; filter and tab chips are buttons and use the same geometry with a red selected fill.

### Cards / Containers

- **Corner Style:** Modules at 1px, cards/panels at 2px — never more.
- **Background:** `--panel-surface` for content; `--panel-inset` for recessed strips; the charcoal slot (`--cg-charcoal`) for terminal and code blocks.
- **Shadow Strategy:** None. See Elevation & Depth.
- **Border:** 1px `--panel-rule` on all sides; the header closes with the doubled hairline — the 1px bottom rule plus a 50%-opacity 1px `--panel-rule-strong` rule 2px above it — never a fill.
- **Internal Padding:** 8px body, 0 8–10px header at 30px min-height; table cells 0 8px horizontally.
- **Signature header:** a solid red index tab flush to the module's top-left, then the Chinese module title in Noto Sans SC Black at 11.5px/700, optional meta, then a right-aligned 10px link, all over the doubled hairline.

### Inputs / Fields

- **Style:** 32px high, 2px radius, 1px `--panel-control` border, surface background, 14px text on desktop (16px below 768px), 10px horizontal padding; placeholder uses `ink-3`.
- **Focus:** Border shifts to the focus color with a 1px ring; no glow.
- **Error / Disabled:** `aria-invalid` switches the border to danger with a 2px danger ring at 20%; disabled drops to muted background and 50% opacity.
- **Density:** settings and filter rows stack label over field with a 4px gap and run two columns where the form allows.

### Navigation

Navigation is a 200px rail of grouped links, not a menu of cards. Each group carries a Chinese label (实时控制 / 资源管理 / 系统) with a small uppercase code set beside it (`LIVE OPS` / `RESOURCES` / `SYSTEM`), separated from the next group by a 1px top rule. Items are 30px high, 1px radius, 12.5px/500 in `ink-2` with a 14px stroke icon at 72% opacity; hover adds a rule border, raised fill and ink text; the current item is a solid red fill with `--panel-accent-ink` text and a 650 weight. On screens below 1024px the rail is replaced by the same list inside a left sheet opened from a 28px icon button.

### Module index tab (signature)

Every module is identified by a red tab sitting flush in the top-left corner of its header, level with the 1px bottom rule: 9px/750 uppercase tracking +0.12em, 0 8px padding, 30px tall, no radius. It carries the English classification code (`FLEET`, `AGENTS`, `CHAINS`, `SERVERS`, `EVENTS`, `RATE`, `RUNTIME`, `USERS`) while the adjacent title carries the Chinese name (机群总计, Agent 状态, 链路总览, 服务器读数, 事件, 实时吞吐, 运行状态, 订阅用户). The tab is the module's own label, not a selection: it stays red whether or not anything inside the module is selected.

### Readings table and chain bench (signature)

The 服务器读数 table is the densest object in the panel: 9.5px column heads on an inset strip, 27px rows with 1px bottom rules, numbers right-aligned in tabular mono, and the whole row as the selection target. Selecting a row fills it with the utility red and switches every inner mark — alias, region, tags, figures and the status dot — to `--panel-accent-ink`, matching the /servers row and the active chain row; selection is never a wash tint. The selection updates the runtime module in place. The chain bench renders a selected chain as a single hairline strip of hop cells: 1px `--panel-rule-strong` borders with collapsed left edges, a mono sequence number, alias at 10.5px/650, role and state at 9px, and a top border colored green/amber/red per hop. Hops animate in once, staggered 45ms, on selection change.

### Access screen

The login is a single 840px two-column ruled card — an inset brief panel carrying a 34px/900 Chinese statement and three facts, and a form panel with a 20px/900 heading, 32px fields and one full-width primary button. Both headings stand alone: the former `ACCESS` / `SIGN IN` eyebrow tabs are removed and no uppercase label sits above either heading. Below 768px the card stacks, the brief gains a bottom rule, and the statement drops to 26px.

### Named Rules

**The Tab Opens The Module Rule.** A module without its red index tab is unrecognizable in this system: tab, Chinese title, and the doubled header hairline travel together. The tab names what the module is; it is not a decorative kicker and never sits above a page heading.

**The Selection Is Red Rule.** Current selection is expressed as a solid red fill across the entire row or chain, with inverted text and desaturated inner chips. Selection is never a border, a checkmark, a background tint plus stripe, or a hover-only state.

**The Chain Bench Rule.** One selected chain drives the hop strip; the selected server drives the runtime module. Each selection updates its module in place without navigating, and the hop strip reveals once per change rather than looping.

## Do's and Don'ts

### Do:

- **Do** build every screen as ruled modules on the 12-column mosaic with the 6px gutter and `--panel-*` tokens, so it renders identically in dark and light.
- **Do** give each module its red index tab plus a Chinese title, and keep the header at 30px with the doubled hairline.
- **Do** set Chinese page and dashboard headings in the self-hosted Noto Sans SC Black (900) and metric figures in Barlow Condensed; keep Chinese body copy on the system CJK stack.
- **Do** keep the paper grain to the page field (128px tile; multiply in daylight, faint screen in dark) and keep modules flat.
- **Do** use tabular numerals for every figure and keep Chinese product copy in Chinese.
- **Do** express selection as a full red fill with inverted text, and primary action as the single red button in its local group.
- **Do** keep rows dense (26–30px) and align columns to the table's right edge for numbers.
- **Do** preserve keyboard focus (`outline: 2px`, 1px offset), semantic status text, and `prefers-reduced-motion` opt-outs whenever a new surface is added.
- **Do** let the selected server/chain update its module in place rather than pushing a new route.

### Don't:

- **Don't** introduce a second accent hue beyond the utility red and its danger step, a tinted surface, a gradient, or a colored side stripe.
- **Don't** put decorative English eyebrows or kickers above Chinese headings; uppercase belongs to the module tab and machine codes, and the removed `ACCESS` / `SIGN IN` tabs stay removed.
- **Don't** use the danger crimson for ordinary action or decoration; it is the fault step, not a second accent.
- **Don't** color chart series with state green or amber; data series are grayscale plus one red.
- **Don't** round above 2px, use pills for status, or turn modules into floating cards with outer shadows and margins.
- **Don't** add shadows, glows, glass, or backdrop blur to in-page surfaces; overlay shadow only.
- **Don't** lead a page with a hero metric or a marketing-style header block; the first viewport is the desk.
- **Don't** encode state with color alone — a label or state word must carry it.
- **Don't** depend on external font or texture CDNs; the panel must render fully self-hosted.
- **Don't** reintroduce theme variants beyond appearance: one design language, shipped dark by default and light as its daylight version.
