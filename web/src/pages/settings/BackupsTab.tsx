import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { HardDrive } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api } from '../../api'
import type { BackupDrillConfig, BackupRemoteConfig } from '../../types'
import { Badge, Button, Card } from '../../components/ui'
import { captionClass, errorClass, fieldClass, sectionLabelClass } from './shared'

export default function BackupsTab() {
  const qc = useQueryClient()
  const remote = useQuery({ queryKey: ['backup-remote'], queryFn: api.backupRemote })
  const drill = useQuery({ queryKey: ['backup-drill'], queryFn: api.backupDrill })
  const [remoteForm, setRemoteForm] = useState<BackupRemoteConfig>({
    enabled: false,
    target: '',
    rclonePath: 'rclone',
  })
  const [drillForm, setDrillForm] = useState<BackupDrillConfig>({
    enabled: false,
    intervalHours: 168,
    lastRun: 0,
    lastStatus: '',
    lastDetail: '',
  })

  useEffect(() => {
    if (remote.data) setRemoteForm(remote.data.config)
  }, [remote.data])
  useEffect(() => {
    if (drill.data) setDrillForm(drill.data.config)
  }, [drill.data])

  const saveRemote = useMutation({
    mutationFn: () => api.saveBackupRemote(remoteForm),
    onSuccess: (res) => {
      setRemoteForm(res.config)
      qc.invalidateQueries({ queryKey: ['backup-remote'] })
    },
  })
  const test = useMutation({
    mutationFn: () => api.testBackupRemote(),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['backup-remote'] }),
  })
  const sync = useMutation({
    mutationFn: () => api.syncBackupRemote(),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['backup-remote'] }),
  })
  const saveDrill = useMutation({
    mutationFn: () => api.saveBackupDrill(drillForm),
    onSuccess: (res) => {
      setDrillForm(res.config)
      qc.invalidateQueries({ queryKey: ['backup-drill'] })
    },
  })

  return (
    <Card className="p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className={sectionLabelClass}>
          <HardDrive size={12} /> Backups
        </div>
        {remoteForm.enabled ? <Badge tone="good">off-site on</Badge> : <Badge>off-site off</Badge>}
      </div>
      <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
        Backups are always encrypted locally. An rclone remote copies the encrypted dump and a small metadata
        sidecar off the control-plane host. Configure the remote in <span className="font-mono">rclone config</span>;
        phleet only stores its name and path.
      </p>

      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        <div className="sm:col-span-2">
          <label className="flex cursor-pointer items-center gap-2 text-[11px] text-zinc-300">
            <input
              type="checkbox"
              checked={remoteForm.enabled}
              onChange={(e) => setRemoteForm({ ...remoteForm, enabled: e.target.checked })}
            />
            Copy completed backups off-site
          </label>
        </div>
        <div>
          <div className={captionClass}>rclone target</div>
          <input
            className={`${fieldClass} mt-1 font-mono`}
            value={remoteForm.target}
            onChange={(e) => setRemoteForm({ ...remoteForm, target: e.target.value })}
            placeholder="b2:my-bucket/phleet"
          />
        </div>
        <div>
          <div className={captionClass}>rclone executable</div>
          <input
            className={`${fieldClass} mt-1 font-mono`}
            value={remoteForm.rclonePath}
            onChange={(e) => setRemoteForm({ ...remoteForm, rclonePath: e.target.value })}
            placeholder="rclone"
          />
        </div>
        <div className="flex flex-wrap items-center gap-2 sm:col-span-2">
          <Button size="sm" variant="primary" onClick={() => saveRemote.mutate()} disabled={saveRemote.isPending}>
            Save off-site target
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => test.mutate()}
            disabled={test.isPending || !remoteForm.target}
          >
            Test connection
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => sync.mutate()}
            disabled={sync.isPending || !remoteForm.enabled || !remoteForm.target}
            title="Upload complete backups that are not off-site yet"
          >
            Upload existing
          </Button>
          {saveRemote.isError ? <span className={errorClass}>{String(saveRemote.error)}</span> : null}
          {test.isError ? <span className={errorClass}>{String(test.error)}</span> : null}
          {test.isSuccess ? <span className="text-[10px] text-emerald-300">remote reached</span> : null}
          {sync.isSuccess ? <span className="text-[10px] text-emerald-300">{sync.data.queued} queued</span> : null}
          {sync.isError ? <span className={errorClass}>{String(sync.error)}</span> : null}
        </div>
      </div>

      <div className="mt-5 border-t border-zinc-800/60 pt-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className={captionClass}>Scheduled restore drills</div>
          {drillForm.lastStatus ? (
            <Badge tone={drillForm.lastStatus === 'failed' ? 'bad' : drillForm.lastStatus === 'passed' ? 'good' : 'warn'}>
              last {drillForm.lastStatus}
            </Badge>
          ) : null}
        </div>
        <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
          Downloads the latest backup, verifies its checksum and runs the engine-native integrity check. It never
          touches the live data directory.
        </p>
        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          <label className="flex items-center gap-2 text-[11px] text-zinc-300">
            <input
              type="checkbox"
              checked={drillForm.enabled}
              onChange={(e) => setDrillForm({ ...drillForm, enabled: e.target.checked })}
            />
            Run drills automatically
          </label>
          <div>
            <div className={captionClass}>Every (hours)</div>
            <input
              type="number"
              min={1}
              max={8760}
              className={`${fieldClass} mt-1`}
              value={drillForm.intervalHours}
              onChange={(e) =>
                setDrillForm({
                  ...drillForm,
                  intervalHours: Math.max(1, Math.min(8760, Number(e.target.value) || 168)),
                })
              }
            />
          </div>
          <div className="flex flex-wrap items-center gap-2 sm:col-span-2">
            <Button size="sm" variant="primary" onClick={() => saveDrill.mutate()} disabled={saveDrill.isPending}>
              Save drill schedule
            </Button>
            {drillForm.lastRun ? (
              <span className="text-[10px] text-zinc-600">
                last run {new Date(drillForm.lastRun * 1000).toLocaleString()}
              </span>
            ) : null}
            {drillForm.lastDetail ? (
              <span className="max-w-full truncate text-[10px] text-zinc-600" title={drillForm.lastDetail}>
                {drillForm.lastDetail}
              </span>
            ) : null}
            {saveDrill.isError ? <span className={errorClass}>{String(saveDrill.error)}</span> : null}
          </div>
        </div>
      </div>
    </Card>
  )
}
