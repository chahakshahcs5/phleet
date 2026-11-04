import { useQuery } from '@tanstack/react-query'
import { Navigate, Routes, Route } from 'react-router-dom'
import { api } from './api'
import Layout from './components/Layout'
import Alerts from './pages/Alerts'
import AddPhone from './pages/AddPhone'
import Dashboard from './pages/Dashboard'
import Addons from './pages/Addons'
import AddonDetail from './pages/AddonDetail'
import Services from './pages/Services'
import ServiceDetail from './pages/ServiceDetail'
import Events from './pages/Events'
import Login from './pages/Login'
import Projects from './pages/Projects'
import ProjectDetail from './pages/ProjectDetail'
import StackDetail from './pages/StackDetail'
import ServerDetail from './pages/ServerDetail'
import Settings from './pages/Settings'
import Tasks from './pages/Tasks'
import { Button, FullScreenLoader } from './components/ui'

export default function App() {
  const me = useQuery({ queryKey: ['me'], queryFn: api.me, retry: false, staleTime: 30_000 })

  if (me.isLoading) {
    return <FullScreenLoader label="Connecting to phleet" />
  }

  if (me.isError) {
    const status = (me.error as { status?: number }).status
    if (status === 401) return <Login />
    return (
      <div className="grid h-full place-items-center px-6">
        <div className="max-w-sm text-center">
          <div className="text-sm font-medium text-zinc-200">Control plane unreachable</div>
          <p className="mt-2 text-xs leading-relaxed text-zinc-500">
            {String((me.error as Error).message || me.error)}
          </p>
          <Button className="mt-4" onClick={() => me.refetch()}>
            Retry
          </Button>
        </div>
      </div>
    )
  }

  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Dashboard />} />
        <Route path="projects" element={<Projects />} />
        <Route path="projects/:id" element={<ProjectDetail />} />
        <Route path="stacks/:id" element={<StackDetail />} />
        <Route path="services" element={<Services />} />
        <Route path="services/:id" element={<ServiceDetail />} />
        <Route path="addons" element={<Addons />} />
        <Route path="addons/:id" element={<AddonDetail />} />
        {/* Old links: the page was called Data before addons were generalised. */}
        <Route path="data" element={<Navigate to="/addons" replace />} />
        <Route path="data/:id" element={<Navigate to="/addons" replace />} />
        <Route path="alerts" element={<Alerts />} />
        <Route path="tasks" element={<Tasks />} />
        <Route path="servers/add" element={<AddPhone />} />
        <Route path="servers/:id" element={<ServerDetail />} />
        <Route path="events" element={<Events />} />
        <Route path="settings" element={<Settings />} />
        <Route
          path="*"
          element={
            <div className="grid h-full place-items-center text-sm text-zinc-500">Not found</div>
          }
        />
      </Route>
    </Routes>
  )
}
