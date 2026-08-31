---
name: Lattix Signal Rack
description: A tactile signal-routing console for trustworthy infrastructure operations.
colors:
  signal-blue: "#315a70"
  signal-blue-bright: "#4e91b4"
  safety-orange: "#f05a28"
  signal-green: "#6f933d"
  signal-green-bright: "#9bc356"
  aluminum: "#d8d8d0"
  aluminum-bright: "#f0efe9"
  aluminum-dim: "#b9b9b1"
  charcoal: "#1d2528"
  enamel: "#111719"
  ink: "#1b2022"
  paper-white: "#f7f7f2"
typography:
  display:
    fontFamily: "Barlow Condensed, Arial Narrow, PingFang SC, sans-serif"
    fontSize: "clamp(1.65rem, 2vw, 2.35rem)"
    fontWeight: 740
    lineHeight: 0.92
    letterSpacing: "-0.015em"
  title:
    fontFamily: "Barlow Condensed, Arial Narrow, PingFang SC, sans-serif"
    fontSize: "20px"
    fontWeight: 700
    lineHeight: 1
    letterSpacing: "0.01em"
  body:
    fontFamily: "Segoe UI, PingFang SC, Microsoft YaHei, sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Barlow Condensed, Arial Narrow, PingFang SC, sans-serif"
    fontSize: "12px"
    fontWeight: 650
    lineHeight: 1.2
    letterSpacing: "0.05em"
rounded:
  engraved: "3px"
  control: "5px"
  module: "7px"
  panel: "8px"
spacing:
  port: "4px"
  module-gap: "8px"
  control-x: "10px"
  panel: "12px"
  section: "14px"
components:
  button-primary:
    backgroundColor: "{colors.signal-blue}"
    textColor: "{colors.paper-white}"
    rounded: "{rounded.control}"
    padding: "0 11px"
    height: "32px"
    typography: "{typography.label}"
  button-secondary:
    backgroundColor: "{colors.aluminum-dim}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0 10px"
    height: "32px"
    typography: "{typography.label}"
  input:
    backgroundColor: "{colors.aluminum-bright}"
    textColor: "{colors.ink}"
    rounded: "{rounded.engraved}"
    padding: "8px 10px"
    typography: "{typography.body}"
  module:
    backgroundColor: "{colors.aluminum}"
    textColor: "{colors.ink}"
    rounded: "{rounded.module}"
    padding: "12px"
  status-healthy:
    backgroundColor: "{colors.aluminum-bright}"
    textColor: "{colors.signal-green}"
    rounded: "{rounded.engraved}"
    padding: "0 10px"
    height: "32px"
    typography: "{typography.label}"
  nav-active:
    backgroundColor: "{colors.charcoal}"
    textColor: "{colors.paper-white}"
    rounded: "{rounded.engraved}"
    padding: "0 10px"
    height: "40px"
    typography: "{typography.title}"
---

# Design System: Lattix Signal Rack

## Overview

**Creative North Star: "The Live Signal Rack"**

Lattix should feel like a purpose-built control instrument: tactile, dense, and calm enough for long operational sessions. Its visual world borrows from modular synthesizers and broadcast patchbays—not as decoration, but as a legible model for servers, routes, endpoints, state, and signal flow.

Manufactured surfaces establish hierarchy. Pale anodized modules carry readable content; charcoal enamel holds routing and terminal work; engraved rules and condensed labels keep dense interfaces ordered. Color is operational evidence: blue identifies a live selected signal, green confirms health, and safety orange interrupts the surface only when attention is required.

**Key Characteristics:**

- Physical rack modules instead of generic floating SaaS cards.
- Condensed equipment typography paired with a neutral CJK-capable body face.
- State-first hierarchy with redundant text, color, and shape cues.
- Dense desktop workbenches that become deliberate vertical assemblies on narrow screens.
- Motion reserved for real progress, loading, and active signal flow.

## Colors

The palette combines warm industrial metal with a near-black chassis and a small set of signal colors whose rarity preserves meaning.

### Primary

- **Deep Signal Blue:** The primary action, focus, active navigation, and confirmed live-route color.
- **Bright Trace Blue:** A higher-energy variant for selected route traces and dark-surface accents.

### Secondary

- **Instrument Green:** Healthy status text, LEDs, and meters.
- **Lamp Green:** A brighter local indicator used only where a small signal must remain visible.

### Tertiary

- **Safety Orange:** Degraded, failed, reconnecting, and attention-required states; never a decorative accent.

### Neutral

- **Brushed Aluminum:** Default module surface for information and controls.
- **Bright Aluminum:** Raised or input-adjacent surface.
- **Dim Aluminum:** Secondary control surface and dividers.
- **Rack Charcoal:** Navigation chassis and high-contrast framing.
- **Enamel Black:** Routing matrices, terminals, and recessed technical work.
- **Equipment Ink:** Primary light-surface text.
- **Paper White:** Text and icons on dark or primary surfaces.

### Named Rules

**The Signal Truth Rule.** Blue motion belongs only to an active selected route; pending, degraded, failed, and unassigned routes must retain their real state styling.

**The Safety Orange Rule.** Orange is an exception channel, not a brand flourish. If nothing needs attention, it should be almost absent.

## Typography

**Display Font:** Barlow Condensed (with Arial Narrow and PingFang SC fallbacks)  
**Body Font:** Segoe UI (with PingFang SC and Microsoft YaHei fallbacks)  
**Label Font:** Barlow Condensed (with CJK fallbacks)

**Character:** Condensed faces make operational labels and numerals read like engraved equipment markings, while the body stack keeps Chinese explanations and form copy familiar. The contrast is technical without becoming theatrical.

### Hierarchy

- **Display** (740, fluid 1.65–2.35rem, 0.92): Page titles and major instrument headings.
- **Title** (700, 20px, 1): Module identities, selected resources, and high-value labels.
- **Body** (400, 13px, 1.5): Explanations, form guidance, and readable operational copy; keep prose near 70 characters per line.
- **Label** (650, 12px, 0.05em tracking): Controls, column heads, state plates, and technical annotations; uppercase is appropriate for short English equipment labels.

### Named Rules

**The Engraved Label Rule.** Use condensed type for short operational labels and metrics, not for paragraphs or long error explanations.

## Layout

Desktop uses a 190px charcoal equipment rail with a compact work surface up to 1680px wide. Modules sit on an 8px local gap; page sections use a 14px rhythm and 10–12px panel padding. Dense screens should establish status first, put the primary working instrument next, and keep selected-resource detail adjacent to that instrument.

The Dashboard expresses this model as a coordinated Command Rail: a compact master strip and four readouts sit above a dark operating deck; inside the deck, a 226px existing-chain rail drives the selected chain's ordered topology while a 284px incident dock remains visible beside it. The aluminum runtime rack below belongs to the same selection and reports the selected chain's entry server. At narrower desktop widths, the incident dock stacks below the route bay before the chain selector becomes a horizontal rail and runtime instruments settle into two columns at 760px and below.

At intermediate widths, multi-column readouts collapse before the operational surface does. At 760px and below, do not preserve a desktop rack through a long horizontal scroll: stack source controls, provide a compact route or state view, then place endpoints and instruments in two-column groups. Mobile supports quick inspection and limited action rather than compressing every desktop affordance.

## Elevation & Depth

Depth is structural and shallow. Aluminum modules use engraved inset highlights; dark technical fields are recessed; menus and dialogs receive the only material lift. Texture is confined to the page backing and chassis. Content cards, readouts, menus, and the routing canvas use opaque, noise-free fills so labels and boundaries remain readable.

### Shadow Vocabulary

- **Engraved module:** A bright top inset and dark bottom inset make a stamped metal plate.
- **Recessed field:** A short inner shadow establishes route matrices and terminals below the chassis.
- **Raised overlay:** A small near shadow plus a broad low-opacity shadow separates menus and dialogs from the workbench.

### Named Rules

**The Manufactured Depth Rule.** Shadows describe assembly—raised plate, recessed bay, or temporary overlay. They never create decorative floating card stacks.

## Shapes

The system is mostly squared with gently machined corners. Tiny labels use 3px corners, controls use 4–5px, modules use 7px, and major panels stop at 8px. Thin borders and engraved dividers carry more weight than radius. Circular geometry is reserved for screws, sockets, LEDs, and route ports, where it communicates a physical function.

## Components

### Buttons

- **Shape:** Compact machined control (5px radius, 32px minimum height).
- **Primary:** Deep signal blue with paper-white text; use for the single immediate action in a local control group.
- **Hover / Focus:** Slightly brighter blue on hover and a 2px visible focus outline; active state moves down by 1px and becomes inset.
- **Secondary:** Aluminum surface with equipment ink and an engraved edge.
- **Disabled / Busy:** Reduce opacity, block repeat input, retain the label, and show explicit progress text or motion.

### Chips

- **Style:** Rectangular status plates with a 4px radius, fine current-color border, condensed label, and redundant text.
- **State:** Healthy uses green; informational uses blue; warnings and failures use safety orange or destructive red.

### Cards / Containers

- **Corner Style:** Machined module corners (7px).
- **Background:** Solid aluminum for readable surfaces; solid enamel for routing and terminals. Server cards use raised slate (#314249) against a dark canvas (#182226), with muted text at #b4c5c8.
- **Shadow Strategy:** Engraved at rest; raised shadows are reserved for overlays.
- **Border:** One-pixel rule with enough contrast to remain visible in both appearance modes.
- **Internal Padding:** Usually 10–14px, tightened only for repeated instrument cells.

### Inputs / Fields

- **Style:** Slightly bright aluminum field, 4px radius, one-pixel rule, and a shallow inner shadow.
- **Focus:** A 2px signal-blue outline offset by 2px; never rely on color fill alone.
- **Error / Disabled:** Preserve readable text and pair color with explicit copy or an icon.

### Navigation

Navigation lives in the charcoal chassis. Default entries are muted, hover adds a subtle raised charcoal plate, and active entries use a blue border plus a 3px inset rail. On mobile, the chassis becomes a compact top bar and navigation opens through the existing semantic menu control.

### Dashboard Command Rail

The Dashboard's signature workbench is chain-led. Its left rail lists existing non-deleted chains as semantic pressed-state buttons with chain name, hop context, and redundant status; it is not a server selector. Selecting a chain updates the central ordered-hop topology and resolves that chain's first hop as the entry server for the bottom runtime instruments. The adjacent incident dock summarizes current unavailable-server and non-active-chain exceptions and routes the operator to existing management or log surfaces; it does not introduce an inline recovery action.

**The Chain Owns the Workbench Rule.** On the Dashboard, one selected existing chain is the source of truth for both the central topology and the bottom entry-server instruments.

### Routing Focus

The routing focus renders one selected chain's ordered hop sequence in a recessed bay, with server identity, hop role, location, status, and a compact chain summary. Keep fleet awareness in the existing-chain rail and incident dock instead of drawing every path at once. Hops derive from real chain data and status. Each pair of adjacent servers connects through explicit OUT and IN sockets with a continuous U-shaped patch cable. Shared socket offsets and a fixed inter-card gap keep both plugs attached even when card widths change. On narrow screens, only the topology rail scrolls horizontally, retaining readable card widths.

A bright segment travels along each cable from OUT to IN only when the selected route and both endpoint nodes are active. This indicates route status, not measured traffic. Faulted endpoints use a static orange cable; deployment-pending or unconfirmed connections use a static dashed cable with a state label. Reduced-motion mode hides the travelling segment while retaining cable and status information; signals pause when the routing stage leaves the viewport or the document is hidden. Each cable exposes an accessible source, destination, and state label.

### Motion feedback

- **Chain selection:** a 260ms sliding outline connects the old selection to the new one. Arrow keys and Home/End operate only within the chain selector; Tab retains normal focus navigation. Resizing snaps the outline to the new rail orientation.
- **Route handoff:** a short border emphasis advances from ENTRY to EXIT when a different chain is selected. Total stagger is capped at 135ms; content, sockets, and cables never disappear. Background polling does not restart this sequence or reset the topology scroll position.
- **Instrument updates:** a 260ms masked roll exchanges the previous and current formatted readings. Initial values appear immediately; unchanged values remain still. There are no interpolated telemetry values, count-from-zero intros, or animated version identifiers. Assistive technology receives the exact latest value without duplicate animation layers.
- **Cable flow:** a compact bright head and muted tail travel along the existing OUT → IN cable. A dedicated pause/play control governs the signal and route-handoff effects. All feedback honors reduced motion and hidden documents; finite animations cancel cleanly when interrupted.

Reference research: [React Bits Animated List](https://reactbits.dev/components/animated-list), [React Bits Counter](https://reactbits.dev/components/counter), and [Magic UI Animated Beam](https://magicui.design/docs/components/animated-beam). These are behavioral references, not vendored components: the implementation uses native Web Animations and existing CSS/SVG, without an additional animation dependency. The existing scroll-reveal component remains unchanged because an operational selection should respond to state, not scroll position.

### Chain discovery and topology navigation

The chain rail includes inline search and an All / Needs attention segmented filter with a 220ms sliding plate. Search matches normalized chain names, node aliases, codes, and locations; all words must match. Filtering preserves original numbering and the currently inspected chain, even if it is outside the results. Empty results offer an explicit reset. The existing selection frame tracks the filtered rows and disappears when the selection is outside the list; keyboard navigation stays scoped to visible results.

A compact topology navigator reports the actually readable node range, counting a card once at least half is visible. Its small viewport thumb follows the topology's horizontal scroll, not traffic or health. Previous / next and return-to-entry controls appear only when the route overflows, use native smooth scrolling, and respect reduced motion and the pause control. Resizing recalculates the range; switching chains returns to ENTRY, while polling preserves position. Ports and cables scroll together as one continuous instrument.

Behavioral references: [shadcn searchable selection](https://ui.shadcn.com/docs/components/base/combobox) and [Magic UI Scroll Progress](https://magicui.design/docs/components/scroll-progress). The search is intentionally inline, retaining the existing semantic buttons rather than introducing a duplicate popover. Both are implemented with the existing React/CSS infrastructure, not added packages.

## Do's and Don'ts

### Do:

- **Do** lead dense screens with current health, actionable exceptions, and progress feedback.
- **Do** derive route geometry, destination labels, and signal styling from real operational state.
- **Do** use aluminum and enamel textures only where a physical surface helps hierarchy.
- **Do** retain semantic controls, keyboard focus, redundant state labels, and reduced-motion behavior.
- **Do** redesign narrow layouts as vertical operational assemblies instead of shrinking desktop geometry.
- **Do** let Dashboard chain selection update both the central ordered topology and the bottom entry-server instruments.

### Don't:

- **Don't** turn every section into an interchangeable rounded card or floating glass panel.
- **Don't** animate an inactive, failed, pending, or unassigned route as if it were carrying traffic.
- **Don't** use safety orange for decoration, promotion, or ordinary selection.
- **Don't** rasterize controls, text, sockets, diagrams, or routing geometry.
- **Don't** render the entire route mesh when the operator is inspecting one selected chain.
- **Don't** turn the Dashboard's left Command Rail into a server selector or detach its runtime rack from the selected chain's entry server.
- **Don't** depend on external font or asset CDNs for a self-hosted control surface.
