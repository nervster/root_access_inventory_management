import { useState, type FormEvent } from 'react'
import { useParams } from 'react-router'
import { useMembership, useOrganization, useUpdateOrganization } from '../../api/hooks'
import { can } from '../../api/roles'
import { Permissions } from '../../api/types'
import { NotFound } from '../../components/Guards'
import type { OrganizationDto } from '../../api/types'
import { Button, Card, ErrorBanner, Field, Input, Loading, Notice, PageHeader, Select } from '../../components/ui'
import { TIME_ZONES } from '../../lib/format'

export function SettingsPage() {
  const orgId = Number(useParams().orgId)
  const org = useOrganization(orgId)
  const membership = useMembership(orgId)

  if (!can(membership, Permissions.OrganizationManage)) return <NotFound message="Only Owners can change nursery settings." />
  if (org.isPending) return <Loading />
  if (org.error) return <ErrorBanner error={org.error} />
  return <SettingsForm org={org.data} />
}

function SettingsForm({ org }: { org: OrganizationDto }) {
  const update = useUpdateOrganization(org.id)
  const [name, setName] = useState(org.name)
  const [timeZone, setTimeZone] = useState(org.timeZone)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    update.mutate({ name, timeZone })
  }

  return (
    <div className="space-y-4">
      <PageHeader title="Settings" />
      <Card>
        <form onSubmit={submit} className="space-y-4">
          <Field label="Nursery name">
            <Input required maxLength={200} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="Time zone" hint="Used for dates and reports.">
            <Select value={timeZone} onChange={(e) => setTimeZone(e.target.value)}>
              {TIME_ZONES.map((tz) => (
                <option key={tz}>{tz}</option>
              ))}
            </Select>
          </Field>
          <ErrorBanner error={update.error} />
          {update.isSuccess && <Notice>Saved.</Notice>}
          <Button type="submit" disabled={update.isPending}>
            {update.isPending ? 'Saving…' : 'Save'}
          </Button>
        </form>
      </Card>
    </div>
  )
}
