import { AlertTriangle, X } from 'lucide-react'
import {
  useEffect,
  useId,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from 'react'
import { Button, Spinner, cn } from './ui'
import { useTheme } from '../theme'

const focusable =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

export function Dialog({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  danger = false,
  busy = false,
  width = 'max-w-sm',
}: {
  open: boolean
  onClose: () => void
  title: string
  description?: ReactNode
  children?: ReactNode
  footer?: ReactNode
  danger?: boolean
  busy?: boolean
  width?: string
}) {
  const dialogRef = useRef<HTMLDivElement>(null)
  const closeRef = useRef(onClose)
  const busyRef = useRef(busy)
  closeRef.current = onClose
  busyRef.current = busy
  const titleId = useId()
  const { resolved } = useTheme()

  useEffect(() => {
    if (!open) return
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busyRef.current) {
        event.preventDefault()
        closeRef.current()
        return
      }
      if (event.key !== 'Tab') return
      const nodes = dialogRef.current?.querySelectorAll<HTMLElement>(focusable)
      if (!nodes || nodes.length === 0) return
      const first = nodes[0]
      const last = nodes[nodes.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', onKeyDown)
    const timer = window.setTimeout(() => {
      const target =
        dialogRef.current?.querySelector<HTMLElement>('[data-autofocus]') ??
        dialogRef.current?.querySelector<HTMLElement>(focusable)
      target?.focus()
      if (target instanceof HTMLInputElement) target.select()
    }, 0)

    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.body.style.overflow = previousOverflow
      window.clearTimeout(timer)
      if (previous && document.contains(previous)) previous.focus()
    }
  }, [open])

  if (!open) return null

  return (
    <div
      className={cn(
        'fixed inset-0 z-50 grid place-items-center p-4 backdrop-blur-sm',
        resolved === 'light' ? 'bg-black/30' : 'bg-black/60',
      )}
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !busy) onClose()
      }}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className={cn(
          'w-full rounded-xl border border-zinc-800 bg-zinc-900 p-5 shadow-2xl shadow-black/40',
          width,
        )}
      >
        <div className="flex items-start gap-3">
          {danger ? (
            <div className="mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-red-500/10 text-red-300">
              <AlertTriangle size={15} />
            </div>
          ) : null}
          <div className="min-w-0 flex-1">
            <h2 id={titleId} className="text-sm font-semibold text-zinc-100">
              {title}
            </h2>
            {description ? (
              <div className="mt-1 text-[11px] leading-relaxed text-zinc-500">{description}</div>
            ) : null}
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            aria-label="Close dialog"
            className="-mt-1 rounded p-1 text-zinc-500 transition-colors hover:bg-zinc-800/60 hover:text-zinc-200 disabled:opacity-40"
          >
            <X size={14} />
          </button>
        </div>
        {children ? <div className="mt-4">{children}</div> : null}
        {footer ? <div className="mt-5 flex flex-wrap justify-end gap-2">{footer}</div> : null}
      </div>
    </div>
  )
}

export function PromptDialog({
  open,
  onClose,
  title,
  description,
  label,
  initialValue = '',
  placeholder,
  confirmLabel = 'Save',
  validate,
  onSubmit,
  busy = false,
}: {
  open: boolean
  onClose: () => void
  title: string
  description?: ReactNode
  label: string
  initialValue?: string
  placeholder?: string
  confirmLabel?: string
  validate?: (value: string) => string | null | undefined
  onSubmit: (value: string) => void
  busy?: boolean
}) {
  const [value, setValue] = useState(initialValue)
  const [error, setError] = useState('')
  const formId = useId()
  const inputId = useId()

  useEffect(() => {
    if (!open) return
    setValue(initialValue)
    setError('')
  }, [open, initialValue])

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const next = value.trim()
    if (!next) {
      setError('Enter a name.')
      return
    }
    const problem = validate?.(next)
    if (problem) {
      setError(problem)
      return
    }
    onSubmit(next)
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={title}
      description={description}
      busy={busy}
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={busy || !value.trim()}>
            {busy ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : null}
            {confirmLabel}
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={submit}>
        <label
          htmlFor={inputId}
          className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase"
        >
          {label}
        </label>
        <input
          id={inputId}
          data-autofocus
          value={value}
          onChange={(event) => {
            setValue(event.target.value)
            if (error) setError('')
          }}
          placeholder={placeholder}
          autoComplete="off"
          spellCheck={false}
          aria-invalid={!!error}
          aria-describedby={error ? `${inputId}-error` : undefined}
          className={cn(
            'mt-1 h-9 w-full rounded-lg border bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none placeholder:text-zinc-600',
            error ? 'border-red-500/50 focus:border-red-400' : 'border-zinc-800 focus:border-emerald-400/40',
          )}
        />
        {error ? (
          <p id={`${inputId}-error`} className="mt-1 text-[10px] text-red-300">
            {error}
          </p>
        ) : null}
      </form>
    </Dialog>
  )
}

export function ConfirmDialog({
  open,
  onClose,
  title,
  description,
  confirmLabel = 'Confirm',
  danger = false,
  busy = false,
  onConfirm,
}: {
  open: boolean
  onClose: () => void
  title: string
  description?: ReactNode
  confirmLabel?: string
  danger?: boolean
  busy?: boolean
  onConfirm: () => void
}) {
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={title}
      description={description}
      danger={danger}
      busy={busy}
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            type="button"
            variant={danger ? 'danger' : 'primary'}
            onClick={onConfirm}
            disabled={busy}
          >
            {busy ? <Spinner className="h-3 w-3" /> : null}
            {confirmLabel}
          </Button>
        </>
      }
    />
  )
}
