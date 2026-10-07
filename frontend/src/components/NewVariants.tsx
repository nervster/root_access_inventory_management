import { Link } from 'react-router'
import type { NewVariantDto } from '../api/types'

/** The SKUs an action just created (plants went on sale in a pot for the first time); they need prices. */
export function NewVariants({ orgId, variants }: { orgId: number; variants: NewVariantDto[] }) {
  if (variants.length === 0) return null
  return (
    <div className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-200">
      <p className="font-medium">New SKU{variants.length === 1 ? '' : 's'}, no price yet:</p>
      <ul className="mt-1 list-disc pl-5">
        {variants.map((v) => (
          <li key={v.sku}>
            {v.product} · {v.potSize} · <span className="font-mono text-xs">{v.sku}</span>
          </li>
        ))}
      </ul>
      <Link to={`/orgs/${orgId}/products`} className="mt-1 inline-block font-medium underline">
        Set prices on Products
      </Link>
    </div>
  )
}
