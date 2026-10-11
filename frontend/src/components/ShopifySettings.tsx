import { useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router'
import {
  useConnectShopify,
  useDisconnectShopify,
  useSetShopifyLocation,
  useShopifyConnection,
  useShopifyLocations,
} from '../api/hooks'
import type { ShopifyConnectionDto } from '../api/types'
import { formatDate } from '../lib/format'
import { Badge, Button, Card, ErrorBanner, Field, Input, Loading, Notice, Select } from './ui'

/** What the API's callback reports in ?shopify= after the Owner comes back from Shopify. */
const RESULTS: Record<string, { ok: boolean; message: string }> = {
  connected: { ok: true, message: 'Your Shopify store is connected.' },
  expired: { ok: false, message: 'That took too long. Click Connect to try again.' },
  'different-store': { ok: false, message: 'You approved Plant Stock on a different store than the one you entered. Try again with the right store.' },
  'not-allowed': { ok: false, message: 'Only Owners can connect a store.' },
  'missing-permissions': { ok: false, message: "The store didn't grant everything Plant Stock needs. Connect again and approve all permissions." },
  'other-nursery': { ok: false, message: 'That store is already connected to another nursery.' },
  failed: { ok: false, message: "Shopify didn't finish connecting. Try again in a minute." },
}

/** Settings → Shopify: connect the nursery's store and choose the pickup location. */
export function ShopifySettings({ orgId }: { orgId: number }) {
  const connection = useShopifyConnection(orgId)
  const [searchParams, setSearchParams] = useSearchParams()
  const result = RESULTS[searchParams.get('shopify') ?? '']

  const dismiss = () =>
    setSearchParams(
      (params) => {
        params.delete('shopify')
        return params
      },
      { replace: true },
    )

  return (
    <Card title="Shopify">
      <div className="space-y-4">
        {result && (
          <div className="flex items-start justify-between gap-2">
            {result.ok ? <Notice>{result.message}</Notice> : <ErrorBanner error={new Error(result.message)} />}
            <Button variant="ghost" onClick={dismiss} aria-label="Dismiss">
              ✕
            </Button>
          </div>
        )}
        {connection.isPending ? (
          <Loading />
        ) : connection.error ? (
          <ErrorBanner error={connection.error} />
        ) : !connection.data.configured ? (
          <p className="text-sm text-stone-600 dark:text-stone-400">Shopify isn't set up on this server yet.</p>
        ) : connection.data.connected ? (
          <Connected orgId={orgId} connection={connection.data} />
        ) : (
          <ConnectForm orgId={orgId} />
        )}
      </div>
    </Card>
  )
}

function ConnectForm({ orgId, shop = '' }: { orgId: number; shop?: string }) {
  const connect = useConnectShopify(orgId)
  const [address, setAddress] = useState(shop)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    connect.mutate(address)
  }

  return (
    <form onSubmit={submit} className="space-y-3">
      {!shop && (
        <>
          <p className="text-sm text-stone-600 dark:text-stone-400">
            Connect your store to sell your plants online. You'll go to Shopify to approve Plant Stock, then come back here.
          </p>
          <Field label="Store address" hint="Find it in Shopify under Settings → Domains, e.g. your-store.myshopify.com">
            <Input required placeholder="your-store.myshopify.com" value={address} onChange={(e) => setAddress(e.target.value)} />
          </Field>
        </>
      )}
      <ErrorBanner error={connect.error} />
      {/* After success the browser leaves for Shopify, so the button stays disabled. */}
      <Button type="submit" disabled={connect.isPending || connect.isSuccess}>
        {connect.isPending || connect.isSuccess ? 'Going to Shopify…' : shop ? 'Connect again' : 'Connect'}
      </Button>
    </form>
  )
}

function Connected({ orgId, connection }: { orgId: number; connection: ShopifyConnectionDto }) {
  const disconnect = useDisconnectShopify(orgId)
  const shop = connection.shop ?? ''

  return (
    <div className="space-y-4">
      <div className="space-y-1">
        <p className="flex flex-wrap items-center gap-2 text-sm">
          <span className="font-medium">{shop}</span>
          {connection.needsReconnect ? <Badge tone="red">Needs reconnecting</Badge> : <Badge tone="green">Connected</Badge>}
        </p>
        {connection.connectedAt && (
          <p className="text-xs text-stone-500">
            Connected {formatDate(connection.connectedAt)}
            {connection.connectedByName && ` by ${connection.connectedByName}`}
          </p>
        )}
      </div>

      {connection.needsReconnect ? (
        <div className="space-y-3">
          <ErrorBanner error={new Error('Shopify no longer accepts Plant Stock’s access (was the app uninstalled?). Connect again to fix it.')} />
          <ConnectForm orgId={orgId} shop={shop} />
        </div>
      ) : (
        <PickupLocation orgId={orgId} connection={connection} />
      )}

      <div className="border-t border-stone-200 pt-4 dark:border-stone-800">
        <ErrorBanner error={disconnect.error} />
        <Button
          variant="danger"
          disabled={disconnect.isPending}
          onClick={() =>
            window.confirm(`Disconnect ${shop}? Plant Stock stops updating it. To remove the app from Shopify too, uninstall it in Shopify → Settings → Apps.`) &&
            disconnect.mutate()
          }
        >
          {disconnect.isPending ? 'Disconnecting…' : 'Disconnect'}
        </Button>
      </div>
    </div>
  )
}

function PickupLocation({ orgId, connection }: { orgId: number; connection: ShopifyConnectionDto }) {
  const locations = useShopifyLocations(orgId, true)
  const setLocation = useSetShopifyLocation(orgId)

  if (locations.isPending) return <Loading label="Loading your store's locations…" />
  if (locations.error) return <ErrorBanner error={locations.error} />
  return (
    <div className="space-y-2">
      <Field label="Pickup location" hint="Where customers collect their plants. Plant Stock sets stock here.">
        <Select
          value={connection.locationId ?? ''}
          disabled={setLocation.isPending}
          onChange={(e) => e.target.value && setLocation.mutate(e.target.value)}
        >
          {!connection.locationId && <option value="">Choose a location…</option>}
          {locations.data.map((location) => (
            <option key={location.id} value={location.id}>
              {location.name}
            </option>
          ))}
        </Select>
      </Field>
      <ErrorBanner error={setLocation.error} />
      {!connection.locationId && <Notice>Choose a location before Plant Stock can put stock on Shopify.</Notice>}
    </div>
  )
}
