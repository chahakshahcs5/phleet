import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Activity, ChevronLeft, ChevronRight, RefreshCw, Search, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api } from '../api'
import { Badge, Button, EmptyState, Spinner } from '../components/ui'
import { ago, shortId, time } from '../lib/format'

const PAGE_SIZES = [25, 50, 100, 200]

const control =
  'h-9 rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none focus:border-emerald-400/40'

function toneFor(action: string): 'good' | 'bad' | 'warn' | 'info' | 'neutral' {
  if (action.includes('offline') || action.includes('delete') || action.includes('failed')) return 'bad'
  if (action.includes('online') || action.includes('write')) return 'good'
  if (action.includes('exec') || action.includes('terminal')) return 'info'
  if (action.includes('upload') || action.includes('mkdir')) return 'warn'
  return 'neutral'
}

export default function Events() {
  const [pageSize, setPageSize] = useState(50)
  const [action, setAction] = useState('')
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  // One cursor per visited page. The last entry is the `before` id for the
  // current page, so "Previous" is just a pop and new events never shift rows.
  const [cursors, setCursors] = useState<number[]>([0])

  useEffect(() => {
    const next = search.trim()
    if (next === query) return
    const timer = setTimeout(() => {
      setQuery(next)
      setCursors([0])
    }, 300)
    return () => clearTimeout(timer)
  }, [search, query])

  const before = cursors[cursors.length - 1] ?? 0
  const q = useQuery({
    queryKey: ['events', pageSize, before, action, query],
    queryFn: () =>
      api.events({
        limit: pageSize,
        before: before || undefined,
        action: action || undefined,
        q: query || undefined,
      }),
    placeholderData: keepPreviousData,
    refetchInterval: 15000,
  })
  const facets = useQuery({ queryKey: ['event-facets'], queryFn: api.eventFacets, staleTime: 5 * 60 * 1000 })

  const events = q.data?.events ?? []
  const page = q.data?.page
  const pageIndex = cursors.length - 1
  const first = events.length === 0 ? 0 : pageIndex * pageSize + 1
  const last = events.length === 0 ? 0 : pageIndex * pageSize + events.length
  const filtering = !!action || !!query

  const reset = () => setCursors([0])

  return (
    <div className="mx-auto max-w-5xl px-4 py-6 md:px-8 md:py-8">
      <div className="flex items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-zinc-100">Events</h1>
          <p className="mt-0.5 text-xs text-zinc-500">Agent connections, commands and file changes across the fleet.</p>
        </div>
        <Button size="sm" variant="ghost" onClick={() => q.refetch()}>
          {q.isFetching ? <Spinner className="h-3 w-3" /> : <RefreshCw size={13} />} Refresh
        </Button>
      </div>

      <div className="mt-5 flex flex-wrap items-center gap-2">
        <div className="relative min-w-[220px] flex-1">
          <Search size={13} className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-zinc-600" />
          <input
            className={`${control} w-full pl-8 ${search ? 'pr-8' : ''}`}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search action, target or detail"
            aria-label="Search events"
          />
          {search ? (
            <button
              type="button"
              aria-label="Clear search"
              onClick={() => setSearch('')}
              className="absolute top-1/2 right-2 -translate-y-1/2 rounded p-1 text-zinc-500 hover:text-zinc-200"
            >
              <X size={12} />
            </button>
          ) : null}
        </div>

        <select
          className={control}
          value={action}
          onChange={(e) => {
            setAction(e.target.value)
            reset()
          }}
          aria-label="Filter by action"
        >
          <option value="">All actions</option>
          {(facets.data?.actions ?? []).map((a) => (
            <option key={a} value={a}>
              {a}
            </option>
          ))}
        </select>

        <select
          className={control}
          value={pageSize}
          onChange={(e) => {
            setPageSize(Number(e.target.value))
            reset()
          }}
          aria-label="Events per page"
        >
          {PAGE_SIZES.map((n) => (
            <option key={n} value={n}>
              {n} per page
            </option>
          ))}
        </select>
      </div>

      <div className="mt-3 overflow-hidden rounded-xl border border-zinc-800/80">
        <div className="grid grid-cols-[90px_110px_1fr_110px] gap-3 border-b border-zinc-800/70 bg-zinc-950/50 px-4 py-2 text-[10px] tracking-wider text-zinc-500 uppercase">
          <span>when</span>
          <span>action</span>
          <span>detail</span>
          <span className="text-right">target</span>
        </div>
        <div className="divide-y divide-zinc-800/50">
          {q.isLoading ? (
            <div className="flex items-center gap-2 p-8 text-xs text-zinc-500">
              <Spinner /> loading…
            </div>
          ) : events.length === 0 ? (
            <div className="p-6">
              <EmptyState icon={<Activity size={20} />} title={filtering ? 'No matching events' : 'No events yet'}>
                {filtering
                  ? 'Try a different search, action or page size.'
                  : 'Register a phone to see agent connections, commands and file operations here.'}
              </EmptyState>
            </div>
          ) : (
            events.map((e) => (
              <div
                key={e.id}
                className="grid grid-cols-[90px_110px_1fr_110px] items-center gap-3 px-4 py-2 hover:bg-zinc-900/40"
              >
                <span className="text-[10px] text-zinc-500" title={time(e.ts)}>
                  {ago(e.ts)}
                </span>
                <span>
                  <Badge tone={toneFor(e.action)}>{e.action}</Badge>
                </span>
                <span className="min-w-0 truncate font-mono text-[10px] text-zinc-400" title={e.detail}>
                  {e.detail || '—'}
                </span>
                <span className="truncate text-right font-mono text-[10px] text-zinc-600" title={e.target}>
                  {shortId(e.target)}
                </span>
              </div>
            ))
          )}
        </div>

        <div className="flex flex-col gap-2 border-t border-zinc-800/70 bg-zinc-950/30 px-4 py-2.5 sm:flex-row sm:items-center sm:justify-between">
          <span className="text-[10px] text-zinc-500">
            {events.length === 0
              ? filtering
                ? 'No matching events'
                : 'No events yet'
              : `Showing ${first.toLocaleString()}–${last.toLocaleString()} of ${(page?.total ?? 0).toLocaleString()} events`}
          </span>
          <div className="flex items-center gap-1.5">
            <span className="mr-1 font-mono text-[10px] text-zinc-500">Page {pageIndex + 1}</span>
            <Button
              size="sm"
              variant="ghost"
              disabled={pageIndex === 0 || q.isFetching}
              onClick={() => setCursors((c) => (c.length > 1 ? c.slice(0, -1) : c))}
            >
              <ChevronLeft size={13} /> Prev
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={!page?.hasMore || q.isFetching}
              onClick={() => {
                const next = page?.nextBefore
                if (next) setCursors((c) => [...c, next])
              }}
            >
              Next <ChevronRight size={13} />
            </Button>
          </div>
        </div>
      </div>

      {filtering ? (
        <button
          type="button"
          onClick={() => {
            setAction('')
            setSearch('')
            setQuery('')
            reset()
          }}
          className="mt-2 text-[10px] text-zinc-500 underline-offset-2 hover:text-zinc-300 hover:underline"
        >
          Clear filters
        </button>
      ) : null}
    </div>
  )
}
