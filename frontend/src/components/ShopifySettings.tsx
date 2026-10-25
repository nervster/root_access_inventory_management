import { useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router'
import {
  useCheckDrift,
  useConnectShopify,
  useDisconnectShopify,
  useSetShopifyLocation,
  useShopifyConnection,
  useShopifyLocations,
  useShopifyProducts,
  useSyncShopify,
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

      {!connection.needsReconnect && connection.missingScopes.length > 0 && (
        <div className="space-y-3">
          <Notice>
            Plant Stock needs new permissions from your store ({connection.missingScopes.join(', ')}), for example to see return requests. Connect again to approve them.
          </Notice>
          <ConnectForm orgId={orgId} shop={shop} />
        </div>
      )}

      {connection.needsReconnect ? (
        <div className="space-y-3">
          <ErrorBanner error={new Error('Shopify no longer accepts Plant Stock’s access (was the app uninstalled?). Connect again to fix it.')} />
          <ConnectForm orgId={orgId} shop={shop} />
        </div>
      ) : (
        <>
          {connection.ordersError && (
            <ErrorBanner
              error={
                new Error(
                  connection.ordersError.includes('not approved')
                    ? 'Shopify isn’t letting Plant Stock read orders yet. In the Shopify Dev Dashboard, open Plant Stock and turn on access to protected customer data (orders).'
                    : `Couldn’t read orders from Shopify: ${connection.ordersError}`,
                )
              }
            />
          )}
          <PickupLocation orgId={orgId} connection={connection} />
          <ProductsOnShopify orgId={orgId} />
          <NightlyCheck orgId={orgId} connection={connection} />
        </>
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

function ProductsOnShopify({ orgId }: { orgId: number }) {
  const products = useShopifyProducts(orgId, true)
  const sync = useSyncShopify(orgId)
  const problems = products.data?.filter((p) => p.error) ?? []
  const synced = products.data?.filter((p) => !p.error) ?? []

  return (
    <div className="space-y-3 border-t border-stone-200 pt-4 dark:border-stone-800">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <p className="text-sm font-medium">Products on Shopify</p>
          <p className="text-xs text-stone-500">
            Products with a price go to Shopify automatically, with their photos and stock. New ones arrive as drafts for you to check and publish there.
          </p>
        </div>
        <Button variant="secondary" disabled={sync.isPending} onClick={() => sync.mutate()}>
          {sync.isPending ? 'Queuing…' : 'Sync now'}
        </Button>
      </div>
      <ErrorBanner error={sync.error} />
      {sync.isSuccess && <Notice>Syncing in the background. Products appear on Shopify within a minute.</Notice>}

      {products.isPending ? (
        <Loading />
      ) : products.error ? (
        <ErrorBanner error={products.error} />
      ) : products.data.length === 0 ? (
        <p className="text-sm text-stone-500">None yet. Set a price on a product to put it on Shopify.</p>
      ) : (
        <ul className="divide-y divide-stone-200 text-sm dark:divide-stone-800">
          {[...problems, ...synced].map((product) => (
            <li key={product.productId} className="flex flex-wrap items-center justify-between gap-2 py-2">
              <span className="min-w-0">
                <span className="block truncate">{product.title}</span>
                {product.error && <span className="block text-xs text-red-700 dark:text-red-400">Shopify refused it: {product.error}</span>}
              </span>
              {product.error ? (
                <Badge tone="red">Not synced</Badge>
              ) : (
                product.adminUrl && (
                  <a href={product.adminUrl} target="_blank" rel="noreferrer" className="text-emerald-700 hover:underline dark:text-emerald-400">
                    Open in Shopify ↗
                  </a>
                )
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** The nightly check: Plant Stock compares the store with itself and fixes any difference. */
function NightlyCheck({ orgId, connection }: { orgId: number; connection: ShopifyConnectionDto }) {
  const check = useCheckDrift(orgId)
  const found = connection.driftFound ?? []

  return (
    <div className="space-y-2 border-t border-stone-200 pt-4 dark:border-stone-800">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <p className="text-sm font-medium">Nightly check</p>
          <p className="text-xs text-stone-500">
            Each night at 3 am, Plant Stock compares your store with itself (products, SKUs, prices, stock) and fixes anything that differs.
          </p>
        </div>
        <Button variant="secondary" disabled={check.isPending || check.isSuccess} onClick={() => check.mutate()}>
          {check.isPending ? 'Queuing…' : check.isSuccess ? 'Checking…' : 'Check now'}
        </Button>
      </div>
      <ErrorBanner error={check.error} />
      {!connection.driftCheckedAt ? (
        <p className="text-sm text-stone-500">Hasn’t run yet.</p>
      ) : found.length === 0 ? (
        <p className="text-sm text-stone-600 dark:text-stone-400">Last ran {formatDate(connection.driftCheckedAt)}: everything matched.</p>
      ) : (
        <div className="space-y-1 text-sm">
          <p className="text-stone-600 dark:text-stone-400">
            Last ran {formatDate(connection.driftCheckedAt)}: fixed {found.length} difference{found.length === 1 ? '' : 's'}.
          </p>
          <ul className="list-disc space-y-0.5 pl-5 text-stone-600 dark:text-stone-400">
            {found.map((difference) => (
              <li key={difference}>{difference}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
