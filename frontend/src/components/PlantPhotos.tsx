import { useRef, useState } from 'react'
import { useMembership, usePlantPhotoActions, usePlantPhotos } from '../api/hooks'
import { can } from '../api/roles'
import { Permissions } from '../api/types'
import { formatDate } from '../lib/format'
import { shrinkPhoto } from '../lib/images'
import { Button, ErrorBanner, Input, Loading } from './ui'

/** A plant's photos, newest first: its growth timeline. People who edit inventory can add and delete. */
export function PlantPhotos({ orgId, barcode }: { orgId: number; barcode: string }) {
  const membership = useMembership(orgId)
  const photos = usePlantPhotos(orgId, barcode)
  const actions = usePlantPhotoActions(orgId, barcode)
  const fileInput = useRef<HTMLInputElement>(null)
  const [caption, setCaption] = useState('')
  const [preparing, setPreparing] = useState(false)
  const [prepareError, setPrepareError] = useState<unknown>(null)
  const canEdit = can(membership, Permissions.InventoryEdit)

  const upload = async (file: File) => {
    setPrepareError(null)
    setPreparing(true)
    try {
      const photo = await shrinkPhoto(file)
      actions.add.mutate(
        { photo, caption: caption.trim() || undefined, takenAt: new Date(file.lastModified).toISOString() },
        { onSuccess: () => setCaption('') },
      )
    } catch (error) {
      setPrepareError(error)
    } finally {
      setPreparing(false)
    }
  }

  const busy = preparing || actions.add.isPending

  return (
    <div className="space-y-3">
      {canEdit && (
        <div className="flex flex-wrap gap-2">
          <div className="min-w-40 flex-1">
            <Input aria-label="Caption" placeholder="Caption (optional), e.g. new leaf" maxLength={500} value={caption} onChange={(e) => setCaption(e.target.value)} />
          </div>
          {/* capture opens the back camera on phones; computers get a file picker. */}
          <input
            ref={fileInput}
            type="file"
            accept="image/*"
            capture="environment"
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0]
              e.target.value = '' // so the same file can be chosen again
              if (file) upload(file)
            }}
          />
          <Button disabled={busy} onClick={() => fileInput.current?.click()}>
            {preparing ? 'Preparing…' : actions.add.isPending ? 'Uploading…' : 'Add photo'}
          </Button>
        </div>
      )}
      <ErrorBanner error={prepareError ?? actions.add.error ?? actions.remove.error} />

      {photos.isPending && <Loading />}
      <ErrorBanner error={photos.error} />
      {photos.data?.length === 0 && <p className="text-sm text-stone-600 dark:text-stone-400">No photos yet.</p>}
      <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        {photos.data?.map((photo) => (
          <li key={photo.id} className="overflow-hidden rounded-lg border border-stone-200 dark:border-stone-800">
            <a href={photo.viewUrl} target="_blank" rel="noreferrer">
              <img src={photo.viewUrl} alt={photo.caption ?? `${barcode}, ${formatDate(photo.takenAt)}`} loading="lazy" className="aspect-square w-full object-cover" />
            </a>
            <div className="space-y-0.5 p-2 text-xs">
              <p className="font-medium">{formatDate(photo.takenAt)}</p>
              {photo.caption && <p>{photo.caption}</p>}
              {photo.uploadedBy && <p className="truncate text-stone-500">{photo.uploadedBy}</p>}
              {canEdit && (
                <button
                  type="button"
                  className="text-red-700 underline dark:text-red-400"
                  disabled={actions.remove.isPending}
                  onClick={() => window.confirm('Delete this photo?') && actions.remove.mutate(photo.id)}
                >
                  Delete
                </button>
              )}
            </div>
          </li>
        ))}
      </ul>
    </div>
  )
}
