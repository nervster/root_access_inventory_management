import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Link, useParams } from 'react-router'
import { useMembership, usePlantPhotoSession } from '../../api/hooks'
import { can } from '../../api/roles'
import { Permissions, type PlantDto } from '../../api/types'
import { Button, Card, Input, LinkButton, PageHeader } from '../../components/ui'
import { useCamera } from '../../lib/camera'
import { photoFromVideo, shrinkPhoto } from '../../lib/images'
import { loadScanned } from '../../scanList'

/** A photo taken in this session, from the moment it's taken (uploading) until it's saved. */
interface Shot {
  key: number
  barcode: string
  photo: Blob // kept, so a failed upload can be retried without retaking it
  preview: string // object URL of photo
  takenAt: string
  status: 'uploading' | 'saved' | 'failed'
  error?: string
  photoId?: number
  caption: string
  savedCaption: string
  captionStatus?: 'saving' | 'failed'
}

let nextKey = 1

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : 'Something went wrong.'
}

/**
 * Photographs the Scan page's plants one after another: the camera stays open, each plant can get
 * several photos (each with an optional caption), and Next moves on. Uploads run in the background,
 * so nobody waits between plants.
 */
export function PhotoSessionPage() {
  const orgId = Number(useParams().orgId)
  const membership = useMembership(orgId)
  const photos = usePlantPhotoSession(orgId)
  const [plants] = useState<PlantDto[]>(() => loadScanned(orgId).flatMap((s) => (s.plant ? [s.plant] : [])))
  const [index, setIndex] = useState(0)
  const [finished, setFinished] = useState(false)
  const [shots, setShots] = useState<Shot[]>([])
  // The latest shots, for uploads finishing after the render that started them.
  const latest = useRef(shots)
  useEffect(() => {
    latest.current = shots
  })

  const update = (key: number, change: Partial<Shot>) => setShots((list) => list.map((s) => (s.key === key ? { ...s, ...change } : s)))

  // Closing the tab mid-upload would lose those photos: ask first.
  const uploading = shots.some((s) => s.status === 'uploading')
  useEffect(() => {
    if (!uploading) return
    const warn = (event: BeforeUnloadEvent) => event.preventDefault()
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [uploading])
  useEffect(() => () => latest.current.forEach((s) => URL.revokeObjectURL(s.preview)), [])

  // force: send it even if unchanged (retrying a caption that failed to save).
  const saveCaption = async (shot: Shot, force = false) => {
    const caption = shot.caption.trim()
    // Not saved yet: the caption goes along once the upload finishes (see send).
    if (shot.status !== 'saved' || shot.photoId === undefined || (caption === shot.savedCaption && !force)) return
    update(shot.key, { captionStatus: 'saving' })
    try {
      await photos.setCaption(shot.barcode, shot.photoId, caption)
      update(shot.key, { savedCaption: caption, captionStatus: undefined })
    } catch {
      update(shot.key, { captionStatus: 'failed' })
    }
  }

  const send = async (shot: Shot) => {
    update(shot.key, { status: 'uploading', error: undefined })
    try {
      const saved = await photos.upload(shot.barcode, shot.photo, shot.takenAt)
      update(shot.key, { status: 'saved', photoId: saved.id })
      const now = latest.current.find((s) => s.key === shot.key)
      if (now?.caption.trim()) void saveCaption({ ...now, status: 'saved', photoId: saved.id })
    } catch (error) {
      update(shot.key, { status: 'failed', error: errorMessage(error) })
    }
  }

  const remove = async (shot: Shot) => {
    if (!window.confirm('Delete this photo?')) return
    if (shot.status === 'saved' && shot.photoId !== undefined) {
      try {
        await photos.remove(shot.barcode, shot.photoId)
      } catch (error) {
        update(shot.key, { error: errorMessage(error) })
        return
      }
    }
    URL.revokeObjectURL(shot.preview)
    setShots((list) => list.filter((s) => s.key !== shot.key))
  }

  if (!can(membership, Permissions.InventoryEdit))
    return <p className="text-sm text-stone-600 dark:text-stone-400">Adding photos needs permission to edit inventory.</p>
  if (plants.length === 0)
    return (
      <Card>
        <p className="text-sm">No plants to photograph. Scan some first.</p>
        <LinkButton to={`/orgs/${orgId}/scan`} className="mt-3">
          Go to Scan
        </LinkButton>
      </Card>
    )

  if (finished) return <Summary orgId={orgId} plants={plants} shots={shots} onRetry={send} onBack={() => setFinished(false)} />

  const plant = plants[index]
  const last = index === plants.length - 1
  const plantShots = shots.filter((s) => s.barcode === plant.barcode)

  const take = (photo: Blob) => {
    const shot: Shot = {
      key: nextKey++,
      barcode: plant.barcode,
      photo,
      preview: URL.createObjectURL(photo),
      takenAt: new Date().toISOString(),
      status: 'uploading',
      caption: '',
      savedCaption: '',
    }
    setShots((list) => [...list, shot])
    void send(shot)
  }

  return (
    <div className="space-y-3">
      <div>
        <p className="text-sm text-stone-600 dark:text-stone-400">
          Plant {index + 1} of {plants.length}
        </p>
        <h1 className="text-lg font-semibold leading-tight">
          {plant.plantName}
          {plant.variegation && ` ${plant.variegation}`}
        </h1>
        <p className="text-sm">
          <span className="font-mono font-medium">{plant.barcode}</span> · {plant.potSize}
        </p>
      </div>

      <SessionCamera
        onPhoto={take}
        left={
          <Button variant="secondary" disabled={index === 0} onClick={() => setIndex(index - 1)}>
            Back
          </Button>
        }
        right={
          <Button onClick={() => (last ? setFinished(true) : setIndex(index + 1))}>
            {last ? 'Finish' : plantShots.length === 0 ? 'Skip' : 'Next'}
          </Button>
        }
      />

      {plantShots.length > 0 && (
        <ul className="space-y-2">
          {plantShots.map((shot) => (
            <li key={shot.key} className="flex items-start gap-3 rounded-lg border border-stone-200 p-2 dark:border-stone-800">
              <img src={shot.preview} alt="" className="size-16 shrink-0 rounded-md object-cover" />
              <div className="min-w-0 flex-1 space-y-1">
                <Input
                  aria-label="Caption"
                  placeholder="Caption (optional)"
                  value={shot.caption}
                  onChange={(e) => update(shot.key, { caption: e.target.value })}
                  onBlur={() => void saveCaption(shot)}
                  onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
                />
                <ShotStatus shot={shot} onRetry={() => void send(shot)} onRetryCaption={() => void saveCaption(shot, true)} />
              </div>
              <Button variant="ghost" aria-label="Delete photo" disabled={shot.status === 'uploading'} onClick={() => void remove(shot)}>
                ✕
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function ShotStatus({ shot, onRetry, onRetryCaption }: { shot: Shot; onRetry: () => void; onRetryCaption: () => void }) {
  const retry = (onClick: () => void) => (
    <button type="button" onClick={onClick} className="font-medium underline">
      Retry
    </button>
  )
  if (shot.status === 'uploading') return <p className="text-xs text-stone-600 dark:text-stone-400">Uploading…</p>
  if (shot.status === 'failed')
    return (
      <p className="text-xs text-red-700 dark:text-red-400">
        ⚠ Didn't upload{shot.error ? `: ${shot.error}` : ''} {retry(onRetry)}
      </p>
    )
  if (shot.captionStatus === 'saving') return <p className="text-xs text-stone-600 dark:text-stone-400">Saving caption…</p>
  if (shot.captionStatus === 'failed')
    return <p className="text-xs text-red-700 dark:text-red-400">⚠ Caption not saved {retry(onRetryCaption)}</p>
  return (
    <p className="text-xs text-emerald-700 dark:text-emerald-400">
      ✓ Saved{shot.error ? ` (${shot.error})` : ''}
    </p>
  )
}

/**
 * The camera, open for the whole session, with the shutter between `left` and `right` (Back and Next)
 * so all three are in thumb reach. Without a camera it falls back to the phone's photo picker.
 */
function SessionCamera({ onPhoto, left, right }: { onPhoto: (photo: Blob) => void; left: ReactNode; right: ReactNode }) {
  const fileInput = useRef<HTMLInputElement>(null)
  const [cameraError, setCameraError] = useState<string | null>(null)
  const [problem, setProblem] = useState<string | null>(null)
  const [flash, setFlash] = useState(false)
  const video = useCamera(setCameraError, { highResolution: true })

  const shoot = async () => {
    const v = video.current
    if (!v || v.readyState < v.HAVE_ENOUGH_DATA) return
    setFlash(true)
    setTimeout(() => setFlash(false), 150)
    navigator.vibrate?.(40)
    try {
      setProblem(null)
      onPhoto(await photoFromVideo(v))
    } catch (error) {
      setProblem(errorMessage(error))
    }
  }

  const pick = async (file: File) => {
    try {
      setProblem(null)
      onPhoto(await shrinkPhoto(file))
    } catch (error) {
      setProblem(errorMessage(error))
    }
  }

  return (
    <div className="space-y-3">
      {cameraError ? (
        <p className="text-sm text-red-700 dark:text-red-400">{cameraError} You can still add photos with the phone's camera.</p>
      ) : (
        <div className="relative overflow-hidden rounded-xl bg-black">
          {/* contain, not cover: the photo is the whole picture, so show all of it. */}
          <video ref={video} className="max-h-[55dvh] w-full object-contain" playsInline muted />
          {flash && <div className="absolute inset-0 bg-white/70" />}
        </div>
      )}
      <div className="flex items-center justify-between gap-3">
        <div className="flex-1">{left}</div>
        {cameraError ? (
          <>
            <Button onClick={() => fileInput.current?.click()}>Add photo</Button>
            {/* capture opens the back camera on phones; computers get a file picker. */}
            <input
              ref={fileInput}
              type="file"
              accept="image/*"
              capture="environment"
              className="hidden"
              onChange={(e) => {
                const file = e.target.files?.[0]
                e.target.value = ''
                if (file) void pick(file)
              }}
            />
          </>
        ) : (
          <button
            type="button"
            aria-label="Take photo"
            onClick={() => void shoot()}
            className="size-16 shrink-0 rounded-full border-4 border-emerald-700 bg-white shadow active:scale-95 dark:border-emerald-400"
          />
        )}
        <div className="flex flex-1 justify-end">{right}</div>
      </div>
      {problem && <p className="text-sm text-red-700 dark:text-red-400">{problem}</p>}
    </div>
  )
}

function Summary({ orgId, plants, shots, onRetry, onBack }: { orgId: number; plants: PlantDto[]; shots: Shot[]; onRetry: (shot: Shot) => void; onBack: () => void }) {
  const saved = shots.filter((s) => s.status === 'saved').length
  const uploading = shots.filter((s) => s.status === 'uploading').length
  const failed = shots.filter((s) => s.status === 'failed')
  const skipped = plants.filter((p) => !shots.some((s) => s.barcode === p.barcode)).length
  return (
    <div className="space-y-4">
      <PageHeader title="Photos done" subtitle={`${plants.length} plant${plants.length === 1 ? '' : 's'}`} />
      <Card>
        <ul className="space-y-1 text-sm">
          <li>
            {saved} photo{saved === 1 ? '' : 's'} added
          </li>
          {uploading > 0 && <li>{uploading} still uploading…</li>}
          <li>
            {skipped} plant{skipped === 1 ? '' : 's'} skipped
          </li>
          <li className={failed.length > 0 ? 'font-medium text-red-700 dark:text-red-400' : ''}>{failed.length} failed</li>
        </ul>
        {failed.length > 0 && (
          <ul className="mt-3 space-y-2">
            {failed.map((shot) => (
              <li key={shot.key} className="flex items-center gap-3">
                <img src={shot.preview} alt="" className="size-12 rounded-md object-cover" />
                <Link to={`/orgs/${orgId}/plants/${shot.barcode}`} className="flex-1 font-mono text-sm">
                  {shot.barcode}
                </Link>
                <Button variant="secondary" onClick={() => onRetry(shot)}>
                  Retry
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Card>
      <div className="flex gap-2">
        <Button variant="secondary" onClick={onBack}>
          Back to the last plant
        </Button>
        <LinkButton to={`/orgs/${orgId}/scan`}>Back to Scan</LinkButton>
      </div>
    </div>
  )
}
