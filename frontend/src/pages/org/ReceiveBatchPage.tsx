import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { useMembership, usePlantActions, usePlantNames, usePotSizes, useVariegations } from '../../api/hooks'
import { can } from '../../api/roles'
import { Permissions, type PlantNameDto } from '../../api/types'
import { Button, Card, ErrorBanner, Field, Input, Loading, PageHeader, Select } from '../../components/ui'

/** Receive a batch: NMS creates one plant per unit, each with its own barcode. */
export function ReceiveBatchPage() {
  const orgId = Number(useParams().orgId)
  const navigate = useNavigate()
  const membership = useMembership(orgId)
  const potSizes = usePotSizes(orgId)
  const variegations = useVariegations(orgId)
  const { receiveBatch } = usePlantActions(orgId)

  const [plantName, setPlantName] = useState<PlantNameDto | null>(null)
  const [variegationId, setVariegationId] = useState('')
  const [quantity, setQuantity] = useState('')
  const [potSizeId, setPotSizeId] = useState('')
  const [receivedOn, setReceivedOn] = useState(() => new Date().toLocaleDateString('en-CA')) // today, YYYY-MM-DD
  const [source, setSource] = useState('')
  const [notes, setNotes] = useState('')
  const [cost, setCost] = useState('')
  const seesCost = can(membership, Permissions.FinancialsView)

  if (potSizes.isPending) return <Loading />
  if (potSizes.data?.length === 0)
    return (
      <Card>
        <p className="text-sm">
          Add your pot sizes first, so new plants have a pot to start in.{' '}
          <Link to={`/orgs/${orgId}/pot-sizes`} className="font-medium text-emerald-800 underline dark:text-emerald-400">
            Pot sizes
          </Link>
        </p>
      </Card>
    )

  // Until someone picks one, new plants start in the smallest pot (usually the Tissue Culture Cup).
  const startPot = potSizeId || String(potSizes.data?.[0]?.id ?? '')
  const count = Number(quantity)
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!plantName) return
    receiveBatch.mutate(
      {
        plantNameId: plantName.id,
        variegationId: variegationId ? Number(variegationId) : undefined,
        potSizeId: Number(startPot),
        quantity: count,
        receivedOn,
        source: source || undefined,
        notes: notes || undefined,
        unitCostCents: seesCost && cost ? Math.round(Number(cost) * 100) : undefined,
      },
      { onSuccess: (result) => navigate(`/orgs/${orgId}/batches/${result.batch.id}?received=1`) },
    )
  }

  return (
    <div className="space-y-4">
      <PageHeader title="Receive a batch" subtitle="Each plant gets its own barcode label." />
      <Card>
        <form onSubmit={submit} className="space-y-4">
          <PlantNamePicker orgId={orgId} value={plantName} onChange={setPlantName} />
          <Field label="Variegation">
            <Select value={variegationId} onChange={(e) => setVariegationId(e.target.value)}>
              <option value="">None</option>
              {variegations.data?.map((v) => (
                <option key={v.id} value={v.id}>
                  {v.name}
                </option>
              ))}
            </Select>
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="How many plants">
              <Input type="number" inputMode="numeric" required min={1} max={500} value={quantity} onChange={(e) => setQuantity(e.target.value)} />
            </Field>
            <Field label="Starts in">
              <Select value={startPot} onChange={(e) => setPotSizeId(e.target.value)}>
                {potSizes.data?.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Received">
              <Input type="date" required value={receivedOn} onChange={(e) => setReceivedOn(e.target.value)} />
            </Field>
            {seesCost && (
              <Field label="Cost per plant ($)" hint="Only Owners see this.">
                <Input type="number" inputMode="decimal" min={0} step="0.01" value={cost} onChange={(e) => setCost(e.target.value)} />
              </Field>
            )}
          </div>
          <Field label="From (lab or seller)">
            <Input maxLength={200} value={source} onChange={(e) => setSource(e.target.value)} />
          </Field>
          <Field label="Notes">
            <Input maxLength={2000} value={notes} onChange={(e) => setNotes(e.target.value)} />
          </Field>
          <ErrorBanner error={receiveBatch.error} />
          <Button type="submit" disabled={!plantName || !count || receiveBatch.isPending} className="w-full sm:w-auto">
            {receiveBatch.isPending ? 'Receiving…' : count > 0 ? `Receive ${count} plant${count === 1 ? '' : 's'}` : 'Receive plants'}
          </Button>
        </form>
      </Card>
    </div>
  )
}

/** Type part of a plant's name ("dragon") and pick it from the matches. */
function PlantNamePicker({ orgId, value, onChange }: { orgId: number; value: PlantNameDto | null; onChange: (name: PlantNameDto | null) => void }) {
  const [text, setText] = useState('')
  const [search, setSearch] = useState('')
  const names = usePlantNames(orgId, search)

  // Wait for a pause in typing before searching.
  useEffect(() => {
    const timer = setTimeout(() => setSearch(text), 250)
    return () => clearTimeout(timer)
  }, [text])

  if (value)
    return (
      <Field label="Plant">
        <div className="flex items-center justify-between gap-2 rounded-lg border border-stone-300 px-3 py-2 dark:border-stone-700">
          <span className="text-sm font-medium">{value.fullName}</span>
          <Button type="button" variant="ghost" onClick={() => onChange(null)}>
            Change
          </Button>
        </div>
      </Field>
    )

  return (
    <Field label="Plant" hint="Type part of the name, e.g. dragon or alocasia.">
      <Input autoFocus value={text} onChange={(e) => setText(e.target.value)} placeholder="Search plant names" />
      {search.trim().length >= 2 && (
        <ul className="mt-1 max-h-64 overflow-y-auto rounded-lg border border-stone-200 dark:border-stone-800">
          {names.isPending && <li className="px-3 py-2 text-sm text-stone-500">Searching…</li>}
          {names.data?.length === 0 && <li className="px-3 py-2 text-sm text-stone-500">No matches.</li>}
          {names.data?.map((name) => (
            <li key={name.id}>
              <button
                type="button"
                onClick={() => onChange(name)}
                className="block min-h-11 w-full px-3 py-2 text-left text-sm hover:bg-stone-100 dark:hover:bg-stone-800"
              >
                <span className="font-medium">{name.fullName}</span>
                {name.notes && <span className="block text-xs text-stone-500">{name.notes}</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
    </Field>
  )
}
