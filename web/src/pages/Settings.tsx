import { useQuery } from '@tanstack/react-query'
import { Cloud, HardDrive, Shield, SlidersHorizontal, Smartphone, Users } from 'lucide-react'
import type { ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api } from '../api'
import type { Me } from '../types'
import { Badge, Tabs } from '../components/ui'
import AccessTab from './settings/AccessTab'
import BackupsTab from './settings/BackupsTab'
import DevicesTab from './settings/DevicesTab'
import EdgeTab from './settings/EdgeTab'
import GeneralTab from './settings/GeneralTab'
import SecurityTab from './settings/SecurityTab'

type Tab = 'general' | 'access' | 'security' | 'edge' | 'backups' | 'devices'

const allTabs: { id: Tab; label: string; icon: ReactNode; admin?: boolean }[] = [
  { id: 'general', label: 'General', icon: <SlidersHorizontal size={13} /> },
  { id: 'access', label: 'Users & tokens', icon: <Users size={13} />, admin: true },
  { id: 'security', label: 'Security', icon: <Shield size={13} /> },
  { id: 'edge', label: 'Edge', icon: <Cloud size={13} />, admin: true },
  { id: 'backups', label: 'Backups', icon: <HardDrive size={13} />, admin: true },
  { id: 'devices', label: 'Devices', icon: <Smartphone size={13} /> },
]

function roleTone(role?: Me['role']) {
  return role === 'admin' ? 'warn' : role === 'deployer' ? 'info' : 'neutral'
}

export default function Settings() {
  const me = useQuery({ queryKey: ['me'], queryFn: api.me })
  const [searchParams, setSearchParams] = useSearchParams()
  const isAdmin = me.data?.role === 'admin'
  const tabs = allTabs.filter((t) => !t.admin || isAdmin)
  const requested = searchParams.get('tab') as Tab | null
  const tab = requested && tabs.some((t) => t.id === requested) ? requested : 'general'
  const setTab = (id: Tab) => setSearchParams(id === 'general' ? {} : { tab: id }, { replace: true })
  const base = me.data?.publicUrl || 'https://fleet.example.com'
  const dataDir = me.data?.dataDir || '%ProgramData%\\phleet'

  return (
    <div className="mx-auto max-w-4xl px-4 py-6 md:px-8 md:py-8">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-zinc-100">Settings</h1>
          <p className="mt-0.5 text-xs text-zinc-500">Control plane, access and edge configuration.</p>
        </div>
        {me.data ? (
          <div className="flex items-center gap-2 text-[11px] text-zinc-500">
            <span>{me.data.username}</span>
            <Badge tone={roleTone(me.data.role)}>{me.data.role ?? 'admin'}</Badge>
          </div>
        ) : null}
      </div>

      <Tabs tabs={tabs} value={tab} onChange={setTab} className="mt-4" />

      <div className="mt-4">
        {tab === 'general' ? <GeneralTab me={me.data} /> : null}
        {tab === 'access' ? <AccessTab /> : null}
        {tab === 'security' ? <SecurityTab /> : null}
        {tab === 'edge' ? <EdgeTab /> : null}
        {tab === 'backups' ? <BackupsTab /> : null}
        {tab === 'devices' ? <DevicesTab base={base} dataDir={dataDir} /> : null}
      </div>

      {me.data && !isAdmin ? (
        <p className="mt-4 text-[10px] leading-relaxed text-zinc-600">
          Users, API tokens, edge and backup controls require an admin session.
        </p>
      ) : null}
    </div>
  )
}
