import { useQuery } from '@tanstack/react-query'
import { RefreshCw, Search } from 'lucide-react'
import { useState } from 'react'
import { api } from '../api'
import { Badge, Button, Spinner } from './ui'
import { bytes } from '../lib/format'

export default function ProcessesTab({ id, online }: { id: string; online: boolean }) {
  const [filter, setFilter] = useState('')
  const q = useQuery({
    queryKey: ['procs', id],
    queryFn: () => api.processes(id),
    refetchInterval: 10000,
    enabled: online,
  })

  const rows = (q.data?.processes ?? []).filter((p) => {
    if (!filter.trim()) return true
    const f = filter.toLowerCase()
    return p.command.toLowerCase().includes(f) || String(p.pid).includes(f) || p.user.toLowerCase().includes(f)
  })

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-0 flex-1 sm:max-w-xs">
          <Search size={13} className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-zinc-600" />
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="filter by command, pid or user"
            className="h-8 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 pr-2 pl-8 font-mono text-[11px] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40"
          />
        </div>
        <Badge>{rows.length} processes</Badge>
        <Button size="sm" variant="ghost" onClick={() => q.refetch()}>
          <RefreshCw size={12} /> Refresh
        </Button>
        {q.isFetching ? <Spinner /> : null}
      </div>

      {q.isError ? (
        <div className="rounded-lg border border-red-900/40 bg-red-500/5 px-3 py-2 text-[11px] text-red-300">
          {String((q.error as Error).message)}
        </div>
      ) : null}

      <div className="overflow-hidden rounded-xl border border-zinc-800/80">
        <div className="grid grid-cols-[70px_90px_80px_1fr] gap-2 border-b border-zinc-800/70 bg-zinc-950/50 px-3 py-1.5 text-[10px] tracking-wider text-zinc-500 uppercase">
          <span>pid</span>
          <span>user</span>
          <span className="text-right">rss</span>
          <span>command</span>
        </div>
        <div className="max-h-[calc(100vh-340px)] divide-y divide-zinc-800/50 overflow-y-auto">
          {q.isLoading ? (
            <div className="flex items-center gap-2 p-6 text-xs text-zinc-500">
              <Spinner /> reading /proc…
            </div>
          ) : rows.length === 0 ? (
            <div className="p-8 text-center text-[11px] text-zinc-600">no matching processes</div>
          ) : (
            rows.map((p) => (
              <div
                key={p.pid}
                className="grid grid-cols-[70px_90px_80px_1fr] items-center gap-2 px-3 py-1.5 font-mono text-[10px] hover:bg-zinc-800/30"
              >
                <span className="text-zinc-500">{p.pid}</span>
                <span className="truncate text-zinc-500">{p.user}</span>
                <span className="text-right text-zinc-400">{bytes(p.rss)}</span>
                <span className="truncate text-zinc-300" title={p.command}>
                  {p.command}
                </span>
              </div>
            ))
          )}
        </div>
      </div>
      <p className="text-[10px] text-zinc-600">
        Sorted by resident memory. Process control lands with the deploy supervisor.
      </p>
    </div>
  )
}
