import { useState, type FormEvent } from 'react'
import { useParams } from 'react-router'
import { useMembership, useProductActions, useProducts } from '../../api/hooks'
import { can } from '../../api/roles'
import { Permissions, type ProductDto, type VariantDto } from '../../api/types'
import { Badge, Button, Card, ErrorBanner, Field, Input, Loading, Notice, PageHeader } from '../../components/ui'
import { dollarsToCents, formatMoney } from '../../lib/format'

/**
 * What the nursery sells. Products and their SKUs (one per pot size) appear when plants are marked
 * for sale; here people set prices, SKUs, and the details customers see.
 */
export function ProductsPage() {
  const orgId = Number(useParams().orgId)
  const membership = useMembership(orgId)
  const products = useProducts(orgId)
  const [onlyUnpriced, setOnlyUnpriced] = useState(false)
  const canSell = can(membership, Permissions.ListingsManage)

  if (products.isPending) return <Loading />
  if (products.error) return <ErrorBanner error={products.error} />

  const unpriced = products.data.flatMap((p) => p.variants).filter((v) => v.priceCents === null).length
  const shown = onlyUnpriced ? products.data.filter((p) => p.variants.some((v) => v.priceCents === null)) : products.data

  return (
    <div className="space-y-4">
      <PageHeader title="Products" subtitle="What you're selling: one product per plant and variegation, a SKU and price per pot size." />

      {unpriced > 0 && (
        <Notice>
          {unpriced} SKU{unpriced === 1 ? ' has' : 's have'} no price yet.{' '}
          <button type="button" className="font-medium underline" onClick={() => setOnlyUnpriced(!onlyUnpriced)}>
            {onlyUnpriced ? 'Show all products' : 'Show only those'}
          </button>
        </Notice>
      )}

      {products.data.length === 0 && (
        <Card>
          <p className="text-sm text-stone-600 dark:text-stone-400">
            Nothing for sale yet. Mark plants for sale from a batch or Scan: each kind of plant gets a product here, with a SKU for each pot size.
          </p>
        </Card>
      )}
      {shown.map((product) => (
        <ProductCard key={product.id} orgId={orgId} product={product} canSell={canSell} />
      ))}
    </div>
  )
}

function ProductCard({ orgId, product, canSell }: { orgId: number; product: ProductDto; canSell: boolean }) {
  const actions = useProductActions(orgId)
  const [editingPot, setEditingPot] = useState<number | null>(null)
  const [editingDetails, setEditingDetails] = useState(false)
  const stock = product.variants.reduce((sum, v) => sum + v.stock, 0)

  return (
    <Card>
      {editingDetails ? (
        <ProductDetailsForm
          product={product}
          pending={actions.update.isPending}
          error={actions.update.error}
          onSave={(details) => actions.update.mutate({ id: product.id, ...details }, { onSuccess: () => setEditingDetails(false) })}
          onCancel={() => setEditingDetails(false)}
        />
      ) : (
        <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="font-semibold">{product.title}</h2>
            <p className="text-sm text-stone-500">
              {product.productType && <>{product.productType} · </>}
              {stock} for sale
            </p>
            {product.tags.length > 0 && (
              <p className="mt-1 flex flex-wrap gap-1">
                {product.tags.map((tag) => (
                  <Badge key={tag}>{tag}</Badge>
                ))}
              </p>
            )}
            {product.description ? (
              <p className="mt-1 text-sm">{product.description}</p>
            ) : (
              canSell && <p className="mt-1 text-sm text-stone-500 italic">No description yet</p>
            )}
          </div>
          {canSell && (
            <Button variant="ghost" onClick={() => setEditingDetails(true)}>
              Edit details
            </Button>
          )}
        </div>
      )}

      <ErrorBanner error={actions.removePrice.error} />
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-stone-500">
            <th className="py-1 font-normal">Pot</th>
            <th className="py-1 font-normal">SKU</th>
            <th className="py-1 text-right font-normal">Price</th>
            <th className="py-1 text-right font-normal">Stock</th>
            {canSell && <th className="w-0" />}
          </tr>
        </thead>
        <tbody>
          {product.variants.map((variant) =>
            editingPot === variant.potSizeId ? (
              <tr key={variant.id}>
                <td colSpan={canSell ? 5 : 4} className="py-2">
                  <p className="mb-1 font-medium">{variant.potSize}</p>
                  <VariantForm
                    variant={variant}
                    pending={actions.updateVariant.isPending}
                    error={actions.updateVariant.error}
                    onSave={(sku, priceCents) =>
                      actions.updateVariant.mutate(
                        { productId: product.id, potSizeId: variant.potSizeId, sku, priceCents },
                        { onSuccess: () => setEditingPot(null) },
                      )
                    }
                    onCancel={() => setEditingPot(null)}
                    onRemove={
                      variant.stock === 0
                        ? () => actions.removePrice.mutate({ productId: product.id, potSizeId: variant.potSizeId }, { onSuccess: () => setEditingPot(null) })
                        : undefined
                    }
                  />
                </td>
              </tr>
            ) : (
              <tr key={variant.id} className="border-t border-stone-100 dark:border-stone-800">
                <td className="py-2">{variant.potSize}</td>
                <td className="py-2 font-mono text-xs">{variant.sku}</td>
                <td className="py-2 text-right tabular-nums">{variant.priceCents === null ? <Badge tone="amber">No price</Badge> : formatMoney(variant.priceCents)}</td>
                <td className="py-2 text-right tabular-nums">{variant.stock}</td>
                {canSell && (
                  <td className="py-1 pl-2 text-right">
                    <Button variant="ghost" onClick={() => setEditingPot(variant.potSizeId)}>
                      {variant.priceCents === null ? 'Set price' : 'Edit'}
                    </Button>
                  </td>
                )}
              </tr>
            ),
          )}
        </tbody>
      </table>
    </Card>
  )
}

/** A SKU's code and price (typed in dollars, sent in cents). */
function VariantForm({
  variant,
  pending,
  error,
  onSave,
  onCancel,
  onRemove,
}: {
  variant: VariantDto
  pending: boolean
  error: unknown
  onSave: (sku: string, priceCents: number) => void
  onCancel: () => void
  /** Offered only when nothing is for sale in this pot size. */
  onRemove?: () => void
}) {
  const [sku, setSku] = useState(variant.sku)
  const [price, setPrice] = useState(variant.priceCents === null ? '' : (variant.priceCents / 100).toFixed(2))
  const cents = dollarsToCents(price)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    onSave(sku.trim(), cents)
  }

  return (
    <form onSubmit={submit} className="space-y-2">
      <div className="grid grid-cols-2 gap-2">
        <Field label="Price ($)">
          <Input autoFocus required type="number" inputMode="decimal" min={0} step="0.01" value={price} onChange={(e) => setPrice(e.target.value)} />
        </Field>
        <Field label="SKU">
          <Input required maxLength={64} value={sku} onChange={(e) => setSku(e.target.value)} />
        </Field>
      </div>
      <ErrorBanner error={error} />
      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={pending || !sku.trim() || price === '' || Number.isNaN(cents)}>
          {pending ? 'Saving…' : 'Save'}
        </Button>
        <Button type="button" variant="secondary" onClick={onCancel}>
          Cancel
        </Button>
        {onRemove && (
          <Button type="button" variant="danger" onClick={onRemove}>
            Remove this pot size
          </Button>
        )}
      </div>
    </form>
  )
}

function ProductDetailsForm({
  product,
  pending,
  error,
  onSave,
  onCancel,
}: {
  product: ProductDto
  pending: boolean
  error: unknown
  onSave: (details: { title: string; description: string; productType: string; tags: string[] }) => void
  onCancel: () => void
}) {
  const [title, setTitle] = useState(product.title)
  const [description, setDescription] = useState(product.description ?? '')
  const [productType, setProductType] = useState(product.productType ?? '')
  const [tags, setTags] = useState(product.tags.join(', '))
  return (
    <form
      className="mb-3 space-y-2"
      onSubmit={(event) => {
        event.preventDefault()
        onSave({ title, description, productType, tags: tags.split(',').map((t) => t.trim()).filter(Boolean) })
      }}
    >
      <Field label="Title" hint="What customers see.">
        <Input required maxLength={200} value={title} onChange={(e) => setTitle(e.target.value)} />
      </Field>
      <Field label="Description">
        <textarea
          rows={4}
          maxLength={5000}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className="block w-full rounded-lg border border-stone-300 bg-white px-3 py-2 text-sm focus:border-emerald-600 focus:ring-2 focus:ring-emerald-600/30 focus:outline-none dark:border-stone-700 dark:bg-stone-950"
        />
      </Field>
      <div className="grid gap-2 sm:grid-cols-2">
        <Field label="Product type" hint="E.g. Houseplant.">
          <Input maxLength={255} value={productType} onChange={(e) => setProductType(e.target.value)} />
        </Field>
        <Field label="Tags" hint="Separate with commas, e.g. Aroid, Rare.">
          <Input value={tags} onChange={(e) => setTags(e.target.value)} />
        </Field>
      </div>
      <ErrorBanner error={error} />
      <div className="flex gap-2">
        <Button type="submit" disabled={pending}>
          Save
        </Button>
        <Button type="button" variant="secondary" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  )
}
