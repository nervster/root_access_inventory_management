import { useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { useCreateOrganization, usePlatformOrganizations } from '../../api/hooks'
import type { CreateOrganizationResponse } from '../../api/types'
import { Badge, Button, Card, ErrorBanner, Field, Input, Loading, Notice, PageHeader, Select } from '../../components/ui'
import { TIME_ZONES, formatDate } from '../../lib/format'

export function OrganizationsPage() {
  const orgs = usePlatformOrganizations()

  return (
    <div className="space-y-4">
      <PageHeader title="Nurseries" subtitle="Create nurseries and manage their accounts." />
      <Card title="Create a nursery">
        <CreateOrganizationForm />
      </Card>
      <Card title="All nurseries">
        {orgs.isPending ? (
          <Loading />
        ) : orgs.error ? (
          <ErrorBanner error={orgs.error} />
        ) : orgs.data.length === 0 ? (
          <p className="text-sm text-stone-500">No nurseries yet.</p>
        ) : (
          <ul className="divide-y divide-stone-200 dark:divide-stone-800">
            {orgs.data.map((org) => (
              <li key={org.id}>
                <Link
                  to={`/platform/orgs/${org.id}`}
                  className="-mx-2 flex items-center justify-between gap-3 rounded-lg px-2 py-3 hover:bg-stone-50 dark:hover:bg-stone-800/50"
                >
                  <div className="min-w-0">
                    <p className="truncate font-medium">{org.name}</p>
                    <p className="truncate text-sm text-stone-500">
                      {org.slug} · created {formatDate(org.createdAt)}
                    </p>
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    {org.status === 'suspended' && <Badge tone="red">Suspended</Badge>}
                    <Badge>
                      {org.memberCount} member{org.memberCount === 1 ? '' : 's'}
                    </Badge>
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  )
}

function CreateOrganizationForm() {
  const create = useCreateOrganization()
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [timeZone, setTimeZone] = useState('America/Chicago')
  const [ownerEmail, setOwnerEmail] = useState('')
  const [created, setCreated] = useState<CreateOrganizationResponse | null>(null)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    setCreated(null)
    create.mutate(
      { name, slug: slug || undefined, timeZone, ownerEmail },
      {
        onSuccess: (result) => {
          setCreated(result)
          setName('')
          setSlug('')
          setOwnerEmail('')
        },
      },
    )
  }

  return (
    <form onSubmit={submit} className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Nursery name">
          <Input required maxLength={200} value={name} onChange={(e) => setName(e.target.value)} placeholder="Root Access HTX" />
        </Field>
        <Field label="Slug" hint="Optional. Lowercase letters, numbers, hyphens. Generated from the name if blank.">
          <Input value={slug} pattern="[a-z0-9]+(-[a-z0-9]+)*" maxLength={100} onChange={(e) => setSlug(e.target.value)} placeholder="root-access-htx" />
        </Field>
        <Field label="Owner's email" hint="They'll be invited as the nursery's first Owner.">
          <Input type="email" required value={ownerEmail} onChange={(e) => setOwnerEmail(e.target.value)} />
        </Field>
        <Field label="Time zone">
          <Select value={timeZone} onChange={(e) => setTimeZone(e.target.value)}>
            {TIME_ZONES.map((tz) => (
              <option key={tz}>{tz}</option>
            ))}
          </Select>
        </Field>
      </div>
      <ErrorBanner error={create.error} />
      {created && (
        <Notice>
          Created{' '}
          <Link to={`/platform/orgs/${created.organization.id}`} className="font-medium underline">
            {created.organization.name}
          </Link>{' '}
          and invited {created.ownerInvitation.email} as Owner.
          {created.deliveryError && <> The invite email failed: {created.deliveryError}</>}
        </Notice>
      )}
      <Button type="submit" disabled={create.isPending}>
        {create.isPending ? 'Creating…' : 'Create nursery'}
      </Button>
    </form>
  )
}
