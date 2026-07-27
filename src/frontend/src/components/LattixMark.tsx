import type { SVGProps } from 'react'

export default function LattixMark(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 64 64"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      <g
        fill="none"
        stroke="#6437f2"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="7"
      >
        <path d="M11 11v42h42" />
        <path d="m11 53 42-42" />
        <path d="M11 11h9c3 0 5 2 5 5v3c0 3 1 5 4 8l3 5 5 3c3 3 5 4 8 4h3c3 0 5 2 5 5v9" />
      </g>
      <g fill="none" stroke="currentColor" strokeWidth="5">
        <circle cx="11" cy="11" r="7" />
        <circle cx="53" cy="11" r="7" />
        <circle cx="11" cy="53" r="7" />
      </g>
      <circle cx="53" cy="53" r="7" fill="none" stroke="#06d5e8" strokeWidth="5" />
    </svg>
  )
}
