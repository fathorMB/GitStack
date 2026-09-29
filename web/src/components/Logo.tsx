// Logo GitStack (tre strati), lo stesso di design/mockups-v1 (#logo).
export function Logo({ size = 20, className }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none" aria-hidden="true" className={className}>
      <path d="M16 3.5 28.5 10 16 16.5 3.5 10Z" fill="currentColor" />
      <path
        d="M3.5 16 16 22.5 28.5 16"
        stroke="currentColor"
        strokeWidth="2.6"
        strokeLinecap="round"
        strokeLinejoin="round"
        opacity="0.75"
      />
      <path
        d="M3.5 22 16 28.5 28.5 22"
        stroke="currentColor"
        strokeWidth="2.6"
        strokeLinecap="round"
        strokeLinejoin="round"
        opacity="0.45"
      />
    </svg>
  );
}
