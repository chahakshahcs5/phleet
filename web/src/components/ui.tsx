import { clsx, type ClassValue } from 'clsx'
import type { ButtonHTMLAttributes, ReactNode } from 'react'

export function cn(...inputs: ClassValue[]) {
  return clsx(inputs)
}

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <div
      className={cn(
        'rounded-xl border border-zinc-800/80 bg-zinc-900/40 backdrop-blur-sm',
        className,
      )}
    >
      {children}
    </div>
  )
}

type Variant = 'primary' | 'ghost' | 'subtle' | 'danger' | 'outline'
type Size = 'sm' | 'md' | 'icon'

export function Button({
  variant = 'subtle',
  size = 'md',
  className,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: Size }) {
  return (
    <button
      className={cn(
        'inline-flex items-center justify-center gap-2 rounded-lg font-medium transition-colors disabled:pointer-events-none disabled:opacity-45',
        size === 'sm' && 'h-8 px-2.5 text-xs',
        size === 'md' && 'h-9 px-3.5 text-sm',
        size === 'icon' && 'h-8 w-8',
        variant === 'primary' && 'bg-emerald-400 text-[var(--accent-foreground)] hover:bg-emerald-300',
        variant === 'subtle' && 'bg-zinc-800/80 text-zinc-200 hover:bg-zinc-700/80',
        variant === 'ghost' && 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-100',
        variant === 'outline' &&
          'border border-zinc-700/80 text-zinc-300 hover:border-zinc-600 hover:text-zinc-100',
        variant === 'danger' && 'bg-red-500/10 text-red-300 hover:bg-red-500/20',
        className,
      )}
      {...props}
    />
  )
}

export function Badge({
  children,
  tone = 'neutral',
  className,
}: {
  children: ReactNode
  tone?: 'neutral' | 'good' | 'warn' | 'bad' | 'info'
  className?: string
}) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] font-medium tracking-wide uppercase',
        tone === 'neutral' && 'bg-zinc-800/80 text-zinc-400',
        tone === 'good' && 'bg-emerald-500/10 text-emerald-300',
        tone === 'warn' && 'bg-amber-500/10 text-amber-300',
        tone === 'bad' && 'bg-red-500/10 text-red-300',
        tone === 'info' && 'bg-sky-500/10 text-sky-300',
        className,
      )}
    >
      {children}
    </span>
  )
}

export function StatusDot({ online, className }: { online: boolean; className?: string }) {
  return (
    <span className={cn('relative inline-flex h-2 w-2', className)}>
      <span
        className={cn(
          'inline-flex h-2 w-2 rounded-full',
          online ? 'bg-emerald-400 pulse-dot' : 'bg-zinc-600',
        )}
      />
    </span>
  )
}

export function Spinner({ className }: { className?: string }) {
  return (
    <span
      className={cn(
        'inline-block h-4 w-4 animate-spin rounded-full border-2 border-zinc-700 border-t-emerald-400',
        className,
      )}
    />
  )
}

export function EmptyState({
  icon,
  title,
  children,
}: {
  icon?: ReactNode
  title: string
  children?: ReactNode
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 rounded-xl border border-dashed border-zinc-800 px-6 py-14 text-center">
      {icon ? <div className="text-zinc-600">{icon}</div> : null}
      <div className="text-sm font-medium text-zinc-300">{title}</div>
      {children ? <div className="max-w-md text-xs leading-relaxed text-zinc-500">{children}</div> : null}
    </div>
  )
}

export function Sparkline({
  values,
  height = 36,
  stroke = 'var(--accent)',
  className,
}: {
  values: number[]
  height?: number
  stroke?: string
  className?: string
}) {
  const w = 100
  const h = height
  if (values.length < 2) {
    return <div className={cn('text-[10px] text-zinc-600', className)}>not enough data</div>
  }
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min || 1
  const step = w / (values.length - 1)
  const pts = values.map((v, i) => {
    const x = i * step
    const y = h - 3 - ((v - min) / span) * (h - 6)
    return `${x.toFixed(2)},${y.toFixed(2)}`
  })
  const line = `M${pts.join(' L')}`
  const area = `${line} L${w},${h} L0,${h} Z`
  return (
    <svg
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      className={cn('w-full', className)}
      style={{ height, color: stroke }}
      role="img"
      aria-hidden
    >
      <path d={area} fill="currentColor" opacity={0.09} />
      <path d={line} fill="none" stroke="currentColor" strokeWidth={1.2} vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

export function Bar({
  value,
  max,
  tone = 'emerald',
  className,
}: {
  value: number
  max: number
  tone?: 'emerald' | 'amber' | 'red' | 'sky'
  className?: string
}) {
  const p = max > 0 ? Math.min(100, Math.max(0, (value / max) * 100)) : 0
  return (
    <div className={cn('h-1.5 w-full overflow-hidden rounded-full bg-zinc-800', className)}>
      <div
        className={cn(
          'h-full rounded-full transition-all',
          tone === 'emerald' && 'bg-emerald-400/80',
          tone === 'amber' && 'bg-amber-400/80',
          tone === 'red' && 'bg-red-400/80',
          tone === 'sky' && 'bg-sky-400/80',
        )}
        style={{ width: `${p}%` }}
      />
    </div>
  )
}

export function KeyValue({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-1.5">
      <span className="text-xs text-zinc-500">{label}</span>
      <span className="truncate text-right text-xs text-zinc-300">{children}</span>
    </div>
  )
}

export function Tabs<T extends string>({
  tabs,
  value,
  onChange,
  className,
}: {
  tabs: { id: T; label: string; icon?: ReactNode; badge?: ReactNode }[]
  value: T
  onChange: (id: T) => void
  className?: string
}) {
  return (
    <div className={cn('flex gap-1 overflow-x-auto border-b border-zinc-800/70', className)}>
      {tabs.map((tab) => (
        <button
          key={tab.id}
          type="button"
          onClick={() => onChange(tab.id)}
          className={cn(
            'flex items-center gap-1.5 border-b-2 px-3 py-2 text-xs whitespace-nowrap transition-colors',
            value === tab.id
              ? 'border-emerald-400 text-zinc-100'
              : 'border-transparent text-zinc-500 hover:text-zinc-300',
          )}
        >
          {tab.icon}
          {tab.label}
          {tab.badge}
        </button>
      ))}
    </div>
  )
}

export function DeployBadge({ status }: { status?: string }) {
  const s = status || 'unknown'
  const tone: 'good' | 'warn' | 'bad' | 'info' | 'neutral' =
    s === 'running' || s === 'active'
      ? 'good'
      : s === 'failed' || s === 'crashed'
        ? 'bad'
        : s === 'rolled_back'
          ? 'info'
          : s === 'stopped' || s === 'pending' || s === 'unknown'
            ? 'neutral'
            : 'warn'
  return <Badge tone={tone}>{s.replace(/_/g, ' ')}</Badge>
}
