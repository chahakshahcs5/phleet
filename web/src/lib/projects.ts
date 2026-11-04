import type { ProjectView } from '../types'

export function aggregateStatus(p: ProjectView): string {
  if (p.services && p.services.length > 0) {
    const states = p.services.map(aggregateStatus)
    if (states.some((s) => s === 'deploying')) return 'deploying'
    if (states.some((s) => s === 'failed' || s === 'crashed' || s === 'degraded')) return 'degraded'
    if (states.every((s) => s === 'running')) return 'running'
    if (states.every((s) => s === 'stopped')) return 'stopped'
    if (states.some((s) => s === 'running')) return 'degraded'
    return states[0] || 'pending'
  }
  if (p.placements.length === 0) return 'unplaced'
  const states = p.placements.map((x) => x.status)
  if (states.some((s) => s === 'deploying')) return 'deploying'
  if (states.some((s) => s === 'running')) return 'running'
  if (states.every((s) => s === 'stopped')) return 'stopped'
  if (states.some((s) => s === 'failed' || s === 'crashed')) return 'degraded'
  return states[0] || 'pending'
}

export function onlineReplicas(p: ProjectView): number {
  if (p.services && p.services.length > 0) {
    return p.services.reduce((sum, service) => sum + onlineReplicas(service), 0)
  }
  return p.placements.filter((pl) => pl.online).length
}

export function primaryService(p: ProjectView): ProjectView | undefined {
  if (!p.services || p.services.length === 0) return p
  return p.services.find((service) => service.hostname) ?? p.services[0]
}
