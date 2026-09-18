import type { SVGProps } from "react"

export function BrandMark({ className, ...props }: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 36 36"
      fill="none"
      className={className}
      aria-hidden="true"
      focusable="false"
      shapeRendering="geometricPrecision"
      {...props}
    >
      <rect x="0.5" y="0.5" width="35" height="35" rx="9.5" fill="url(#brand-mark-gradient)" />
      <path
        d="M11.25 18H14.5C18.5 18 18.5 9 22.5 9H24.75"
        stroke="white"
        strokeWidth="2"
        strokeLinecap="round"
      />
      <path
        d="M14.5 18C18.5 18 18.5 27 22.5 27H24.75"
        stroke="white"
        strokeWidth="2"
        strokeLinecap="round"
      />
      <circle cx="9" cy="18" r="2.25" fill="white" />
      <circle cx="27" cy="9" r="2.25" fill="white" />
      <circle cx="27" cy="27" r="2.25" fill="white" />
      <defs>
        <linearGradient
          id="brand-mark-gradient"
          x1="4"
          y1="3"
          x2="32"
          y2="34"
          gradientUnits="userSpaceOnUse"
        >
          <stop stopColor="#1683FF" />
          <stop offset="1" stopColor="#075FC8" />
        </linearGradient>
      </defs>
    </svg>
  )
}
