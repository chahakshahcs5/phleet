import { Boxes, Check, Monitor, Moon, RotateCcw, Shuffle, Sun } from 'lucide-react'
import { useEffect, useState } from 'react'
import { cn, Button } from './ui'
import { Dialog } from './Dialog'
import { isHexColor, useTheme, type Palette } from '../theme'

const PRESETS: (Palette & { name: string })[] = [
  { name: 'Fleet Emerald', primary: '#047857', secondary: '#14b8a6', preset: 'mint-emerald' },
  { name: 'Control Indigo', primary: '#6366f1', secondary: '#a855f7', preset: 'electric-indigo' },
  { name: 'Signal Amber', primary: '#f59e0b', secondary: '#ef4444', preset: 'solar-flare' },
  { name: 'Alert Coral', primary: '#ef4444', secondary: '#22d3ee', preset: 'crimson-cyan' },
  { name: 'Neon Terminal', primary: '#ec4899', secondary: '#22d3ee', preset: 'cyberpunk-neon' },
  { name: 'Arctic Link', primary: '#0ea5e9', secondary: '#38bdf8', preset: 'polar-frost' },
  { name: 'Ultraviolet', primary: '#7c3aed', secondary: '#ec4899', preset: 'dracula-velvet' },
  { name: 'Graphite', primary: '#64748b', secondary: '#94a3b8', preset: 'titanium-slate' },
]

const SWATCHES = [
  '#ef4444',
  '#f97316',
  '#f59e0b',
  '#eab308',
  '#10b981',
  '#14b8a6',
  '#06b6d4',
  '#3b82f6',
  '#6366f1',
  '#8b5cf6',
  '#ec4899',
  '#64748b',
]

const MODES = [
  { id: 'light' as const, label: 'Light', hint: 'Default', icon: Sun },
  { id: 'dark' as const, label: 'Dark', hint: 'Low light', icon: Moon },
  { id: 'system' as const, label: 'System', hint: 'Auto', icon: Monitor },
]

function ColorField({
  label,
  value,
  onChange,
  swatches,
}: {
  label: string
  value: string
  onChange: (hex: string) => void
  swatches: string[]
}) {
  const [text, setText] = useState(value)

  useEffect(() => {
    setText(value)
  }, [value])

  const commit = () => {
    if (isHexColor(text)) onChange(text.trim())
    else setText(value)
  }

  return (
    <div className="rounded-xl border border-zinc-800/80 bg-zinc-950/40 p-3.5">
      <div className="flex items-center justify-between gap-2">
        <span className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">{label}</span>
        <span className="font-mono text-[10px] text-zinc-400">{value}</span>
      </div>
      <div className="mt-2 flex items-center gap-2">
        <input
          type="color"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          aria-label={`${label} color picker`}
          className="h-10 w-12 cursor-pointer rounded-lg border border-zinc-800 bg-transparent p-1"
        />
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              commit()
            }
            if (e.key === 'Escape') setText(value)
          }}
          spellCheck={false}
          className="h-9 min-w-0 flex-1 rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 font-mono text-xs text-zinc-200 outline-none focus:border-emerald-400/40"
          aria-label={`${label} hex value`}
        />
      </div>
      <div className="mt-2.5 flex flex-wrap gap-1.5">
        {swatches.map((color) => (
          <button
            key={color}
            type="button"
            onClick={() => onChange(color)}
            title={color}
            aria-label={`${label} ${color}`}
            style={{ backgroundColor: color }}
            className={cn(
              'h-6 w-6 rounded-md border border-black/10 transition-transform hover:scale-110',
              value.toLowerCase() === color.toLowerCase() && 'ring-2 ring-zinc-300 ring-offset-2 ring-offset-zinc-900',
            )}
          />
        ))}
      </div>
    </div>
  )
}

export default function ThemeStudio({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { theme, setTheme, palette, setPrimary, setSecondary, applyPalette, resetPalette } = useTheme()

  const shuffle = () => {
    const first = SWATCHES[Math.floor(Math.random() * SWATCHES.length)]
    let second = SWATCHES[Math.floor(Math.random() * SWATCHES.length)]
    while (second === first) second = SWATCHES[Math.floor(Math.random() * SWATCHES.length)]
    applyPalette({ primary: first, secondary: second, preset: 'custom' })
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Theme studio"
      description="Mode and accent colors for the phleet control plane. Changes apply instantly."
      width="max-w-3xl"
      footer={
        <div className="flex w-full flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-1.5">
            <Button type="button" size="sm" variant="ghost" onClick={shuffle}>
              <Shuffle size={12} /> Shuffle
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={resetPalette}>
              <RotateCcw size={12} /> Reset
            </Button>
          </div>
          <Button type="button" size="sm" variant="primary" onClick={onClose}>
            Done
          </Button>
        </div>
      }
    >
      <div className="max-h-[68vh] space-y-5 overflow-y-auto pr-1">
        <section>
          <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Mode</div>
          <div className="mt-2 grid grid-cols-3 gap-2">
            {MODES.map(({ id, label, hint, icon: Icon }) => {
              const active = theme === id
              return (
                <button
                  key={id}
                  type="button"
                  onClick={() => setTheme(id)}
                  className={cn(
                    'relative flex flex-col items-center gap-1 rounded-lg border px-2 py-3 text-xs transition-colors',
                    active
                      ? 'border-emerald-400 bg-emerald-400/10 text-zinc-100'
                      : 'border-zinc-800 bg-zinc-950/40 text-zinc-400 hover:border-zinc-700 hover:text-zinc-200',
                  )}
                >
                  <Icon size={15} />
                  <span className="font-medium">{label}</span>
                  <span className="text-[9px] text-zinc-500">{hint}</span>
                  {active ? <Check size={11} className="absolute top-1.5 right-1.5 text-emerald-400" /> : null}
                </button>
              )
            })}
          </div>
        </section>

        <section>
          <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Preview</div>
          <div
            className="mt-2 rounded-xl border border-zinc-800/80 border-l-2 bg-zinc-950/40 p-4"
            style={{ borderLeftColor: 'var(--accent)' }}
          >
            <div className="flex items-start justify-between gap-2">
              <div className="flex items-center gap-2">
                <span
                  className="grid h-7 w-7 place-items-center rounded-md"
                  style={{ backgroundColor: 'color-mix(in srgb, var(--accent) 12%, transparent)' }}
                >
                  <Boxes size={14} style={{ color: 'var(--accent)' }} />
                </span>
                <div>
                  <div className="text-[11px] font-medium text-zinc-300">Project preview</div>
                  <div className="font-mono text-[9px] text-zinc-500">3 services · one phone</div>
                </div>
              </div>
              <span
                className="rounded-md px-2 py-1 text-[9px] font-medium"
                style={{
                  backgroundColor: 'color-mix(in srgb, var(--accent-2) 12%, transparent)',
                  color: 'var(--accent-2)',
                }}
              >
                RUNNING
              </span>
            </div>
            <p className="mt-3 text-[11px] leading-relaxed text-zinc-500">
              Accent colors update buttons, links, focus rings, badges and status surfaces across the control plane.
              The secondary accent is available for highlights and previews.
            </p>
            <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
              <Button type="button" size="sm" variant="primary">
                Deploy project
              </Button>
              <span className="text-[10px] font-medium" style={{ color: 'var(--accent-2)' }}>
                3/3 replicas healthy
              </span>
            </div>
          </div>
        </section>

        <section>
          <div className="flex items-center justify-between">
            <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Accent presets</div>
            <span className="text-[10px] text-zinc-600">Applied instantly</span>
          </div>
          <div className="mt-2 grid grid-cols-2 gap-2 sm:grid-cols-4">
            {PRESETS.map((preset) => {
              const active = palette.preset === preset.preset
              return (
                <button
                  key={preset.preset}
                  type="button"
                  onClick={() => applyPalette(preset)}
                  className={cn(
                    'relative flex flex-col items-start gap-2 rounded-lg border p-2.5 text-left text-[10px] transition-colors',
                    active
                      ? 'border-emerald-400 bg-emerald-400/5 text-zinc-200'
                      : 'border-zinc-800 bg-zinc-950/40 text-zinc-500 hover:border-zinc-700 hover:text-zinc-300',
                  )}
                >
                  <span className="flex items-center gap-1">
                    <span className="h-3.5 w-3.5 rounded-full" style={{ backgroundColor: preset.primary }} />
                    <span className="h-3.5 w-3.5 rounded-full" style={{ backgroundColor: preset.secondary }} />
                  </span>
                  <span className="leading-tight">{preset.name}</span>
                  {active ? <Check size={12} className="absolute top-2 right-2 text-emerald-400" /> : null}
                </button>
              )
            })}
          </div>
        </section>

        <section>
          <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Accents</div>
          <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
            Primary drives buttons, links and focus rings. Secondary is used for highlights and previews.
          </p>
          <div className="mt-2 grid gap-3 sm:grid-cols-2">
            <ColorField
              label="Primary"
              value={palette.primary}
              onChange={setPrimary}
              swatches={SWATCHES}
            />
            <ColorField
              label="Secondary"
              value={palette.secondary}
              onChange={setSecondary}
              swatches={SWATCHES}
            />
          </div>
        </section>
      </div>
    </Dialog>
  )
}
