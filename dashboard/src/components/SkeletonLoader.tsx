export function SkeletonBox({
  className = '',
  height = 'h-4',
  width = 'w-full',
}: {
  className?: string
  height?: string
  width?: string
}) {
  return <div className={`skeleton-shimmer ${height} ${width} ${className}`} />
}

export function SkeletonKpiCard() {
  return (
    <div className="card-elevated p-6 space-y-4">
      <div className="flex items-center justify-between">
        <SkeletonBox width="w-28" height="h-3.5" />
        <SkeletonBox width="w-6" height="h-6" className="rounded-full" />
      </div>
      <div className="flex items-baseline gap-3">
        <SkeletonBox width="w-24" height="h-8" />
        <SkeletonBox width="w-16" height="h-5" className="rounded-full" />
      </div>
      <div className="flex items-center justify-between pt-2">
        <SkeletonBox width="w-36" height="h-3" />
        <SkeletonBox width="w-16" height="h-5" />
      </div>
    </div>
  )
}

export function SkeletonTableRows({ rows = 4, cols = 5 }: { rows?: number; cols?: number }) {
  return (
    <>
      {Array.from({ length: rows }).map((_, r) => (
        <tr key={r} className="border-b border-purple-900/[0.03]">
          {Array.from({ length: cols }).map((_, c) => (
            <td key={c} className="py-4 px-6">
              <SkeletonBox
                width={c === 0 ? 'w-36' : c === cols - 1 ? 'w-20' : 'w-24'}
                height="h-4"
              />
            </td>
          ))}
        </tr>
      ))}
    </>
  )
}
