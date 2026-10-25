import { useState } from 'react'
import { usePlantActions } from '../api/hooks'
import type { PlantNameDto } from '../api/types'
import { NewVariants } from './NewVariants'
import { PlantNamePicker } from './PlantNamePicker'
import { Button, ErrorBanner, Notice } from './ui'
import { VariegationSelect } from './VariegationSelect'

/**
 * Fix what plants are: their name and/or variegation (a tissue-culture plant can lose its
 * variegation; a lab can mislabel a batch). Both fields start at "keep as is".
 */
export function CorrectPlantsForm({
  orgId,
  target,
  onDone,
}: {
  orgId: number
  /** A whole batch, or these plants. */
  target: { batchId: number } | { barcodes: string[] }
  onDone?: () => void
}) {
  const { correct } = usePlantActions(orgId)
  const [plantName, setPlantName] = useState<PlantNameDto | null>(null)
  const [variegation, setVariegation] = useState('keep')
  const changing = plantName !== null || variegation !== 'keep'

  const save = () =>
    correct.mutate(
      {
        ...target,
        plantNameId: plantName?.id,
        variegationId: variegation === 'keep' ? undefined : variegation === '' ? null : Number(variegation),
      },
      { onSuccess: () => onDone?.() },
    )

  return (
    <div className="space-y-3">
      <PlantNamePicker orgId={orgId} value={plantName} onChange={setPlantName} />
      {!plantName && <p className="-mt-2 text-xs text-stone-500">Leave empty to keep the plant name.</p>}
      <VariegationSelect orgId={orgId} value={variegation} onChange={setVariegation} keepOption />
      <ErrorBanner error={correct.error} />
      {correct.data && (
        <Notice>
          Corrected {correct.data.corrected}
          {correct.data.unchanged > 0 && ` · ${correct.data.unchanged} were already right`}.
        </Notice>
      )}
      {correct.data && <NewVariants orgId={orgId} variants={correct.data.newVariants} />}
      <Button disabled={!changing || correct.isPending} onClick={save}>
        {correct.isPending ? 'Saving…' : 'Save correction'}
      </Button>
    </div>
  )
}
