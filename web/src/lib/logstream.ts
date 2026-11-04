import { useEffect, useRef, useState } from 'react'

const MAX_CLIENT_LOG = 256 * 1024

type StreamState = {
  logs: string
  path: string
  size: number
  truncated: boolean
  error: string
  connected: boolean
}

const empty: StreamState = {
  logs: '',
  path: '',
  size: 0,
  truncated: false,
  error: '',
  connected: false,
}

// useAppLogStream follows a project's app.log over a WebSocket. The control
// plane polls the phone once a second and pushes initial, chunk, reset and error
// messages, so the browser never needs to poll the REST endpoint.
export function useAppLogStream(projectId: string, serverId: string, enabled: boolean) {
  const [state, setState] = useState<StreamState>(empty)
  const [nonce, setNonce] = useState(0)
  const wsRef = useRef<WebSocket | null>(null)

  useEffect(() => {
    if (!enabled || !projectId || !serverId) {
      setState(empty)
      return
    }
    let closed = false
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${proto}//${location.host}/api/projects/${projectId}/logs/stream?serverId=${encodeURIComponent(serverId)}`
    const ws = new WebSocket(url)
    wsRef.current = ws
    setState((prev) => ({ ...prev, error: '', connected: false }))

    ws.onopen = () => {
      if (!closed) setState((prev) => ({ ...prev, connected: true }))
    }
    ws.onmessage = (event) => {
      if (closed) return
      let m: { type?: string; logs?: string; path?: string; size?: number; truncated?: boolean; data?: string; error?: string }
      try {
        m = JSON.parse(String(event.data))
      } catch {
        return
      }
      switch (m.type) {
        case 'initial':
          setState((prev) => ({
            ...prev,
            logs: m.logs ?? '',
            path: m.path ?? '',
            size: m.size ?? 0,
            truncated: !!m.truncated,
            error: '',
          }))
          break
        case 'chunk':
          setState((prev) => {
            const next = prev.logs + (m.data ?? '')
            return {
              ...prev,
              logs: next.length > MAX_CLIENT_LOG ? next.slice(-MAX_CLIENT_LOG) : next,
              size: prev.size + (m.data?.length ?? 0),
              connected: true,
            }
          })
          break
        case 'reset':
          setState((prev) => ({ ...prev, logs: '', size: m.size ?? 0, truncated: false }))
          break
        case 'error':
          setState((prev) => ({ ...prev, error: m.error || 'log stream failed', connected: false }))
          break
      }
    }
    ws.onerror = () => {
      if (!closed) setState((prev) => ({ ...prev, error: 'log stream connection failed', connected: false }))
    }
    ws.onclose = () => {
      if (!closed) setState((prev) => ({ ...prev, connected: false }))
    }

    return () => {
      closed = true
      wsRef.current = null
      ws.close()
    }
  }, [projectId, serverId, enabled, nonce])

  return {
    ...state,
    reconnect: () => setNonce((n) => n + 1),
  }
}
