// Mirrors the JSON of the Go API (backend/internal/organization/*_handler.go).

export type OrgRole = 'owner' | 'admin' | 'staff' | 'viewer'
export type OrganizationStatus = 'active' | 'suspended'

export const ORG_ROLES: OrgRole[] = ['owner', 'admin', 'staff', 'viewer']

/** How roles and statuses are shown; the API sends them lower-case. */
export const ROLE_LABELS: Record<OrgRole, string> = { owner: 'Owner', admin: 'Admin', staff: 'Staff', viewer: 'Viewer' }
export const STATUS_LABELS: Record<OrganizationStatus, string> = { active: 'Active', suspended: 'Suspended' }

/** Permission names from backend/internal/organization/permissions.go. */
export const Permissions = {
  OrganizationView: 'organization.view',
  OrganizationManage: 'organization.manage',
  IntegrationsManage: 'integrations.manage',
  TeamView: 'team.view',
  TeamManage: 'team.manage',
  RolesManage: 'roles.manage',
  InventoryView: 'inventory.view',
  InventoryEdit: 'inventory.edit',
  ListingsManage: 'listings.manage',
  FinancialsView: 'financials.view',
  OrdersPrepare: 'orders.prepare',
  PickupManage: 'pickup.manage',
  RefundsApprove: 'refunds.approve',
} as const
export type Permission = (typeof Permissions)[keyof typeof Permissions]

export interface MembershipDto {
  organizationId: number
  organizationName: string
  slug: string
  role: OrgRole
  status: OrganizationStatus
  permissions: Permission[]
}

export interface PendingInvitationDto {
  id: number
  organizationId: number
  organizationName: string
  role: OrgRole
  expiresAt: string
}

export interface MeResponse {
  id: number
  email: string
  displayName: string | null
  isPlatformAdmin: boolean
  memberships: MembershipDto[]
  pendingInvitations: PendingInvitationDto[]
}

export interface OrganizationDto {
  id: number
  name: string
  slug: string
  timeZone: string
  status: OrganizationStatus
}

export interface MemberDto {
  id: number
  userId: number
  email: string
  displayName: string | null
  role: OrgRole
  joinedAt: string
}

export interface InvitationDto {
  id: number
  email: string
  role: OrgRole
  createdAt: string
  expiresAt: string
  expired: boolean
  /** Who sent it: their name (email if they have none), as it was then. */
  invitedBy: string | null
}

export interface PlatformOrganizationDto {
  id: number
  name: string
  slug: string
  timeZone: string
  status: OrganizationStatus
  memberCount: number
  createdAt: string
}

export interface CreateOrganizationResponse {
  organization: PlatformOrganizationDto
  ownerInvitation: InvitationDto
  deliveryError: string | null
}

export interface PlatformOrganizationDetailDto {
  organization: PlatformOrganizationDto
  members: MemberDto[]
  pendingInvitations: InvitationDto[]
}

export interface PlatformUserDto {
  id: number
  email: string
  displayName: string | null
  isPlatformAdmin: boolean
  createdAt: string
  memberships: { organizationId: number; organizationName: string; role: OrgRole }[]
}

// ── Inventory (backend/internal/inventory) ──────────────────────────────────

export interface PotSizeDto {
  id: number
  name: string
  diameterInches: number | null
  heightInches: number | null
  sortOrder: number
  sellable: boolean
  description: string | null
}

export interface DashboardDto {
  plants: { growing: number; forSale: number; held: number; returned: number; sold: number; discarded: number }
  last30Days: { received: number; discarded: number }
  /** The prices of everything for sale right now. */
  forSaleValueCents: number
  /** Growing, in a sellable pot, priced: could be marked for sale now. */
  readyToMark: number
  /** Growing, in a sellable pot, with no price for its plant and pot size yet. */
  needsPrice: number
  /** Smallest first: the growth pipeline. */
  potSizes: { id: number; name: string; sellable: boolean; growing: number; forSale: number }[]
}

export type PlantStatus = 'growing' | 'for_sale' | 'held' | 'sold' | 'returned' | 'discarded'
export const PLANT_STATUS_LABELS: Record<PlantStatus, string> = {
  growing: 'Growing',
  for_sale: 'For sale',
  held: 'Held for an order',
  sold: 'Sold',
  returned: 'Returned, to check',
  discarded: 'Discarded',
}

export interface PlantNameDto {
  id: number
  rank: 'family' | 'genus' | 'species' | 'cultivar'
  /** Alocasia baginda 'Dragon Scale' */
  fullName: string
  /** E.g. the trade name it's sold under. */
  notes: string | null
  shared: boolean
}

export interface VariegationDto {
  id: number
  name: string
  notes: string | null
  shared: boolean
}

export interface BatchDto {
  id: number
  /** B-0007 */
  code: string
  plantNameId: number
  plantName: string
  variegationId: number | null
  variegation: string | null
  source: string | null
  /** 2026-10-06 */
  receivedOn: string
  /** Only present for roles that see financials. */
  unitCostCents?: number | null
  notes: string | null
  createdAt: string
  /** held: bought online, waiting for pickup. */
  plantCounts: { growing: number; forSale: number; held: number; returned: number; sold: number; discarded: number }
}

export interface PlantDto {
  id: number
  /** RAH-ADF-000123 */
  barcode: string
  batchId: number
  batchCode: string
  plantNameId: number
  plantName: string
  variegationId: number | null
  variegation: string | null
  potSizeId: number
  potSize: string
  status: PlantStatus
  notes: string | null
  createdAt: string
  discardedAt: string | null
  discardReason: string | null
}

export interface BatchWithPlantsDto {
  batch: BatchDto
  plants: PlantDto[]
}

/** A SKU created along the way: plants of this kind went on sale in this pot for the first time. */
export interface NewVariantDto {
  product: string
  potSize: string
  sku: string
}

export interface RepotResult {
  moved: number
  /** Already in that pot. */
  unchanged: number
  /** For-sale plants moved into a pot they weren't sold in before get a SKU there. */
  newVariants: NewVariantDto[]
}

export interface CorrectionResult {
  corrected: number
  /** Already right. */
  unchanged: number
  /** SKUs created for for-sale plants that are now a different product. */
  newVariants: NewVariantDto[]
}

export interface SaleResult {
  marked: number
  alreadyForSale: number
  skipped: { unsellablePot: number }
  /** SKUs created for these plants; they have no price yet. */
  newVariants: NewVariantDto[]
}

export type PlantEventType =
  | 'received'
  | 'repotted'
  | 'marked_for_sale'
  | 'unmarked_for_sale'
  | 'held_for_order'
  | 'released_from_order'
  | 'returned'
  | 'sold'
  | 'discarded'
  | 'name_corrected'
  | 'photo_added'
  | 'photo_deleted'

/** One thing that happened to a plant. Postgres records these whenever a plant or its photos change. */
export interface PlantEventDto {
  type: PlantEventType
  /** Depends on type: batch and pot names, a discard reason, a photo caption… */
  details: {
    batch?: string
    fromPotSize?: string
    toPotSize?: string
    reason?: string
    from?: string
    to?: string
    caption?: string
    /** The online order it was held for or released from, e.g. #1001. */
    order?: string
    /** The return it came back with, e.g. #1001-R1. */
    return?: string
  }
  occurredAt: string
  /** Who: their name (their email if they have none); null for system changes. */
  changedBy: string | null
}

export interface PlantWithHistoryDto extends PlantDto {
  /** Oldest first. */
  history: PlantEventDto[]
}

export interface VariantDto {
  id: number
  potSizeId: number
  potSize: string
  sku: string
  /** null until someone sets it; unpriced SKUs stay off sales channels. */
  priceCents: number | null
  /** Plants for sale right now. */
  stock: number
}

export interface ProductDto {
  id: number
  plantNameId: number
  plantName: string
  variegationId: number | null
  variegation: string | null
  title: string
  description: string | null
  /** E.g. Houseplant. */
  productType: string | null
  /** E.g. Aroid, Rare: for Shopify collections and search. */
  tags: string[]
  createdAt: string
  /** Smallest pot first. */
  variants: VariantDto[]
  /** In order; the first is the main image. */
  images: { photoId: number; viewUrl: string }[]
}

export type ProductEventType = 'price_changed' | 'sku_added' | 'sku_deleted' | 'sku_changed' | 'details_changed'

/** One change to a product or its SKUs. Postgres records these; money is in cents. */
export interface ProductEventDto {
  type: ProductEventType
  /** Depends on type: the SKU code and pot size name (kept as text), old and new values… */
  details: {
    sku?: string
    potSize?: string
    priceCents?: number | null
    /** The old and new price (cents, null = no price), SKU code, or product field value. */
    from?: number | string | string[] | null
    to?: number | string | string[] | null
    field?: 'title' | 'description' | 'productType' | 'tags'
  }
  occurredAt: string
  /** Who: their name (their email if they have none). */
  changedBy: string | null
}

/** A photo that can be one of a product's images: of a plant with its name and variegation. */
export interface PhotoCandidateDto {
  id: number
  viewUrl: string
  takenAt: string
  caption: string | null
  /** The plant in the photo. */
  barcode: string
  /** Already one of the product's images. */
  chosen: boolean
}

export interface PhotoDto {
  id: number
  /** A signed link, valid for an hour. */
  viewUrl: string
  takenAt: string
  caption: string | null
  /** Who added it: their name (email if they have none), as it was then. */
  uploadedBy: string | null
}

/** The nursery's link to its Shopify store (Settings → Shopify; Owners only). */
export interface ShopifyConnectionDto {
  /** The server has Plant Stock's Shopify app credentials. */
  configured: boolean
  connected: boolean
  /** e.g. root-access-htx.myshopify.com */
  shop: string | null
  /** Shopify stopped accepting Plant Stock's access (e.g. the app was uninstalled): connect again. */
  needsReconnect: boolean
  /** Where customers pick up. */
  locationId: string | null
  locationName: string | null
  connectedByName: string | null
  connectedAt: string | null
  /** Orders are read up to here. */
  ordersCheckedAt: string | null
  /** Why reading orders failed last time. */
  ordersError: string | null
  /** Permissions added since the store connected: connect again to grant them. */
  missingScopes: string[]
  /** When the nightly check last ran (it compares the store with Plant Stock and fixes differences). */
  driftCheckedAt: string | null
  /** What it found different, and fixed, in words. */
  driftFound: string[] | null
}

export interface ShopifyLocationDto {
  id: string
  name: string
}

/** How one product stands on the nursery's Shopify store. */
export interface ShopifyProductStatusDto {
  productId: number
  title: string
  /** The product in the Shopify admin, once created. */
  adminUrl: string | null
  syncedAt: string | null
  /** Why Shopify refused it last time. */
  error: string | null
}

export type OrderStatus = 'open' | 'ready_for_pickup' | 'picked_up' | 'cancelled'

/** An online order, and the plants held for it until pickup. */
export interface OrderDto {
  id: number
  /** e.g. #1001, as the customer sees it. */
  name: string
  status: OrderStatus
  placedAt: string
  cancelledAt: string | null
  shopifyFulfillmentStatus: string | null
  readyAt: string | null
  /** Who marked it ready for pickup, by name. */
  readyByName: string | null
  pickedUpAt: string | null
  pickedUpByName: string | null
  /** The order in the Shopify admin. */
  adminUrl: string
  items: OrderItemDto[]
}

export interface OrderItemDto {
  id: number
  title: string
  sku: string | null
  potSize: string | null
  quantity: number
  priceCents: number | null
  /** False for items that aren't Plant Stock's SKUs: nothing is held for them. */
  inPlantStock: boolean
  /** The plants to pull. Fewer than quantity when it was oversold. */
  heldBarcodes: string[]
}

export interface OrdersStatusDto {
  connected: boolean
  checkedAt?: string | null
  error?: string | null
}

/** A customer sending plants back (requested in Shopify). The refund is issued in Shopify, within a day of receiving the plant. */
export interface ReturnDto {
  id: number
  /** e.g. #1001-R1 */
  name: string
  orderName: string
  shopifyStatus: 'REQUESTED' | 'OPEN' | 'CLOSED' | 'DECLINED' | 'CANCELED'
  requestedAt: string
  /** When the first plant came back. */
  receivedAt: string | null
  /** A day after receiving, until refunded. */
  refundDueAt: string | null
  refundOverdue: boolean
  refunded: boolean
  /** The order in the Shopify admin, where the refund is issued. */
  adminUrl: string
  items: ReturnItemDto[]
}

export interface ReturnItemDto {
  id: number
  title: string
  potSize: string | null
  quantity: number
  reason: string | null
  customerNote: string | null
  /** The order's sold plants: which ones came back? */
  soldBarcodes: string[]
  /** Received, waiting for a check. */
  waitingBarcodes: string[]
  receivedCount: number
}

/** A refund issued in Shopify without a return: maybe without the plant back, against the rule. */
export interface UnreturnedRefundDto {
  id: number
  orderName: string
  refundedAt: string
  amountCents: number | null
  /** e.g. "1 × Dragon Scale Albo"; null for a money-only refund. */
  items: string | null
  adminUrl: string
}

/** A refund without a return that an Owner approved as a special case. */
export interface ApprovedRefundDto extends UnreturnedRefundDto {
  approvedAt: string
  approvedByName: string | null
  /** Why it was approved without a return. */
  note: string
}

/** One of the nursery's daily pickup windows (the same every day), in its time zone. */
export interface PickupWindowDto {
  id: number
  /** e.g. "8 AM – 2 PM", or the nursery's own name ("Morning") */
  name: string
  /** Minutes after midnight. */
  startMinute: number
  lengthMinutes: number
}

export interface PickupSettingsDto {
  /** e.g. America/Chicago */
  timeZone: string
  /** A window can be booked only if it starts at least this many hours from now. */
  leadHours: number
  /** Customers can book today and this many days in all. */
  bookingDays: number
  windows: PickupWindowDto[]
}

/** open: can be booked. closed: closed on the calendar. taken: every locker is held or booked. too_soon: starts within the lead time (or has started). too_far: past the booking range. */
export type PickupSlotStatus = 'open' | 'closed' | 'taken' | 'too_soon' | 'too_far'

/** A hold (a shopper's cart, for 30 minutes while they pay) or a booking (an order). */
export interface PickupBookingDto {
  status: 'held' | 'booked'
  /** When a hold runs out. */
  expiresAt: string
}

export interface PickupClosureDto {
  reason: string | null
  /** Who closed it: their name (email if none), as it was then. */
  closedBy: string | null
}

export interface PickupSlotDto {
  windowId: number
  name: string
  start: string
  end: string
  status: PickupSlotStatus
  /** This window's own closure on this date (a whole-day closure is on the day). */
  closure: PickupClosureDto | null
  bookings: PickupBookingDto[]
}

export interface PickupDayDto {
  /** 2026-10-25 */
  date: string
  closure: PickupClosureDto | null
  slots: PickupSlotDto[]
}

export interface PickupCalendarDto extends PickupSettingsDto {
  /** The nursery's date now. */
  today: string
  days: PickupDayDto[]
}
