import { useState } from 'react'
import { useMembership, usePlantActions, usePotSizes } from '../api/hooks'
import { can } from '../api/roles'
import { Permissions } from '../api/types'
import type { NewVariantDto } from '../api/types'
import { CorrectPlantsForm } from './CorrectPlantsForm'
import { NewVariants } from './NewVariants'
import { Button, ErrorBanner, Input, Notice, Select } from './ui'

/**
 * Actions for some plants, by barcode: move to a pot, mark for sale or take off sale, discard.
 * Each button shows only to roles allowed to use it. The API checks every plant and names any
 * that can't be changed (unknown, sold, discarded, no price…), changing none of them.
 */
export function PlantActions({ orgId, barcodes, onDone }: { orgId: number; barcodes: string[]; onDone?: () => void }) {
  const membership = useMembership(orgId)
  const potSizes = usePotSizes(orgId)
  const actions = usePlantActions(orgId)
  const [potSizeId, setPotSizeId] = useState('')
  const [discarding, setDiscarding] = useState(false)
  const [correcting, setCorrecting] = useState(false)
  const [reason, setReason] = useState('')
  const [done, setDone] = useState<string | null>(null)
  const [newVariants, setNewVariants] = useState<NewVariantDto[]>([])

  const canEdit = can(membership, Permissions.InventoryEdit)
  const canSell = can(membership, Permissions.ListingsManage)
  const count = barcodes.length
  const plants = `${count} plant${count === 1 ? '' : 's'}`
  const busy = actions.repot.isPending || actions.markForSale.isPending || actions.unmarkForSale.isPending || actions.discard.isPending
  const error = actions.repot.error ?? actions.markForSale.error ?? actions.unmarkForSale.error ?? actions.discard.error

  const finished = (message: string, created: NewVariantDto[] = []) => {
    setDone(message)
    setNewVariants(created)
    onDone?.()
  }
  const reset = () => {
    setDone(null)
    setNewVariants([])
    ;[actions.repot, actions.markForSale, actions.unmarkForSale, actions.discard].forEach((m) => m.reset())
  }

  if (!canEdit && !canSell) return null
  if (count === 0) return null

  return (
    <div className="space-y-3">
      {canEdit && (
        <div className="flex gap-2">
          <Select aria-label="Move to pot size" value={potSizeId} onChange={(e) => setPotSizeId(e.target.value)}>
            <option value="">Move to pot…</option>
            {potSizes.data?.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </Select>
          <Button
            disabled={!potSizeId || busy}
            onClick={() => {
              reset()
              actions.repot.mutate(
                { potSizeId: Number(potSizeId), barcodes },
                { onSuccess: (r) => finished(`Moved ${r.moved}${r.unchanged ? ` · ${r.unchanged} already there` : ''}.`, r.newVariants) },
              )
            }}
          >
            Move
          </Button>
        </div>
      )}

      <div className="flex flex-wrap gap-2">
        {canSell && (
          <>
            <Button
              disabled={busy}
              onClick={() => {
                reset()
                actions.markForSale.mutate({ barcodes }, { onSuccess: (r) => finished(`Marked ${r.marked} for sale${r.alreadyForSale ? ` · ${r.alreadyForSale} already were` : ''}.`, r.newVariants) })
              }}
            >
              Mark for sale
            </Button>
            <Button
              variant="secondary"
              disabled={busy}
              onClick={() => {
                reset()
                actions.unmarkForSale.mutate(barcodes, { onSuccess: (r) => finished(`Took ${r.unmarked} off sale.`) })
              }}
            >
              Take off sale
            </Button>
          </>
        )}
        {canEdit && !correcting && (
          <Button variant="secondary" disabled={busy} onClick={() => setCorrecting(true)}>
            Correct name…
          </Button>
        )}
        {canEdit && !discarding && (
          <Button variant="danger" disabled={busy} onClick={() => setDiscarding(true)}>
            Discard…
          </Button>
        )}
      </div>

      {correcting && (
        <div className="space-y-2 rounded-lg border border-stone-200 p-3 dark:border-stone-800">
          <div className="flex items-center justify-between">
            <p className="text-sm font-medium">Correct the name or variegation of {plants}</p>
            <Button variant="ghost" onClick={() => setCorrecting(false)}>
              Close
            </Button>
          </div>
          <CorrectPlantsForm orgId={orgId} target={{ barcodes }} onDone={onDone} />
        </div>
      )}

      {discarding && (
        <div className="space-y-2 rounded-lg border border-red-200 p-3 dark:border-red-900">
          <p className="text-sm font-medium">Discard {plants}? They stay in the records, marked discarded.</p>
          <Input placeholder="Reason (optional), e.g. root rot" maxLength={500} value={reason} onChange={(e) => setReason(e.target.value)} />
          <div className="flex gap-2">
            <Button
              variant="danger"
              disabled={busy}
              onClick={() => {
                reset()
                actions.discard.mutate(
                  { barcodes, reason: reason || undefined },
                  {
                    onSuccess: (r) => {
                      setDiscarding(false)
                      setReason('')
                      finished(`Discarded ${r.discarded}.`)
                    },
                  },
                )
              }}
            >
              Discard {plants}
            </Button>
            <Button variant="secondary" onClick={() => setDiscarding(false)}>
              Cancel
            </Button>
          </div>
        </div>
      )}

      <ErrorBanner error={error} />
      {done && <Notice>{done}</Notice>}
      <NewVariants orgId={orgId} variants={newVariants} />
    </div>
  )
}
