import { useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { usePlatformUsers } from '../../api/hooks'
import { ROLE_LABELS } from '../../api/types'
import { Badge, Button, Card, ErrorBanner, Input, Loading, PageHeader } from '../../components/ui'
import { formatDate } from '../../lib/format'

export function UsersPage() {
  const [draft, setDraft] = useState('')
  const [search, setSearch] = useState('')
  const users = usePlatformUsers(search)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    setSearch(draft.trim())
  }

  return (
    <div className="space-y-4">
      <PageHeader title="Users" subtitle="Find anyone who has signed in, and which nurseries they belong to." />
      <form onSubmit={submit} className="flex gap-2">
        <Input type="search" value={draft} onChange={(e) => setDraft(e.target.value)} placeholder="Search by email" aria-label="Search by email" />
        <Button type="submit">Search</Button>
      </form>
      <Card title={search ? `Results for “${search}”` : 'Newest users'}>
        {users.isPending ? (
          <Loading />
        ) : users.error ? (
          <ErrorBanner error={users.error} />
        ) : users.data.length === 0 ? (
          <p className="text-sm text-stone-500">No users found.</p>
        ) : (
          <ul className="divide-y divide-stone-200 dark:divide-stone-800">
            {users.data.map((user) => (
              <li key={user.id} className="py-3">
                <p className="flex flex-wrap items-center gap-2 font-medium">
                  <span className="truncate">{user.displayName ?? user.email}</span>
                  {user.isPlatformAdmin && <Badge tone="amber">Platform admin</Badge>}
                </p>
                <p className="truncate text-sm text-stone-500">
                  {user.displayName && `${user.email} · `}first signed in {formatDate(user.createdAt)}
                </p>
                {user.memberships.length === 0 ? (
                  <p className="mt-1 text-sm text-stone-500">No nurseries.</p>
                ) : (
                  <ul className="mt-2 flex flex-wrap gap-2">
                    {user.memberships.map((m) => (
                      <li key={m.organizationId}>
                        <Link
                          to={`/platform/orgs/${m.organizationId}`}
                          className="inline-flex items-center gap-1 rounded-full border border-stone-200 px-2 py-0.5 text-xs hover:bg-stone-100 dark:border-stone-700 dark:hover:bg-stone-800"
                        >
                          {m.organizationName} · {ROLE_LABELS[m.role]}
                        </Link>
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  )
}
