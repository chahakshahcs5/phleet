export function bytes(n: number | undefined | null): string {
  if (n === undefined || n === null || !isFinite(n) || n <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`
}

export function pct(n: number | undefined | null): string {
  if (n === undefined || n === null || !isFinite(n)) return '—'
  return `${n.toFixed(0)}%`
}

export function temp(n: number | undefined | null): string {
  if (!n) return '—'
  return `${n.toFixed(1)}°C`
}

export function uptime(seconds: number | undefined | null): string {
  if (!seconds || seconds <= 0) return '—'
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

export function ago(ts: number | undefined | null): string {
  if (!ts) return 'never'
  const s = Math.max(0, Math.floor(Date.now() / 1000 - ts))
  if (s < 5) return 'just now'
  if (s < 60) return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}

export function time(ts: number | undefined | null): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleString()
}

export function shortId(id: string | undefined | null): string {
  if (!id) return ''
  return id.slice(0, 8)
}
