import type { SVGProps } from 'react'

export default function LattixMark(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 64 64"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      <g stroke="var(--brand-ink)" strokeLinejoin="miter" strokeWidth="4">
        <path d="M6 27V6h21v9H15v12Z" fill="var(--brand-yellow)" />
        <path d="M37 6h21v21h-9V15H37Z" fill="var(--brand-mint)" />
        <path d="M6 37h9v12h12v9H6Z" fill="var(--brand-mint)" />
        <path d="M49 37h9v21H37v-9h12Z" fill="var(--brand-yellow)" />
      </g>
      <rect
        x="22"
        y="22"
        width="20"
        height="20"
        rx="2"
        fill="var(--brand-ink)"
        transform="rotate(45 32 32)"
      />
      <rect
        x="28"
        y="28"
        width="8"
        height="8"
        rx="1"
        fill="var(--sidebar-foreground)"
        transform="rotate(45 32 32)"
      />
    </svg>
  )
}
