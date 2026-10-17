import { useEffect, useState, type FormEvent } from 'react'
import { useNameActions, usePlantNames } from '../api/hooks'
import type { PlantNameDto } from '../api/types'
import { Button, ErrorBanner, Field, Input, Select } from './ui'

type Rank = PlantNameDto['rank']

/** What can go under each rank (a cultivar under a genus is a hybrid). Mirrors childRanks in names.go. */
const childRanks: Record<Rank, Rank[]> = { family: ['genus'], genus: ['species', 'cultivar'], species: ['cultivar'], cultivar: [] }
const rankLabels: Record<Rank, string> = { family: 'Family', genus: 'Genus', species: 'Species', cultivar: 'Cultivar' }

/** Waits for a pause in typing before searching. */
function useDebounced(value: string, ms = 250) {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), ms)
    return () => clearTimeout(timer)
  }, [value, ms])
  return debounced
}

/** Type part of a plant's name ("dragon") and pick it from the matches, or add a new one. */
export function PlantNamePicker({ orgId, value, onChange }: { orgId: number; value: PlantNameDto | null; onChange: (name: PlantNameDto | null) => void }) {
  const [text, setText] = useState('')
  const search = useDebounced(text)
  const names = usePlantNames(orgId, search)
  const [adding, setAdding] = useState(false)

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

  if (adding)
    return (
      <AddPlantName
        orgId={orgId}
        initialName={text}
        onAdded={(name) => {
          setAdding(false)
          onChange(name)
        }}
        onCancel={() => setAdding(false)}
      />
    )

  // The results sit beside the Field, not in it: Field is a <label>, and taps on buttons inside a
  // label get sent to its input instead (so on phones nothing could be picked).
  return (
    <div>
      <Field label="Plant" hint="Type part of the name, e.g. dragon or alocasia.">
        <Input autoFocus value={text} onChange={(e) => setText(e.target.value)} placeholder="Search plant names" />
      </Field>
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
                {!name.shared && <span className="ml-2 text-xs text-emerald-700 dark:text-emerald-400">yours</span>}
                {name.notes && <span className="block text-xs text-stone-500">{name.notes}</span>}
              </button>
            </li>
          ))}
          <li className="border-t border-stone-200 dark:border-stone-800">
            <button type="button" onClick={() => setAdding(true)} className="block min-h-11 w-full px-3 py-2 text-left text-sm font-medium text-emerald-800 hover:bg-stone-100 dark:text-emerald-400 dark:hover:bg-stone-800">
              Can't find it? Add a plant name
            </button>
          </li>
        </ul>
      )}
    </div>
  )
}

/** Add one of the nursery's own names under an existing one, e.g. a cultivar under its species. */
function AddPlantName({ orgId, initialName, onAdded, onCancel }: { orgId: number; initialName: string; onAdded: (name: PlantNameDto) => void; onCancel: () => void }) {
  const { addPlantName } = useNameActions(orgId)
  const [parentText, setParentText] = useState('')
  const parentSearch = useDebounced(parentText)
  const parents = usePlantNames(orgId, parentSearch, true)
  const [parent, setParent] = useState<PlantNameDto | null>(null)
  const [rank, setRank] = useState<Rank | ''>('')
  const [name, setName] = useState(initialName.trim())
  const [notes, setNotes] = useState('')

  const ranks = parent ? childRanks[parent.rank] : []
  const chosenRank = rank && ranks.includes(rank) ? rank : ranks[ranks.length - 1] // default: the most specific (cultivar)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!parent || !chosenRank) return
    addPlantName.mutate({ parentId: parent.id, rank: chosenRank, name, notes: notes || undefined }, { onSuccess: onAdded })
  }

  return (
    <div className="space-y-3 rounded-lg border border-stone-200 p-3 dark:border-stone-800">
      <p className="text-sm font-medium">Add a plant name</p>
      {parent ? (
        <Field label="Goes under">
          <div className="flex items-center justify-between gap-2 rounded-lg border border-stone-300 px-3 py-2 dark:border-stone-700">
            <span className="text-sm">
              {parent.fullName} <span className="text-stone-500">({rankLabels[parent.rank].toLowerCase()})</span>
            </span>
            <Button type="button" variant="ghost" onClick={() => setParent(null)}>
              Change
            </Button>
          </div>
        </Field>
      ) : (
        <div>
          <Field label="Goes under" hint="The species for a cultivar, the genus for a species (or a hybrid), the family for a genus.">
            <Input autoFocus value={parentText} onChange={(e) => setParentText(e.target.value)} placeholder="Search, e.g. alocasia or araceae" />
          </Field>
          {parentSearch.trim().length >= 2 && (
            <ul className="mt-1 max-h-48 overflow-y-auto rounded-lg border border-stone-200 dark:border-stone-800">
              {parents.data
                ?.filter((p) => childRanks[p.rank].length > 0)
                .map((p) => (
                  <li key={p.id}>
                    <button type="button" onClick={() => setParent(p)} className="block min-h-11 w-full px-3 py-2 text-left text-sm hover:bg-stone-100 dark:hover:bg-stone-800">
                      {p.fullName} <span className="text-xs text-stone-500">{rankLabels[p.rank].toLowerCase()}</span>
                    </button>
                  </li>
                ))}
            </ul>
          )}
        </div>
      )}

      {parent && (
        <form onSubmit={submit} className="space-y-3">
          {ranks.length > 1 && (
            <Field label="It's a">
              <Select value={chosenRank} onChange={(e) => setRank(e.target.value as Rank)}>
                {ranks.map((r) => (
                  <option key={r} value={r}>
                    {r === 'cultivar' && parent.rank === 'genus' ? 'Cultivar (hybrid)' : rankLabels[r]}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          <Field label="Name" hint={chosenRank === 'cultivar' ? "Just the cultivar, e.g. Moonshine (quotes are added for you)." : undefined}>
            <Input required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="Notes" hint="Optional, e.g. the trade name it's sold under.">
            <Input maxLength={500} value={notes} onChange={(e) => setNotes(e.target.value)} />
          </Field>
          <ErrorBanner error={addPlantName.error} />
          <div className="flex gap-2">
            <Button type="submit" disabled={addPlantName.isPending || !name.trim()}>
              Add and use it
            </Button>
            <Button type="button" variant="secondary" onClick={onCancel}>
              Cancel
            </Button>
          </div>
        </form>
      )}
      {!parent && (
        <Button type="button" variant="secondary" onClick={onCancel}>
          Cancel
        </Button>
      )}
    </div>
  )
}
