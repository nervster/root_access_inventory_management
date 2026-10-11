import { useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'
import { useBatch, useMembership, usePlantActions, usePotSizes } from '../../api/hooks'
import { can } from '../../api/roles'
import { Permissions, PLANT_STATUS_LABELS, type BatchDto, type PlantDto, type PlantStatus } from '../../api/types'
import { CorrectPlantsForm } from '../../components/CorrectPlantsForm'
import { NewVariants } from '../../components/NewVariants'
import { Badge, Button, Card, ErrorBanner, Field, Input, LinkButton, Loading, Notice, PageHeader, Select } from '../../components/ui'
import { dollarsToCents, formatDay, formatMoney } from '../../lib/format'

/** One batch: what arrived, where its plants are now, and actions for the whole batch. */
export function BatchPage() {
  const params = useParams()
  const orgId = Number(params.orgId)
  const batchId = Number(params.batchId)
  const [search] = useSearchParams()
  const membership = useMembership(orgId)
  const batch = useBatch(orgId, batchId)
  const potSizes = usePotSizes(orgId)

  if (batch.isPending) return <Loading />
  if (batch.error) return <ErrorBanner error={batch.error} />
  const { batch: b, plants } = batch.data
  const { growing, forSale, held, sold, discarded } = b.plantCounts
  const living = growing + forSale

  // Group plants by their current pot, smallest pot first.
  const groups = (potSizes.data ?? [])
    .map((pot) => ({ pot, plants: plants.filter((p) => p.potSizeId === pot.id) }))
    .filter((group) => group.plants.length > 0)

  return (
    <div className="space-y-4">
      <PageHeader
        title={`${b.code} · ${b.plantName}${b.variegation ? ` ${b.variegation}` : ''}`}
        subtitle={
          <>
            Received {formatDay(b.receivedOn)}
            {b.source && <> from {b.source}</>}
            {b.unitCostCents != null && <> · {formatMoney(b.unitCostCents)} each</>}
          </>
        }
        actions={
          living > 0 && (
            <LinkButton to={`/orgs/${orgId}/batches/${b.id}/labels`} variant={search.has('received') ? 'primary' : 'secondary'}>
              Print labels
            </LinkButton>
          )
        }
      />
      {search.has('received') && <Notice>Received {plants.length} plants. Print their labels and stick one on each pot.</Notice>}

      <p className="text-sm text-stone-600 dark:text-stone-400">
        {[
          [growing, 'growing'],
          [forSale, 'for sale'],
          [held, 'held for orders'],
          [sold, 'sold'],
          [discarded, 'discarded'],
        ]
          .filter(([n]) => (n as number) > 0)
          .map(([n, label]) => `${n} ${label}`)
          .join(' · ')}
      </p>
      {b.notes && <p className="text-sm">{b.notes}</p>}

      {living > 0 && (
        <div className="grid gap-4 sm:grid-cols-2">
          {can(membership, Permissions.InventoryEdit) && <RepotBatch orgId={orgId} batchId={b.id} />}
          {can(membership, Permissions.ListingsManage) && <MarkBatchForSale orgId={orgId} batchId={b.id} />}
        </div>
      )}

      {can(membership, Permissions.InventoryEdit) && (
        <div className="grid gap-4 sm:grid-cols-2">
          <EditBatchDetails orgId={orgId} batch={b} seesCost={can(membership, Permissions.FinancialsView)} />
          {living > 0 && (
            <Card title="Correct the whole batch">
              <p className="mb-3 text-xs text-stone-500">If the batch isn't what it was labeled as. Its living plants are corrected too.</p>
              <CorrectPlantsForm orgId={orgId} target={{ batchId: b.id }} />
            </Card>
          )}
        </div>
      )}

      {groups.map(({ pot, plants }) => (
        <Card key={pot.id} title={`${pot.name} (${plants.length})`}>
          <ul className="grid grid-cols-1 gap-x-6 sm:grid-cols-2 lg:grid-cols-3">
            {plants.map((plant) => (
              <PlantRow key={plant.id} orgId={orgId} plant={plant} />
            ))}
          </ul>
        </Card>
      ))}
    </div>
  )
}

const statusTone: Record<PlantStatus, 'neutral' | 'green' | 'red' | 'amber'> = {
  growing: 'neutral',
  for_sale: 'green',
  held: 'amber',
  sold: 'amber',
  discarded: 'red',
}

function PlantRow({ orgId, plant }: { orgId: number; plant: PlantDto }) {
  return (
    <li className="flex items-center justify-between gap-2 border-b border-stone-100 py-2 last:border-0 dark:border-stone-800">
      <Link to={`/orgs/${orgId}/plants/${plant.barcode}`} className="font-mono text-sm text-emerald-800 hover:underline dark:text-emerald-400">
        {plant.barcode}
      </Link>
      <Badge tone={statusTone[plant.status]}>{PLANT_STATUS_LABELS[plant.status]}</Badge>
    </li>
  )
}

/** Move every living plant in the batch to a new pot size. */
function RepotBatch({ orgId, batchId }: { orgId: number; batchId: number }) {
  const potSizes = usePotSizes(orgId)
  const { repot } = usePlantActions(orgId)
  const [potSizeId, setPotSizeId] = useState('')

  return (
    <Card title="Repot the whole batch">
      <div className="flex gap-2">
        <Select aria-label="New pot size" value={potSizeId} onChange={(e) => setPotSizeId(e.target.value)}>
          <option value="">Choose a pot size…</option>
          {potSizes.data?.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </Select>
        <Button disabled={!potSizeId || repot.isPending} onClick={() => repot.mutate({ potSizeId: Number(potSizeId), batchId })}>
          Move
        </Button>
      </div>
      <p className="mt-2 text-xs text-stone-500">Plants that grew differently can be moved one by one from Scan.</p>
      <ErrorBanner error={repot.error} />
      {repot.data && (
        <div className="mt-2 space-y-2 text-sm">
          <p>
            Moved {repot.data.moved}
            {repot.data.unchanged > 0 && <> · {repot.data.unchanged} were already in that pot</>}
          </p>
          <NewVariants orgId={orgId} variants={repot.data.newVariants} />
        </div>
      )}
    </Card>
  )
}

/** Mark every plant in the batch that can be sold: in a sellable pot, with a price. */
function MarkBatchForSale({ orgId, batchId }: { orgId: number; batchId: number }) {
  const { markForSale } = usePlantActions(orgId)
  const result = markForSale.data

  return (
    <Card title="Put the batch up for sale">
      <Button disabled={markForSale.isPending} onClick={() => markForSale.mutate({ batchId })}>
        Mark for sale
      </Button>
      <p className="mt-2 text-xs text-stone-500">Plants in a pot that can be sold go on sale. New kinds get their own SKU, priced on Products.</p>
      <ErrorBanner error={markForSale.error} />
      {result && (
        <div className="mt-2 space-y-1 text-sm">
          <p>
            Marked {result.marked}
            {result.alreadyForSale > 0 && <> · {result.alreadyForSale} were already for sale</>}
          </p>
          {result.skipped.unsellablePot > 0 && <p className="text-stone-600 dark:text-stone-400">{result.skipped.unsellablePot} are still in a pot that can't be sold.</p>}
          <NewVariants orgId={orgId} variants={result.newVariants} />
        </div>
      )}
    </Card>
  )
}

/** Fix a batch's details: where it came from, notes, the date it arrived, and (Owners) its cost. */
function EditBatchDetails({ orgId, batch, seesCost }: { orgId: number; batch: BatchDto; seesCost: boolean }) {
  const { updateBatch } = usePlantActions(orgId)
  const [editing, setEditing] = useState(false)
  const [source, setSource] = useState('')
  const [notes, setNotes] = useState('')
  const [receivedOn, setReceivedOn] = useState('')
  const [cost, setCost] = useState('')

  const start = () => {
    setSource(batch.source ?? '')
    setNotes(batch.notes ?? '')
    setReceivedOn(batch.receivedOn)
    setCost(batch.unitCostCents != null ? (batch.unitCostCents / 100).toFixed(2) : '')
    updateBatch.reset()
    setEditing(true)
  }

  const save = () =>
    updateBatch.mutate(
      {
        batchId: batch.id,
        source,
        notes,
        receivedOn,
        ...(seesCost ? { unitCostCents: cost === '' ? null : dollarsToCents(cost) } : {}),
      },
      { onSuccess: () => setEditing(false) },
    )

  return (
    <Card title="Batch details" actions={!editing && <Button variant="ghost" onClick={start}>Edit</Button>}>
      {editing ? (
        <div className="space-y-3">
          <Field label="From (lab or seller)">
            <Input maxLength={200} value={source} onChange={(e) => setSource(e.target.value)} />
          </Field>
          <Field label="Received">
            <Input type="date" required value={receivedOn} onChange={(e) => setReceivedOn(e.target.value)} />
          </Field>
          {seesCost && (
            <Field label="Cost per plant ($)" hint="Leave empty if unknown. Only Owners see this.">
              <Input type="number" inputMode="decimal" min={0} step="0.01" value={cost} onChange={(e) => setCost(e.target.value)} />
            </Field>
          )}
          <Field label="Notes">
            <Input maxLength={2000} value={notes} onChange={(e) => setNotes(e.target.value)} />
          </Field>
          <ErrorBanner error={updateBatch.error} />
          <div className="flex gap-2">
            <Button disabled={updateBatch.isPending || !receivedOn} onClick={save}>
              Save
            </Button>
            <Button variant="secondary" onClick={() => setEditing(false)}>
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <dl className="space-y-1 text-sm">
          <div>
            <dt className="inline text-stone-500">From: </dt>
            <dd className="inline">{batch.source ?? '—'}</dd>
          </div>
          <div>
            <dt className="inline text-stone-500">Received: </dt>
            <dd className="inline">{formatDay(batch.receivedOn)}</dd>
          </div>
          {seesCost && (
            <div>
              <dt className="inline text-stone-500">Cost per plant: </dt>
              <dd className="inline">{batch.unitCostCents != null ? formatMoney(batch.unitCostCents) : '—'}</dd>
            </div>
          )}
          <div>
            <dt className="inline text-stone-500">Notes: </dt>
            <dd className="inline">{batch.notes ?? '—'}</dd>
          </div>
        </dl>
      )}
    </Card>
  )
}
