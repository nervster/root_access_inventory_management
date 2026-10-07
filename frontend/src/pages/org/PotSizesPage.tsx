import { useState, type FormEvent } from 'react'
import { useParams } from 'react-router'
import { useMembership, usePotSizeActions, usePotSizes, type PotSizeInput } from '../../api/hooks'
import { can } from '../../api/roles'
import { Permissions, type PotSizeDto } from '../../api/types'
import { Badge, Button, Card, ErrorBanner, Field, Input, Loading, PageHeader } from '../../components/ui'

/** The nursery's pot sizes, smallest first. Plants move up through them as they grow. */
export function PotSizesPage() {
  const orgId = Number(useParams().orgId)
  const membership = useMembership(orgId)
  const sizes = usePotSizes(orgId)
  const actions = usePotSizeActions(orgId)
  const [editing, setEditing] = useState<number | null>(null)
  const canEdit = can(membership, Permissions.InventoryEdit)

  if (sizes.isPending) return <Loading />
  if (sizes.error) return <ErrorBanner error={sizes.error} />
  const list = sizes.data

  const move = (index: number, by: -1 | 1) => {
    const ids = list.map((s) => s.id)
    ;[ids[index], ids[index + by]] = [ids[index + by], ids[index]]
    actions.reorder.mutate(ids)
  }

  return (
    <div className="space-y-4">
      <PageHeader title="Pot sizes" subtitle="Smallest first. Plants move up through these as they grow." />

      <Card>
        <ErrorBanner error={actions.reorder.error ?? actions.remove.error} />
        {list.length === 0 && <p className="text-sm text-stone-600 dark:text-stone-400">No pot sizes yet.</p>}
        <ul className="divide-y divide-stone-200 dark:divide-stone-800">
          {list.map((size, index) =>
            editing === size.id ? (
              <li key={size.id} className="py-3">
                <PotSizeForm
                  initial={size}
                  submitLabel="Save"
                  pending={actions.update.isPending}
                  error={actions.update.error}
                  onCancel={() => setEditing(null)}
                  onSubmit={(input) => actions.update.mutate({ id: size.id, ...input }, { onSuccess: () => setEditing(null) })}
                />
              </li>
            ) : (
              <li key={size.id} className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                  <p className="flex flex-wrap items-center gap-2 font-medium">
                    {size.name}
                    {size.sellable ? <Badge tone="green">Can be sold</Badge> : <Badge>Grow-on only</Badge>}
                  </p>
                  <p className="text-sm text-stone-500">
                    {dimensions(size)}
                    {size.description && <> · {size.description}</>}
                  </p>
                </div>
                {canEdit && (
                  <div className="flex gap-1">
                    <Button variant="ghost" aria-label={`Move ${size.name} up`} disabled={index === 0 || actions.reorder.isPending} onClick={() => move(index, -1)}>
                      ↑
                    </Button>
                    <Button
                      variant="ghost"
                      aria-label={`Move ${size.name} down`}
                      disabled={index === list.length - 1 || actions.reorder.isPending}
                      onClick={() => move(index, 1)}
                    >
                      ↓
                    </Button>
                    <Button variant="secondary" onClick={() => setEditing(size.id)}>
                      Edit
                    </Button>
                    <Button
                      variant="danger"
                      disabled={actions.remove.isPending}
                      onClick={() => window.confirm(`Delete ${size.name}?`) && actions.remove.mutate(size.id)}
                    >
                      Delete
                    </Button>
                  </div>
                )}
              </li>
            ),
          )}
        </ul>
      </Card>

      {canEdit && (
        <Card title="Add a pot size">
          <PotSizeForm key={list.length} submitLabel="Add" pending={actions.create.isPending} error={actions.create.error} onSubmit={(input) => actions.create.mutate(input)} />
        </Card>
      )}
    </div>
  )
}

function dimensions(size: PotSizeDto) {
  const parts = [
    size.diameterInches != null && `${size.diameterInches} in wide`,
    size.heightInches != null && `${size.heightInches} in tall`,
  ].filter(Boolean)
  return parts.length > 0 ? parts.join(', ') : 'No size given'
}

function PotSizeForm({
  initial,
  submitLabel,
  pending,
  error,
  onSubmit,
  onCancel,
}: {
  initial?: PotSizeDto
  submitLabel: string
  pending: boolean
  error: unknown
  onSubmit: (input: PotSizeInput) => void
  onCancel?: () => void
}) {
  const [name, setName] = useState(initial?.name ?? '')
  const [diameter, setDiameter] = useState(initial?.diameterInches?.toString() ?? '')
  const [height, setHeight] = useState(initial?.heightInches?.toString() ?? '')
  const [sellable, setSellable] = useState(initial?.sellable ?? true)
  const [description, setDescription] = useState(initial?.description ?? '')

  const submit = (event: FormEvent) => {
    event.preventDefault()
    onSubmit({
      name,
      diameterInches: diameter ? Number(diameter) : undefined,
      heightInches: height ? Number(height) : undefined,
      sellable,
      description,
    })
  }

  return (
    <form onSubmit={submit} className="space-y-3">
      <Field label="Name">
        <Input required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} placeholder="Small Cup" />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Diameter (inches)">
          <Input type="number" inputMode="decimal" min="0.1" step="0.1" value={diameter} onChange={(e) => setDiameter(e.target.value)} />
        </Field>
        <Field label="Height (inches)">
          <Input type="number" inputMode="decimal" min="0.1" step="0.1" value={height} onChange={(e) => setHeight(e.target.value)} />
        </Field>
      </div>
      <label className="flex items-start gap-2 text-sm">
        <input type="checkbox" className="mt-0.5 size-4 accent-emerald-700" checked={sellable} onChange={(e) => setSellable(e.target.checked)} />
        <span>
          <span className="font-medium">Plants in this pot can be sold</span>
          <span className="block text-stone-500">Turn off for the Tissue Culture Cup: plants grow on before they're sold.</span>
        </span>
      </label>
      <Field label="Description" hint="Shown for this size on Shopify.">
        <Input maxLength={2000} value={description} onChange={(e) => setDescription(e.target.value)} />
      </Field>
      <ErrorBanner error={error} />
      <div className="flex gap-2">
        <Button type="submit" disabled={pending}>
          {pending ? 'Saving…' : submitLabel}
        </Button>
        {onCancel && (
          <Button type="button" variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
        )}
      </div>
    </form>
  )
}
