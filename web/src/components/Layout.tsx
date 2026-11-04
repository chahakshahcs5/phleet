import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Activity,
  AppWindow,
  Bell,
  Blocks,
  Boxes,
  ChevronRight,
  Clock,
  LayoutDashboard,
  LogOut,
  Palette,
  Plus,
  Settings,
  Smartphone,
  Terminal,
} from 'lucide-react'
import { useState } from 'react'
import { Link, NavLink, Outlet, useNavigate } from 'react-router-dom'
import { api } from '../api'
import { ago } from '../lib/format'
import { cn, StatusDot } from './ui'
import ThemeToggle from './ThemeToggle'
import ThemeStudio from './ThemeStudio'

function NavItem({
  to,
  icon,
  label,
  end,
  badge,
}: {
  to: string
  icon: React.ReactNode
  label: string
  end?: boolean
  badge?: number
}) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        cn(
          'flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm transition-colors',
          isActive
            ? 'bg-zinc-800/70 text-zinc-100'
            : 'text-zinc-400 hover:bg-zinc-900 hover:text-zinc-200',
        )
      }
    >
      <span className="text-zinc-500">{icon}</span>
      <span className="flex-1">{label}</span>
      {badge ? (
        <span className="rounded-md bg-amber-500/15 px-1.5 py-0.5 font-mono text-[10px] text-amber-300">{badge}</span>
      ) : null}
    </NavLink>
  )
}

export default function Layout() {
  const me = useQuery({ queryKey: ['me'], queryFn: api.me })
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers, refetchInterval: 15000 })
  const alertCount = useQuery({ queryKey: ['alert-count'], queryFn: api.alertCount, refetchInterval: 20000 })
  const [studioOpen, setStudioOpen] = useState(false)
  const qc = useQueryClient()
  const navigate = useNavigate()

  const logout = async () => {
    await api.logout()
    qc.clear()
    navigate('/')
    qc.invalidateQueries({ queryKey: ['me'] })
  }

  const publicUrl = me.data?.publicUrl || ''

  return (
    <div className="flex h-full">
      <aside className="hidden w-60 shrink-0 flex-col border-r border-zinc-800/70 bg-zinc-950/70 md:flex">
        <div className="flex items-center gap-2.5 px-4 pt-5 pb-4">
          <div className="grid h-8 w-8 place-items-center rounded-lg bg-emerald-400/10 ring-1 ring-emerald-400/30">
            <div className="grid grid-cols-2 gap-[3px]">
              <span className="h-1 w-1 rounded-full bg-emerald-400" />
              <span className="h-1 w-1 rounded-full bg-emerald-400/60" />
              <span className="h-1 w-1 rounded-full bg-emerald-400/40" />
              <span className="h-1 w-1 rounded-full" style={{ backgroundColor: 'var(--accent-2)' }} />
            </div>
          </div>
          <div className="min-w-0">
            <div className="text-sm font-semibold tracking-tight text-zinc-100">phleet</div>
            <div className="truncate text-[10px] text-zinc-500">
              {me.data ? `v${me.data.version}` : 'control plane'}
            </div>
          </div>
        </div>

        <nav className="space-y-0.5 px-2">
          <NavItem to="/" end icon={<LayoutDashboard size={15} />} label="Fleet" />
          <NavItem to="/projects" icon={<Boxes size={15} />} label="Projects" />
          <NavItem to="/services" icon={<AppWindow size={15} />} label="Services" />
          <NavItem to="/addons" icon={<Blocks size={15} />} label="Addons" />
          <NavItem to="/tasks" icon={<Clock size={15} />} label="Tasks" />
          <NavItem to="/alerts" icon={<Bell size={15} />} label="Alerts" badge={alertCount.data?.count} />
          <NavItem to="/events" icon={<Activity size={15} />} label="Events" />
          <NavItem to="/settings" icon={<Settings size={15} />} label="Settings" />
        </nav>

        <div className="mt-6 flex items-center justify-between px-4 pb-2">
          <span className="text-[10px] font-medium tracking-widest text-zinc-600 uppercase">
            Phones
          </span>
          <div className="flex items-center gap-2">
            <span className="text-[10px] text-zinc-600">
              {servers.data ? `${servers.data.servers.filter((s) => s.online).length}/${servers.data.servers.length}` : '—'}
            </span>
            <Link
              to="/servers/add"
              title="Add phone"
              className="rounded p-0.5 text-zinc-500 transition-colors hover:bg-zinc-800 hover:text-emerald-300"
            >
              <Plus size={13} />
            </Link>
          </div>
        </div>
        <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2 pb-4">
          {servers.data?.servers.map((s) => (
            <NavLink
              key={s.id}
              to={`/servers/${s.id}`}
              className={({ isActive }) =>
                cn(
                  'group flex items-center gap-2 rounded-lg px-2.5 py-2 text-xs transition-colors',
                  isActive
                    ? 'bg-zinc-800/60 text-zinc-100'
                    : 'text-zinc-400 hover:bg-zinc-900 hover:text-zinc-200',
                )
              }
            >
              <StatusDot online={s.online} />
              <span className="min-w-0 flex-1 truncate">{s.name}</span>
              <ChevronRight size={12} className="text-zinc-700 group-hover:text-zinc-500" />
            </NavLink>
          ))}
          {servers.data && servers.data.servers.length === 0 ? (
            <div className="px-2.5 py-2 text-[11px] leading-relaxed text-zinc-600">
              No phones yet. Run the bootstrap script on a device.
            </div>
          ) : null}
        </div>

        <div className="border-t border-zinc-800/70 p-3">
          <div className="mb-2 flex items-center justify-between px-1">
            <span className="text-[10px] text-zinc-600">Theme</span>
            <div className="flex items-center gap-1">
              <ThemeToggle />
              <button
                type="button"
                onClick={() => setStudioOpen(true)}
                title="Theme studio"
                aria-label="Open theme studio"
                className="rounded-md p-1.5 text-zinc-500 transition-colors hover:bg-zinc-800/60 hover:text-emerald-300"
              >
                <Palette size={13} />
              </button>
            </div>
          </div>
          <div className="rounded-lg bg-zinc-900/60 p-2.5">
            <div className="flex items-center gap-1.5 text-[10px] tracking-widest text-zinc-500 uppercase">
              <Terminal size={11} /> edge
            </div>
            <div className="mt-1 truncate font-mono text-[10px] text-zinc-400" title={publicUrl}>
              {publicUrl ? publicUrl.replace(/^https?:\/\//, '') : 'local only'}
            </div>
          </div>
          <button
            onClick={logout}
            className="mt-2 flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-xs text-zinc-500 transition-colors hover:bg-zinc-900 hover:text-zinc-200"
          >
            <LogOut size={13} /> Sign out
          </button>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center justify-between border-b border-zinc-800/70 bg-zinc-950/80 px-4 py-3 md:hidden">
          <div className="flex items-center gap-2">
            <Smartphone size={15} className="text-emerald-400" />
            <span className="text-sm font-semibold">phleet</span>
          </div>
          <nav className="flex items-center gap-1">
            <NavLink to="/" end className="rounded-md p-2 text-zinc-400 aria-[current=page]:text-emerald-400">
              <LayoutDashboard size={16} />
            </NavLink>
            <NavLink to="/projects" className="rounded-md p-2 text-zinc-400 aria-[current=page]:text-emerald-400">
              <Boxes size={16} />
            </NavLink>
            <NavLink to="/addons" className="rounded-md p-2 text-zinc-400 aria-[current=page]:text-emerald-400">
              <Blocks size={16} />
            </NavLink>
            <NavLink to="/tasks" className="rounded-md p-2 text-zinc-400 aria-[current=page]:text-emerald-400">
              <Clock size={16} />
            </NavLink>
            <NavLink to="/alerts" className="rounded-md p-2 text-zinc-400 aria-[current=page]:text-emerald-400">
              <Bell size={16} />
            </NavLink>
            <NavLink to="/events" className="rounded-md p-2 text-zinc-400 aria-[current=page]:text-emerald-400">
              <Activity size={16} />
            </NavLink>
            <NavLink to="/settings" className="rounded-md p-2 text-zinc-400 aria-[current=page]:text-emerald-400">
              <Settings size={16} />
            </NavLink>
            <button
              type="button"
              onClick={() => setStudioOpen(true)}
              className="rounded-md p-2 text-zinc-400 hover:text-emerald-400"
              aria-label="Open theme studio"
            >
              <Palette size={16} />
            </button>
          </nav>
        </header>
        <main className="min-h-0 flex-1 overflow-y-auto">
          <Outlet />
        </main>
        <footer className="hidden shrink-0 items-center justify-between border-t border-zinc-800/60 px-6 py-2 text-[10px] text-zinc-600 md:flex">
          <span>
            {servers.data
              ? `${servers.data.servers.filter((s) => s.online).length} online · ${servers.data.servers.length} registered`
              : 'loading fleet…'}
          </span>
          <span>last sync {servers.dataUpdatedAt ? ago(servers.dataUpdatedAt / 1000) : '—'}</span>
        </footer>
      </div>
      <ThemeStudio open={studioOpen} onClose={() => setStudioOpen(false)} />
    </div>
  )
}
