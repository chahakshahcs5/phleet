import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowUp,
  Download,
  File,
  FileText,
  Folder,
  FolderPlus,
  Home,
  Pencil,
  RefreshCw,
  Save,
  Trash2,
  Upload,
  X,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { api, textFromBase64 } from '../api'
import type { FSEntry } from '../types'
import { ago, bytes } from '../lib/format'
import { zipSelection } from '../lib/zip'
import { ConfirmDialog, PromptDialog } from './Dialog'
import { Badge, Button, EmptyState, Spinner, cn } from './ui'

export default function FilesTab({ id }: { id: string }) {
  const qc = useQueryClient()
  const [path, setPath] = useState('')
  const [openFile, setOpenFile] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  const [dirty, setDirty] = useState(false)
  const [uploading, setUploading] = useState('')
  const [uploadErr, setUploadErr] = useState('')
  const loadedPath = useRef<string | null>(null)
  const filesRef = useRef<HTMLInputElement>(null)
  const folderRef = useRef<HTMLInputElement>(null)
  const [newFolderOpen, setNewFolderOpen] = useState(false)
  const [renameTarget, setRenameTarget] = useState<FSEntry | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<FSEntry | null>(null)
  const [discardOpen, setDiscardOpen] = useState(false)

  const list = useQuery({
    queryKey: ['fs', id, path],
    queryFn: () => api.fsList(id, path),
    refetchInterval: 20000,
  })

  const file = useQuery({
    queryKey: ['fs-file', id, openFile],
    queryFn: () => api.fsRead(id, openFile!),
    enabled: !!openFile,
  })

  const current = list.data?.path ?? path
  const parent = list.data?.parent ?? ''
  const roots = list.data?.roots ?? []
  const ready = !!list.data
  const childPath = (name: string) => `${current.replace(/\/+$/, '')}/${name}`
  const validateName = (name: string) => {
    if (name === '.' || name === '..') return 'Choose a different name.'
    if (/[\\/]/.test(name)) return 'Use a single name, without slashes.'
    return ''
  }

  useEffect(() => {
    if (!file.data) return
    if (loadedPath.current === file.data.path) return
    loadedPath.current = file.data.path
    if (!file.data.binary && !file.data.truncated) {
      setDraft(textFromBase64(file.data.data))
    } else {
      setDraft('')
    }
    setDirty(false)
  }, [file.data])

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['fs', id, path] })
    if (openFile) qc.invalidateQueries({ queryKey: ['fs-file', id, openFile] })
  }

  const save = useMutation({
    mutationFn: () => api.fsWrite(id, openFile!, new TextEncoder().encode(draft)),
    onSuccess: () => {
      setDirty(false)
      qc.invalidateQueries({ queryKey: ['fs-file', id, openFile] })
      qc.invalidateQueries({ queryKey: ['fs', id, path] })
    },
  })

  const op = useMutation({
    mutationFn: (body: { op: string; path: string; to?: string; recursive?: boolean }) => api.fsOp(id, body),
    onSuccess: (_res, body) => {
      qc.invalidateQueries({ queryKey: ['fs', id, path] })
      if (body.op === 'delete' && openFile === body.path) {
        setOpenFile(null)
        loadedPath.current = null
      }
    },
  })

  const uploadFiles = async (list: FileList) => {
    setUploadErr('')
    setUploading(`uploading ${list.length} file${list.length === 1 ? '' : 's'}…`)
    try {
      if (list.length === 1) {
        await api.upload(id, current, list[0])
      } else {
        const z = await zipSelection(list)
        await api.uploadFolder(id, current, z.blob)
      }
      qc.invalidateQueries({ queryKey: ['fs', id, path] })
    } catch (e) {
      setUploadErr(String(e))
    } finally {
      setUploading('')
    }
  }

  const uploadFolder = async (list: FileList) => {
    setUploadErr('')
    setUploading(`zipping and uploading ${list.length} files…`)
    try {
      const z = await zipSelection(list)
      await api.uploadFolder(id, current, z.blob)
      qc.invalidateQueries({ queryKey: ['fs', id, path] })
    } catch (e) {
      setUploadErr(String(e))
    } finally {
      setUploading('')
    }
  }

  const openEntry = (e: FSEntry) => {
    if (e.dir) {
      setPath(e.path)
      return
    }
    loadedPath.current = null
    setOpenFile(e.path)
  }

  const crumbs = (() => {
    const parts = current.split('/').filter(Boolean)
    const out: { name: string; path: string }[] = []
    let acc = ''
    for (const p of parts) {
      acc += '/' + p
      out.push({ name: p, path: acc })
    }
    return out
  })()

  const error = list.error || save.error || op.error || (uploadErr ? new Error(uploadErr) : null)

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-1.5">
        <Button size="icon" variant="ghost" title="Home" onClick={() => setPath(roots[0] ?? '')}>
          <Home size={13} />
        </Button>
        <Button
          size="icon"
          variant="ghost"
          title="Up"
          disabled={!current || current === '/' || current === roots[0]}
          onClick={() => setPath(parent)}
        >
          <ArrowUp size={13} />
        </Button>
        <Button size="icon" variant="ghost" title="Refresh" onClick={refresh}>
          <RefreshCw size={13} />
        </Button>
        <div className="mx-1 h-5 w-px bg-zinc-800" />
        <Button
          size="sm"
          variant="ghost"
          disabled={!ready}
          onClick={() => setNewFolderOpen(true)}
        >
          <FolderPlus size={13} /> Folder
        </Button>
        <Button size="sm" variant="ghost" disabled={!ready} onClick={() => filesRef.current?.click()}>
          <Upload size={13} /> Files
        </Button>
        <Button size="sm" variant="ghost" disabled={!ready} onClick={() => folderRef.current?.click()}>
          <Upload size={13} /> Folder
        </Button>
        <input
          ref={filesRef}
          type="file"
          multiple
          className="hidden"
          onChange={(e) => {
            const f = e.target.files
            if (f && f.length) void uploadFiles(f)
            e.target.value = ''
          }}
        />
        <input
          ref={(el) => {
            folderRef.current = el
            if (el) {
              el.setAttribute('webkitdirectory', '')
              el.setAttribute('directory', '')
            }
          }}
          type="file"
          multiple
          className="hidden"
          onChange={(e) => {
            const f = e.target.files
            if (f && f.length) void uploadFolder(f)
            e.target.value = ''
          }}
        />
        {uploading ? (
          <span className="flex items-center gap-1.5 text-[10px] text-zinc-500">
            <Spinner className="h-3 w-3" /> {uploading}
          </span>
        ) : null}
      </div>

      <div className="flex items-center gap-1 overflow-x-auto rounded-lg border border-zinc-800/80 bg-zinc-950/60 px-2 py-1.5 font-mono text-[10px] whitespace-nowrap text-zinc-500">
        <Folder size={11} className="mr-1 shrink-0 text-zinc-600" />
        {crumbs.length === 0 ? (
          <span className="text-zinc-300">/</span>
        ) : (
          crumbs.map((c, i) => (
            <span key={c.path} className="flex items-center">
              {i > 0 ? <span className="mx-0.5 text-zinc-700">/</span> : null}
              <button
                className="rounded px-1 py-0.5 hover:bg-zinc-800 hover:text-zinc-200"
                onClick={() => setPath(c.path)}
              >
                {c.name}
              </button>
            </span>
          ))
        )}
      </div>

      {error ? (
        <div className="rounded-lg border border-red-900/40 bg-red-500/5 px-3 py-2 text-[11px] text-red-300">
          {String((error as Error).message || error)}
        </div>
      ) : null}

      <div className={cn('grid gap-3', openFile ? 'lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]' : '')}>
        <div className="overflow-hidden rounded-xl border border-zinc-800/80">
          <div className="flex items-center justify-between border-b border-zinc-800/70 bg-zinc-950/50 px-3 py-1.5 text-[10px] tracking-wider text-zinc-500 uppercase">
            <span>contents</span>
            <span>{list.data?.entries.length ?? 0} items</span>
          </div>
          {list.isLoading ? (
            <div className="flex items-center gap-2 p-6 text-xs text-zinc-500">
              <Spinner /> listing…
            </div>
          ) : list.data && list.data.entries.length === 0 ? (
            <div className="p-8 text-center text-[11px] text-zinc-600">empty directory</div>
          ) : (
            <div className="divide-y divide-zinc-800/50">
              {list.data?.entries.map((e) => (
                <div
                  key={e.path}
                  onClick={() => openEntry(e)}
                  className={cn(
                    'group flex cursor-pointer items-center gap-2 px-3 py-2 transition-colors hover:bg-zinc-800/40',
                    openFile === e.path && 'bg-emerald-400/5',
                  )}
                >
                  <span className="shrink-0 text-zinc-500">
                    {e.dir ? <Folder size={14} /> : <File size={14} />}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-xs text-zinc-300">
                    {e.name}
                    {e.symlink ? <span className="ml-1 text-[9px] text-zinc-600">link</span> : null}
                  </span>
                  <span className="hidden w-16 shrink-0 text-right font-mono text-[10px] text-zinc-500 sm:block">
                    {e.dir ? '—' : bytes(e.size)}
                  </span>
                  <span className="hidden w-24 shrink-0 text-right text-[10px] text-zinc-600 md:block">
                    {ago(e.modTime)}
                  </span>
                  <span className="flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
                    {!e.dir ? (
                      <a
                        href={api.downloadUrl(id, e.path)}
                        onClick={(ev) => ev.stopPropagation()}
                        className="rounded p-1 text-zinc-500 hover:bg-zinc-700/60 hover:text-zinc-200"
                        title="Download"
                      >
                        <Download size={12} />
                      </a>
                    ) : null}
                    <button
                      onClick={(ev) => {
                        ev.stopPropagation()
                        setRenameTarget(e)
                      }}
                      className="rounded p-1 text-zinc-500 hover:bg-zinc-700/60 hover:text-zinc-200"
                      title="Rename"
                    >
                      <Pencil size={12} />
                    </button>
                    <button
                      onClick={(ev) => {
                        ev.stopPropagation()
                        setDeleteTarget(e)
                      }}
                      className="rounded p-1 text-zinc-500 hover:bg-red-500/10 hover:text-red-300"
                      title="Delete"
                    >
                      <Trash2 size={12} />
                    </button>
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>

        {openFile ? (
          <div className="flex min-h-[320px] flex-col overflow-hidden rounded-xl border border-zinc-800/80">
            <div className="flex items-center gap-2 border-b border-zinc-800/70 bg-zinc-950/50 px-3 py-1.5">
              <FileText size={12} className="text-zinc-500" />
              <span className="min-w-0 flex-1 truncate font-mono text-[10px] text-zinc-400">
                {openFile.split('/').pop()}
              </span>
              {file.data ? <Badge>{bytes(file.data.size)}</Badge> : null}
              {dirty ? <Badge tone="warn">modified</Badge> : null}
              <Button
                size="sm"
                variant="primary"
                disabled={!dirty || save.isPending || !file.data || file.data.binary || file.data.truncated}
                onClick={() => save.mutate()}
              >
                {save.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Save size={12} />}
                Save
              </Button>
              <a
                href={api.downloadUrl(id, openFile)}
                className="rounded p-1 text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200"
                title="Download"
              >
                <Download size={12} />
              </a>
              <button
                onClick={() => {
                  if (dirty) {
                    setDiscardOpen(true)
                    return
                  }
                  setOpenFile(null)
                  loadedPath.current = null
                }}
                className="rounded p-1 text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200"
                title="Close"
              >
                <X size={13} />
              </button>
            </div>

            {file.isLoading ? (
              <div className="flex items-center gap-2 p-6 text-xs text-zinc-500">
                <Spinner /> opening…
              </div>
            ) : file.data?.binary ? (
              <EmptyState icon={<File size={20} />} title="Binary file">
                {bytes(file.data.size)} · preview is not available. Use download to fetch it.
              </EmptyState>
            ) : file.data?.truncated ? (
              <EmptyState icon={<File size={20} />} title="File too large to edit">
                {bytes(file.data.size)} exceeds the editor limit. Download it instead.
              </EmptyState>
            ) : (
              <textarea
                value={draft}
                onChange={(e) => {
                  setDraft(e.target.value)
                  setDirty(true)
                }}
                spellCheck={false}
                className="min-h-0 flex-1 resize-none bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed text-zinc-300 outline-none"
              />
            )}
            {file.data && !file.data.binary ? (
              <div className="border-t border-zinc-800/70 px-3 py-1.5 font-mono text-[10px] text-zinc-600">
                {file.data.mode} · {bytes(file.data.size)} · {ago(file.data.modTime)}
              </div>
            ) : null}
          </div>
        ) : null}
      </div>

      <PromptDialog
        open={newFolderOpen}
        onClose={() => setNewFolderOpen(false)}
        title="New folder"
        description={
          <>
            Create a folder in <span className="font-mono text-zinc-400">{current || '/'}</span>.
          </>
        }
        label="Folder name"
        placeholder="logs"
        confirmLabel="Create folder"
        validate={validateName}
        onSubmit={(name) => {
          setNewFolderOpen(false)
          op.mutate({ op: 'mkdir', path: childPath(name) })
        }}
      />

      <PromptDialog
        open={!!renameTarget}
        onClose={() => setRenameTarget(null)}
        title="Rename"
        description={
          renameTarget ? (
            <>
              Rename <span className="font-mono text-zinc-400">{renameTarget.name}</span>.
            </>
          ) : undefined
        }
        label="New name"
        initialValue={renameTarget?.name ?? ''}
        confirmLabel="Rename"
        validate={validateName}
        onSubmit={(name) => {
          if (renameTarget && name !== renameTarget.name) {
            op.mutate({ op: 'rename', path: renameTarget.path, to: childPath(name) })
          }
          setRenameTarget(null)
        }}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        title={deleteTarget?.dir ? 'Delete folder?' : 'Delete file?'}
        description={
          deleteTarget ? (
            <>
              Delete <span className="font-mono text-zinc-300">{deleteTarget.name}</span>?
              {deleteTarget.dir ? ' The folder and everything inside it will be removed.' : ''} This
              cannot be undone.
            </>
          ) : undefined
        }
        confirmLabel="Delete"
        danger
        onConfirm={() => {
          if (deleteTarget) op.mutate({ op: 'delete', path: deleteTarget.path, recursive: true })
          setDeleteTarget(null)
        }}
      />

      <ConfirmDialog
        open={discardOpen}
        onClose={() => setDiscardOpen(false)}
        title="Discard unsaved changes?"
        description="Your edits to this file will be lost."
        confirmLabel="Discard changes"
        danger
        onConfirm={() => {
          setDiscardOpen(false)
          setDirty(false)
          setOpenFile(null)
          loadedPath.current = null
        }}
      />
    </div>
  )
}

