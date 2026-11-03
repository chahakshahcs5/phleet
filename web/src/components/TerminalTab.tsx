import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { Eraser, RotateCw, Terminal as TerminalIcon } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { terminalUrl, textToBase64, bytesFromBase64 } from '../api'
import { useTheme } from '../theme'
import { Badge, Button, cn } from './ui'

const QUICK = [
  { label: 'battery', cmd: 'termux-battery-status 2>/dev/null || cat /sys/class/power_supply/battery/capacity\n' },
  { label: 'disk', cmd: 'df -h ~\n' },
  { label: 'uptime', cmd: 'uptime\n' },
  { label: 'top', cmd: 'top -n 1 -b 2>/dev/null | head -20\n' },
  { label: 'wake-lock', cmd: 'termux-wake-lock && echo wake lock acquired\n' },
]

const LIGHT_TERM_THEME = {
  background: '#f6f7f8',
  foreground: '#27272a',
  cursor: '#047857',
  cursorAccent: '#f6f7f8',
  selectionBackground: 'rgba(4,120,87,0.18)',
  black: '#18181b',
  red: '#b91c1c',
  green: '#047857',
  yellow: '#b45309',
  blue: '#1d4ed8',
  magenta: '#7e22ce',
  cyan: '#0e7490',
  white: '#3f3f46',
  brightBlack: '#71717a',
  brightRed: '#dc2626',
  brightGreen: '#059669',
  brightYellow: '#d97706',
  brightBlue: '#2563eb',
  brightMagenta: '#9333ea',
  brightCyan: '#0891b2',
  brightWhite: '#18181b',
}

const DARK_TERM_THEME = {
  background: '#08080a',
  foreground: '#d4d4d8',
  cursor: '#34d399',
  cursorAccent: '#08080a',
  selectionBackground: 'rgba(52,211,153,0.22)',
  black: '#18181b',
  red: '#f87171',
  green: '#34d399',
  yellow: '#fbbf24',
  blue: '#60a5fa',
  magenta: '#c084fc',
  cyan: '#22d3ee',
  white: '#e4e4e7',
  brightBlack: '#52525b',
  brightRed: '#fca5a5',
  brightGreen: '#6ee7b7',
  brightYellow: '#fcd34d',
  brightBlue: '#93c5fd',
  brightMagenta: '#d8b4fe',
  brightCyan: '#67e8f9',
  brightWhite: '#fafafa',
}

function termTheme(resolved: 'light' | 'dark') {
  const base = resolved === 'light' ? LIGHT_TERM_THEME : DARK_TERM_THEME
  const accent =
    typeof window === 'undefined'
      ? base.cursor
      : getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || base.cursor
  return {
    ...base,
    cursor: accent,
    cursorAccent: base.background,
    selectionBackground: `${accent}33`,
  }
}

export default function TerminalTab({ id, online }: { id: string; online: boolean }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const [status, setStatus] = useState<'connecting' | 'open' | 'closed'>('connecting')
  const [nonce, setNonce] = useState(0)
  const { resolved, palette } = useTheme()

  useEffect(() => {
    if (!online || !containerRef.current) {
      setStatus('closed')
      return
    }
    const el = containerRef.current
    el.innerHTML = ''
    setStatus('connecting')

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily:
        'ui-monospace, "SFMono-Regular", "JetBrains Mono", Menlo, Consolas, "Liberation Mono", monospace',
      lineHeight: 1.25,
      scrollback: 5000,
      theme: termTheme(resolved),
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(el)
    termRef.current = term
    const fitNow = () => {
      try {
        fit.fit()
      } catch {
        /* container not measurable yet */
      }
    }
    requestAnimationFrame(fitNow)

    const ws = new WebSocket(terminalUrl(id))
    wsRef.current = ws
    ws.onopen = () => {
      setStatus('open')
      fitNow()
      ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
    }
    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(String(ev.data)) as { type: string; b64?: string; code?: number }
        if (msg.type === 'output' && msg.b64) {
          term.write(bytesFromBase64(msg.b64))
        } else if (msg.type === 'exit') {
          setStatus('closed')
          term.write(`\r\n\x1b[90m[session ended, code ${msg.code ?? 0}]\x1b[0m\r\n`)
        }
      } catch {
        /* ignore malformed frames */
      }
    }
    ws.onclose = () => setStatus('closed')
    ws.onerror = () => setStatus('closed')

    const dataSub = term.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'input', data: textToBase64(d) }))
      }
    })
    const resizeSub = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    })
    const ro = new ResizeObserver(fitNow)
    ro.observe(el)
    const ping = window.setInterval(() => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'ping' }))
    }, 20000)

    return () => {
      window.clearInterval(ping)
      ro.disconnect()
      dataSub.dispose()
      resizeSub.dispose()
      ws.close()
      term.dispose()
      termRef.current = null
      wsRef.current = null
      el.innerHTML = ''
    }
  }, [id, online, nonce])

  useEffect(() => {
    if (termRef.current) {
      termRef.current.options.theme = termTheme(resolved)
    }
  }, [resolved, palette.primary])

  const send = (text: string) => {
    const ws = wsRef.current
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    ws.send(JSON.stringify({ type: 'input', data: textToBase64(text) }))
    termRef.current?.focus()
  }

  return (
    <div className="flex h-[calc(100vh-250px)] min-h-[420px] flex-col overflow-hidden rounded-xl border border-zinc-800/80 bg-zinc-950">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-zinc-800/80 bg-zinc-950/70 px-3 py-2">
        <div className="flex items-center gap-2">
          <TerminalIcon size={13} className="text-zinc-500" />
          <span className="text-[11px] text-zinc-400">termux shell</span>
          <Badge tone={status === 'open' ? 'good' : status === 'connecting' ? 'warn' : 'bad'}>
            {status === 'open' ? 'connected' : status === 'connecting' ? 'connecting' : 'disconnected'}
          </Badge>
          {!online ? <span className="text-[10px] text-zinc-600">phone offline</span> : null}
        </div>
        <div className="flex flex-wrap items-center gap-1">
          {QUICK.map((q) => (
            <button
              key={q.label}
              onClick={() => send(q.cmd)}
              disabled={status !== 'open'}
              className="rounded-md bg-zinc-900 px-2 py-1 font-mono text-[10px] text-zinc-400 ring-1 ring-zinc-800 transition-colors hover:text-emerald-300 disabled:opacity-40"
            >
              {q.label}
            </button>
          ))}
          <Button size="icon" variant="ghost" title="Clear" onClick={() => termRef.current?.clear()}>
            <Eraser size={13} />
          </Button>
          <Button
            size="icon"
            variant="ghost"
            title="Reconnect"
            disabled={!online}
            onClick={() => setNonce((n) => n + 1)}
          >
            <RotateCw size={13} />
          </Button>
        </div>
      </div>
      <div className={cn('min-h-0 flex-1 p-2', status === 'closed' && 'opacity-70')} ref={containerRef} />
      <div className="border-t border-zinc-800/80 px-3 py-1.5 text-[10px] text-zinc-600">
        The session ends when you leave this tab. Commands run as the agent user; nothing is exposed to the network.
      </div>
    </div>
  )
}
