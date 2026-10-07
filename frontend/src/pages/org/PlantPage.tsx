import { Link, useParams } from 'react-router'
import { ApiError } from '../../api/client'
import { usePlant } from '../../api/hooks'
import { PLANT_STATUS_LABELS } from '../../api/types'
import { NotFound } from '../../components/Guards'
import { PlantActions } from '../../components/PlantActions'
import { PlantPhotos } from '../../components/PlantPhotos'
import { Badge, Card, ErrorBanner, Loading, PageHeader } from '../../components/ui'
import { formatDate } from '../../lib/format'

/** One plant: where it came from, where it is now, and its pot history. */
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
        actions={<Badge tone={p.status === 'for_sale' ? 'green' : living ? 'neutral' : 'red'}>{PLANT_STATUS_LABELS[p.status]}</Badge>}
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
        {p.notes && <p className="mt-3 text-sm">{p.notes}</p>}
      </Card>

      {living && (
        <Card title="Actions">
          <PlantActions orgId={orgId} barcodes={[p.barcode]} />
        </Card>
      )}

      <Card title="Photos">
        <PlantPhotos orgId={orgId} barcode={p.barcode} />
      </Card>

      <Card title="Pot history">
        <ol className="space-y-3 border-l-2 border-stone-200 pl-4 dark:border-stone-800">
          {[...p.potHistory].reverse().map((change, i) => (
            <li key={i} className="relative text-sm">
              <span className="absolute top-1.5 -left-[21px] size-2 rounded-full bg-emerald-700" aria-hidden="true" />
              <p className="font-medium">{change.fromPotSize ? `${change.fromPotSize} → ${change.toPotSize}` : `Started in ${change.toPotSize}`}</p>
              <p className="text-stone-500">
                {formatDate(change.changedAt)}
                {change.changedBy && ` · ${change.changedBy}`}
              </p>
            </li>
          ))}
        </ol>
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
