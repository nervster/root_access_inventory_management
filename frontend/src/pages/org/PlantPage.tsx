import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { ApiError } from '../../api/client'
import { useMembership, usePlant, usePlantActions } from '../../api/hooks'
import { can } from '../../api/roles'
import { Permissions, PLANT_STATUS_LABELS } from '../../api/types'
import { NotFound } from '../../components/Guards'
import { PlantActions } from '../../components/PlantActions'
import { PlantHistory } from '../../components/PlantHistory'
import { PlantPhotos } from '../../components/PlantPhotos'
import { Badge, Button, Card, ErrorBanner, Loading, PageHeader } from '../../components/ui'
import { formatDate } from '../../lib/format'

/** One plant: where it came from, where it is now, its photos, and everything that happened to it. */
export function PlantPage() {
  const params = useParams()
  const orgId = Number(params.orgId)
  const barcode = params.barcode ?? ''
  const plant = usePlant(orgId, barcode)

  if (plant.isPending) return <Loading />
  if (plant.error instanceof ApiError && plant.error.status === 404) return <NotFound message={`No plant with barcode ${barcode}.`} />
  if (plant.error) return <ErrorBanner error={plant.error} />
  const p = plant.data
  const living = p.status === 'growing' || p.status === 'for_sale'

  return (
    <div className="space-y-4">
      <PageHeader
        title={p.barcode}
        subtitle={
          <>
            {p.plantName}
            {p.variegation && ` ${p.variegation}`}
          </>
        }
        actions={<Badge tone={p.status === 'for_sale' ? 'green' : p.status === 'held' ? 'amber' : living ? 'neutral' : 'red'}>{PLANT_STATUS_LABELS[p.status]}</Badge>}
      />

      <Card>
        <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm sm:grid-cols-4">
          <Detail label="Pot">{p.potSize}</Detail>
          <Detail label="Batch">
            <Link to={`/orgs/${orgId}/batches/${p.batchId}`} className="font-medium text-emerald-800 underline dark:text-emerald-400">
              {p.batchCode}
            </Link>
          </Detail>
          <Detail label="Added">{formatDate(p.createdAt)}</Detail>
          {p.discardedAt && (
            <Detail label="Discarded">
              {formatDate(p.discardedAt)}
              {p.discardReason && ` · ${p.discardReason}`}
            </Detail>
          )}
        </dl>
        <PlantNotes orgId={orgId} barcode={p.barcode} notes={p.notes} />
      </Card>

      {living && (
        <Card title="Actions">
          <PlantActions orgId={orgId} barcodes={[p.barcode]} />
        </Card>
      )}

      <Card title="Photos">
        <PlantPhotos orgId={orgId} barcode={p.barcode} />
      </Card>

      <Card title="History">
        <PlantHistory history={p.history} />
      </Card>
    </div>
  )
}

function Detail({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-stone-500">{label}</dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  )
}

/** The plant's notes (e.g. pests and treatments). People who edit inventory can change them. */
function PlantNotes({ orgId, barcode, notes }: { orgId: number; barcode: string; notes: string | null }) {
  const canEdit = can(useMembership(orgId), Permissions.InventoryEdit)
  const { setNotes } = usePlantActions(orgId)
  const [editing, setEditing] = useState(false)
  const [text, setText] = useState(notes ?? '')

  if (!editing)
    return (
      <div className="mt-3 flex items-start justify-between gap-2 text-sm">
        <p className={notes ? '' : 'text-stone-500 italic'}>{notes ?? 'No notes'}</p>
        {canEdit && (
          <Button
            variant="ghost"
            onClick={() => {
              setText(notes ?? '')
              setEditing(true)
            }}
          >
            Edit notes
          </Button>
        )}
      </div>
    )

  return (
    <div className="mt-3 space-y-2">
      <textarea
        aria-label="Notes"
        rows={3}
        maxLength={2000}
        value={text}
        onChange={(e) => setText(e.target.value)}
        className="block w-full rounded-lg border border-stone-300 bg-white px-3 py-2 text-sm focus:border-emerald-600 focus:ring-2 focus:ring-emerald-600/30 focus:outline-none dark:border-stone-700 dark:bg-stone-950"
      />
      <ErrorBanner error={setNotes.error} />
      <div className="flex gap-2">
        <Button disabled={setNotes.isPending} onClick={() => setNotes.mutate({ barcode, notes: text }, { onSuccess: () => setEditing(false) })}>
          Save notes
        </Button>
        <Button variant="secondary" onClick={() => setEditing(false)}>
          Cancel
        </Button>
      </div>
    </div>
  )
}
