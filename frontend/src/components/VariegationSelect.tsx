import { useState } from 'react'
import { useNameActions, useVariegations } from '../api/hooks'
import { Button, ErrorBanner, Field, Input, Select } from './ui'

const addNew = 'add'

/**
 * Choose a variegation (or None), with "+ Add a variegation…" for the nursery's own. value is the
 * variegation's ID as a string, '' for none.
 */
export function VariegationSelect({
  orgId,
  value,
  onChange,
  label = 'Variegation',
  keepOption,
}: {
  orgId: number
  value: string
  onChange: (id: string) => void
  label?: string
  /** Offer "keep as is" (value "keep") first, for corrections. */
  keepOption?: boolean
}) {
  const variegations = useVariegations(orgId)
  const { addVariegation } = useNameActions(orgId)
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')

  if (adding)
    return (
      <Field label={`New ${label.toLowerCase()}`}>
        <div className="flex gap-2">
          <Input autoFocus maxLength={100} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Sport" />
          <Button
            type="button"
            disabled={!name.trim() || addVariegation.isPending}
            onClick={() =>
              addVariegation.mutate(
                { name },
                {
                  onSuccess: (added) => {
                    onChange(String(added.id))
                    setAdding(false)
                    setName('')
                  },
                },
              )
            }
          >
            Add
          </Button>
          <Button type="button" variant="secondary" onClick={() => setAdding(false)}>
            Cancel
          </Button>
        </div>
        <ErrorBanner error={addVariegation.error} />
      </Field>
    )

  return (
    <Field label={label}>
      <Select value={value} onChange={(e) => (e.target.value === addNew ? setAdding(true) : onChange(e.target.value))}>
        {keepOption && <option value="keep">Keep as is</option>}
        <option value="">None</option>
        {variegations.data?.map((v) => (
          <option key={v.id} value={v.id}>
            {v.name}
          </option>
        ))}
        <option value={addNew}>+ Add a variegation…</option>
      </Select>
    </Field>
  )
}
