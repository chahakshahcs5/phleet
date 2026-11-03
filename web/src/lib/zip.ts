import JSZip from 'jszip'

const MAX_ZIP = 64 * 1024 * 1024

export interface ZipResult {
  blob: Blob
  files: number
  bytes: number
}

/** Zip a browser FileList. Folder picks keep their relative paths; when
 * stripRoot is set, the selected top-level folder is removed so a project's
 * files land at the release root. */
export async function zipSelection(
  files: FileList | File[],
  opts: { stripRoot?: boolean } = {},
): Promise<ZipResult> {
  const list = Array.from(files)
  if (list.length === 0) throw new Error('no files selected')

  const paths = list.map((f) => {
    const rel = (f as File & { webkitRelativePath?: string }).webkitRelativePath
    return rel && rel.length > 0 ? rel : f.name
  })

  let strip = ''
  if (opts.stripRoot) {
    const roots = new Set(paths.map((p) => p.split('/')[0]))
    if (roots.size === 1 && paths.every((p) => p.includes('/'))) {
      strip = [...roots][0] + '/'
    }
  }

  const zip = new JSZip()
  let bytes = 0
  let added = 0
  for (let i = 0; i < list.length; i++) {
    let rel = paths[i]
    if (strip && rel.startsWith(strip)) rel = rel.slice(strip.length)
    if (!rel) continue
    zip.file(rel, list[i])
    bytes += list[i].size
    added++
  }
  if (added === 0) throw new Error('the selected folder is empty')

  const blob = await zip.generateAsync({
    type: 'blob',
    compression: 'DEFLATE',
    compressionOptions: { level: 6 },
  })
  if (blob.size > MAX_ZIP) {
    throw new Error(`archive is ${Math.round(blob.size / 1048576)} MB; the limit is 64 MB`)
  }
  return { blob, files: added, bytes }
}
