import { useState } from 'react'
import { useProductActions, useProductPhotoCandidates } from '../api/hooks'
import type { ProductDto } from '../api/types'
import { formatDate } from '../lib/format'
import { Button, ErrorBanner, Loading } from './ui'

const maxImages = 20

/** A product's images in order, the first (main) one larger. */
export function ProductImageStrip({ product }: { product: ProductDto }) {
  if (product.images.length === 0) return null
  return (
    <ul className="mb-3 flex gap-2 overflow-x-auto">
      {product.images.map((image, i) => (
        <li key={image.photoId} className="shrink-0">
          <img
            src={image.viewUrl}
            alt={i === 0 ? `${product.title}, main image` : `${product.title}, image ${i + 1}`}
            loading="lazy"
            className={`rounded-lg object-cover ${i === 0 ? 'size-24' : 'size-16'}`}
          />
        </li>
      ))}
    </ul>
  )
}

/**
 * Choose a product's images from photos of its plants, and their order. Nothing changes until Save.
 */
export function ProductImagePicker({ orgId, product, onDone }: { orgId: number; product: ProductDto; onDone: () => void }) {
  const candidates = useProductPhotoCandidates(orgId, product.id, true)
  const { setImages } = useProductActions(orgId)
  const [selected, setSelected] = useState<number[]>(() => product.images.map((image) => image.photoId))

  // Where to show each chosen photo: the current link from either list.
  const linkFor = (photoId: number) =>
    candidates.data?.find((c) => c.id === photoId)?.viewUrl ?? product.images.find((image) => image.photoId === photoId)?.viewUrl

  const move = (index: number, by: -1 | 1) =>
    setSelected((ids) => {
      const next = [...ids]
      ;[next[index], next[index + by]] = [next[index + by], next[index]]
      return next
    })

  return (
    <div className="mb-3 space-y-3 rounded-lg border border-stone-200 p-3 dark:border-stone-800">
      <div>
        <p className="text-sm font-medium">Images, in order ({selected.length})</p>
        <p className="text-xs text-stone-500">The first is the main image customers see.</p>
      </div>
      {selected.length === 0 ? (
        <p className="text-sm text-stone-600 dark:text-stone-400">None chosen. Tap photos below to add them.</p>
      ) : (
        <ol className="flex gap-2 overflow-x-auto pb-1">
          {selected.map((photoId, index) => (
            <li key={photoId} className="w-24 shrink-0 space-y-1">
              <img src={linkFor(photoId)} alt={`Image ${index + 1}`} className="size-24 rounded-lg object-cover" />
              <div className="flex justify-between">
                <button type="button" aria-label="Move earlier" disabled={index === 0} className="px-1 disabled:opacity-30" onClick={() => move(index, -1)}>
                  ←
                </button>
                <button type="button" aria-label="Remove" className="px-1 text-red-700 dark:text-red-400" onClick={() => setSelected((ids) => ids.filter((id) => id !== photoId))}>
                  ✕
                </button>
                <button
                  type="button"
                  aria-label="Move later"
                  disabled={index === selected.length - 1}
                  className="px-1 disabled:opacity-30"
                  onClick={() => move(index, 1)}
                >
                  →
                </button>
              </div>
            </li>
          ))}
        </ol>
      )}

      <div>
        <p className="text-sm font-medium">Photos of these plants</p>
        <p className="text-xs text-stone-500">Take photos on a plant's page; they show up here.</p>
      </div>
      {candidates.isPending && <Loading />}
      <ErrorBanner error={candidates.error} />
      {candidates.data?.length === 0 && <p className="text-sm text-stone-600 dark:text-stone-400">No photos of these plants yet.</p>}
      <ul className="grid grid-cols-3 gap-2 sm:grid-cols-5">
        {candidates.data?.map((photo) => {
          const isSelected = selected.includes(photo.id)
          return (
            <li key={photo.id}>
              <button
                type="button"
                disabled={isSelected || selected.length >= maxImages}
                onClick={() => setSelected((ids) => [...ids, photo.id])}
                className="block w-full text-left disabled:opacity-40"
                aria-label={`Add photo of ${photo.barcode} from ${formatDate(photo.takenAt)}`}
              >
                <img src={photo.viewUrl} alt="" loading="lazy" className="aspect-square w-full rounded-lg object-cover" />
                <span className="mt-0.5 block truncate font-mono text-[10px]">{photo.barcode}</span>
                <span className="block text-[10px] text-stone-500">{formatDate(photo.takenAt)}</span>
              </button>
            </li>
          )
        })}
      </ul>

      <ErrorBanner error={setImages.error} />
      <div className="flex gap-2">
        <Button disabled={setImages.isPending} onClick={() => setImages.mutate({ productId: product.id, photoIds: selected }, { onSuccess: onDone })}>
          {setImages.isPending ? 'Saving…' : 'Save images'}
        </Button>
        <Button variant="secondary" onClick={onDone}>
          Cancel
        </Button>
      </div>
    </div>
  )
}
