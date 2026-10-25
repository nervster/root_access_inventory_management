import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router'
import { ApiError } from '../../api/client'
import { useBatch, useLookupPlant, useMembership } from '../../api/hooks'
import { can } from '../../api/roles'
import { PLANT_STATUS_LABELS, Permissions, type PlantDto } from '../../api/types'
import { CameraScanner } from '../../components/CameraScanner'
import { PlantActions } from '../../components/PlantActions'
import { Badge, Button, Card, Input, LinkButton, PageHeader } from '../../components/ui'
import { loadScanned, saveScanned, type Scanned } from '../../scanList'

/** Scan labels one after another into a list, then act on all of them at once. */
export function ScanPage() {
  const orgId = Number(useParams().orgId)
  const lookup = useLookupPlant(orgId)
  const membership = useMembership(orgId)
  const [scanned, setScanned] = useState<Scanned[]>(() => loadScanned(orgId))
  const [typed, setTyped] = useState('')
  // The barcodes in the list, updated immediately (state updates aren't), so a label scanned twice
  // in quick succession is still added only once.
  const inList = useRef(new Set(scanned.map((s) => s.barcode)))

  useEffect(() => saveScanned(orgId, scanned), [orgId, scanned])

  const describe = useCallback(
    async (barcode: string): Promise<Scanned> => {
      try {
        return { barcode, plant: await lookup(barcode) }
      } catch (error) {
        return { barcode, problem: error instanceof ApiError && error.status === 404 ? 'No plant with this barcode' : 'Couldn\'t look this up' }
      }
    },
    [lookup],
  )

  const add = useCallback(
    async (text: string) => {
      const barcode = text.trim().toUpperCase()
      if (!barcode || inList.current.has(barcode)) return // scanned twice: counts once
      inList.current.add(barcode)
      setScanned((list) => [{ barcode }, ...list])
      const result = await describe(barcode)
      setScanned((list) => list.map((s) => (s.barcode === barcode ? result : s)))
    },
    [describe],
  )

  // The rest of a batch goes right after the plant it came from, in barcode order, so a photo session
  // works through the batch in label order.
  const addPlants = (after: string, plants: PlantDto[]) => {
    const fresh = plants.filter((p) => !inList.current.has(p.barcode))
    fresh.forEach((p) => inList.current.add(p.barcode))
    setScanned((list) => {
      const at = list.findIndex((s) => s.barcode === after) + 1
      return [...list.slice(0, at), ...fresh.map((plant) => ({ barcode: plant.barcode, plant })), ...list.slice(at)]
    })
  }

  // After an action, show each plant's new pot and status.
  const refresh = async () => {
    const updated = await Promise.all(scanned.map((s) => describe(s.barcode)))
    setScanned(updated)
  }

  const submitTyped = (event: FormEvent) => {
    event.preventDefault()
    add(typed)
    setTyped('')
  }

  const ready = scanned.filter((s) => s.plant).map((s) => s.barcode)
  const newest = scanned.find((s) => s.plant)?.plant

  return (
    <div className="space-y-4">
      <PageHeader title="Scan" subtitle="Scan labels one after another, then act on all of them." />

      <Card>
        <CameraScanner onScan={add} />
        {/* Also for damaged labels, and handheld scanners (they type the code and press Enter). */}
        <form onSubmit={submitTyped} className="mt-3 flex gap-2">
          <Input
            aria-label="Barcode"
            placeholder="Or type a barcode, e.g. RAH-ADF-000123"
            autoCapitalize="characters"
            autoComplete="off"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
          />
          <Button type="submit" variant="secondary" disabled={!typed.trim()}>
            Add
          </Button>
        </form>
      </Card>

      <Card
        title={`Scanned (${scanned.length})`}
        actions={
          scanned.length > 0 && (
            <Button
              variant="ghost"
              onClick={() => {
                inList.current.clear()
                setScanned([])
              }}
            >
              Clear
            </Button>
          )
        }
      >
        {newest && (
          <AddRestOfBatch orgId={orgId} plant={newest} listed={new Set(scanned.map((s) => s.barcode))} onAdd={(plants) => addPlants(newest.barcode, plants)} />
        )}
        {scanned.length === 0 ? (
          <p className="text-sm text-stone-600 dark:text-stone-400">Nothing scanned yet.</p>
        ) : (
          <ul className="divide-y divide-stone-200 dark:divide-stone-800">
            {scanned.map((s) => (
              <li key={s.barcode} className="flex items-center gap-2 py-2">
                <div className="min-w-0 flex-1">
                  {s.plant ? (
                    <Link to={`/orgs/${orgId}/plants/${s.barcode}`} className="font-mono text-sm font-medium text-emerald-800 hover:underline dark:text-emerald-400">
                      {s.barcode}
                    </Link>
                  ) : (
                    <span className="font-mono text-sm font-medium">{s.barcode}</span>
                  )}
                  <p className="truncate text-sm text-stone-600 dark:text-stone-400">
                    {s.plant
                      ? `${s.plant.plantName}${s.plant.variegation ? ` ${s.plant.variegation}` : ''} · ${s.plant.potSize}`
                      : (s.problem ?? 'Looking up…')}
                  </p>
                </div>
                {s.plant && <Badge tone={s.plant.status === 'for_sale' ? 'green' : s.plant.status === 'held' || s.plant.status === 'returned' ? 'amber' : s.plant.status === 'growing' ? 'neutral' : 'red'}>{PLANT_STATUS_LABELS[s.plant.status]}</Badge>}
                {s.problem && <Badge tone="red">Not found</Badge>}
                <Button variant="ghost" aria-label={`Remove ${s.barcode} from the list`} onClick={() => {
                    inList.current.delete(s.barcode)
                    setScanned((list) => list.filter((x) => x.barcode !== s.barcode))
                  }}>
                  ✕
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Card>

      {ready.length > 0 && can(membership, Permissions.InventoryEdit) && (
        <LinkButton to={`/orgs/${orgId}/scan/photos`} className="w-full">
          Take photos of {ready.length === 1 ? 'this plant' : `these ${ready.length} plants`}
        </LinkButton>
      )}

      {ready.length > 0 && (
        <Card title={`With these ${ready.length} plant${ready.length === 1 ? '' : 's'}`}>
          <PlantActions orgId={orgId} barcodes={ready} onDone={refresh} />
        </Card>
      )}
    </div>
  )
}

/**
 * Offers the rest of the newest scanned plant's batch: its growing and for-sale plants that aren't in
 * the list yet (sold, discarded, held and returned ones are left out).
 */
function AddRestOfBatch({ orgId, plant, listed, onAdd }: { orgId: number; plant: PlantDto; listed: Set<string>; onAdd: (plants: PlantDto[]) => void }) {
  const batch = useBatch(orgId, plant.batchId)
  const rest = (batch.data?.plants ?? [])
    .filter((p) => (p.status === 'growing' || p.status === 'for_sale') && !listed.has(p.barcode))
    .sort((a, b) => a.barcode.localeCompare(b.barcode))
  if (rest.length === 0) return null
  return (
    <Button variant="secondary" className="mb-3 w-full" onClick={() => onAdd(rest)}>
      Add the rest of {plant.batchCode} ({rest.length} more plant{rest.length === 1 ? '' : 's'})
    </Button>
  )
}
