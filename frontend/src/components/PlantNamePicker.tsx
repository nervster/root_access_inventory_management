import { useEffect, useState } from 'react'
import { usePlantNames } from '../api/hooks'
import type { PlantNameDto } from '../api/types'
import { Button, Field, Input } from './ui'

/** Type part of a plant's name ("dragon") and pick it from the matches. */
export function PlantNamePicker({ orgId, value, onChange }: { orgId: number; value: PlantNameDto | null; onChange: (name: PlantNameDto | null) => void }) {
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
