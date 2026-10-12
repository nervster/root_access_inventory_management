import { useMutation, useQuery, useQueryClient, type QueryKey } from '@tanstack/react-query'
import { useCallback } from 'react'
import { useApi } from './client'
import type {
  CreateOrganizationResponse,
  DashboardDto,
  InvitationDto,
  MeResponse,
  MemberDto,
  MembershipDto,
  OrganizationDto,
  OrganizationStatus,
  OrgRole,
  PlatformOrganizationDetailDto,
  PlatformOrganizationDto,
  PlatformUserDto,
  PotSizeDto,
  BatchDto,
  BatchWithPlantsDto,
  PhotoCandidateDto,
  PhotoDto,
  PlantNameDto,
  PlantWithHistoryDto,
  ProductDto,
  CorrectionResult,
  RepotResult,
  SaleResult,
  ShopifyConnectionDto,
  ShopifyLocationDto,
  ShopifyProductStatusDto,
  OrderDto,
  OrderStatus,
  OrdersStatusDto,
  ReturnDto,
  UnreturnedRefundDto,
  ApprovedRefundDto,
  VariegationDto,
} from './types'

export const queryKeys = {
  me: ['me'] as const,
  organization: (orgId: number) => ['orgs', orgId] as const,
  members: (orgId: number) => ['orgs', orgId, 'members'] as const,
  invitations: (orgId: number) => ['orgs', orgId, 'invitations'] as const,
  platformOrganizations: ['platform', 'orgs'] as const,
  platformOrganization: (id: number) => ['platform', 'orgs', id] as const,
  platformUsers: (email: string) => ['platform', 'users', email] as const,
  shopify: (orgId: number) => ['orgs', orgId, 'shopify'] as const,
  shopifyLocations: (orgId: number) => ['orgs', orgId, 'shopify', 'locations'] as const,
  shopifyProducts: (orgId: number) => ['orgs', orgId, 'shopify', 'products'] as const,
  orders: (orgId: number, status: OrderStatus) => ['orgs', orgId, 'orders', status] as const,
  ordersStatus: (orgId: number) => ['orgs', orgId, 'orders', 'status'] as const,
  returns: (orgId: number, show: 'active' | 'done') => ['orgs', orgId, 'orders', 'returns', show] as const,
  unreturnedRefunds: (orgId: number) => ['orgs', orgId, 'orders', 'returns', 'unreturned-refunds'] as const,
  approvedRefunds: (orgId: number) => ['orgs', orgId, 'orders', 'returns', 'approved-refunds'] as const,
}

// ── Signed-in user ──────────────────────────────────────────────────────────

export function useMe() {
  const api = useApi()
  return useQuery({ queryKey: queryKeys.me, queryFn: () => api<MeResponse>('/me') })
}

/** The signed-in user's membership in an organization, if any. */
export function useMembership(orgId: number): MembershipDto | undefined {
  return useMe().data?.memberships.find((m) => m.organizationId === orgId)
}

export function useRespondToInvitation() {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, accept }: { id: number; accept: boolean }) =>
      api<MembershipDto | undefined>(`/me/invitations/${id}/${accept ? 'accept' : 'decline'}`, { method: 'POST' }),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.me }),
  })
}

// ── Organization (tenant) ───────────────────────────────────────────────────

export function useOrganization(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: queryKeys.organization(orgId), queryFn: () => api<OrganizationDto>(`/orgs/${orgId}`) })
}

export function useUpdateOrganization(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { name?: string; timeZone?: string }) =>
      api<OrganizationDto>(`/orgs/${orgId}`, { method: 'PATCH', json: body }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: queryKeys.organization(orgId) })
      client.invalidateQueries({ queryKey: queryKeys.me })
    },
  })
}

export function useMembers(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: queryKeys.members(orgId), queryFn: () => api<MemberDto[]>(`/orgs/${orgId}/members`) })
}

export function useInvitations(orgId: number, enabled: boolean) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.invitations(orgId),
    queryFn: () => api<InvitationDto[]>(`/orgs/${orgId}/invitations`),
    enabled,
  })
}

/**
 * Team actions for an organization. `base` is the API path that owns the team:
 * `/orgs/{id}` for members acting in their nursery, `/platform/organizations/{id}` for platform admins.
 */
export function useTeamActions(base: string, invalidate: readonly QueryKey[]) {
  const api = useApi()
  const client = useQueryClient()
  const onSuccess = () => Promise.all(invalidate.map((queryKey) => client.invalidateQueries({ queryKey })))

  return {
    invite: useMutation({
      mutationFn: (body: { email: string; role: OrgRole }) =>
        api<InvitationDto>(`${base}/invitations`, { method: 'POST', json: body }),
      onSettled: onSuccess, // a 502 still saved the invite
    }),
    resend: useMutation({
      mutationFn: (id: number) => api<InvitationDto>(`${base}/invitations/${id}/resend`, { method: 'POST' }),
      onSuccess,
    }),
    revoke: useMutation({
      mutationFn: (id: number) => api<void>(`${base}/invitations/${id}`, { method: 'DELETE' }),
      onSuccess,
    }),
    changeRole: useMutation({
      mutationFn: ({ id, role }: { id: number; role: OrgRole }) =>
        api<MemberDto>(`${base}/members/${id}`, { method: 'PATCH', json: { role } }),
      onSuccess,
    }),
    remove: useMutation({
      mutationFn: (id: number) => api<void>(`${base}/members/${id}`, { method: 'DELETE' }),
      onSuccess,
    }),
  }
}

// ── Platform admin ──────────────────────────────────────────────────────────

export function usePlatformOrganizations() {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.platformOrganizations,
    queryFn: () => api<PlatformOrganizationDto[]>('/platform/organizations'),
  })
}

export function useCreateOrganization() {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { name: string; slug?: string; timeZone?: string; ownerEmail: string }) =>
      api<CreateOrganizationResponse>('/platform/organizations', { method: 'POST', json: body }),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.platformOrganizations }),
  })
}

export function usePlatformOrganization(id: number) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.platformOrganization(id),
    queryFn: () => api<PlatformOrganizationDetailDto>(`/platform/organizations/${id}`),
  })
}

export function useUpdatePlatformOrganization(id: number) {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { name?: string; status?: OrganizationStatus }) =>
      api<PlatformOrganizationDto>(`/platform/organizations/${id}`, { method: 'PATCH', json: body }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['platform', 'orgs'] }),
  })
}

export function usePlatformUsers(email: string) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.platformUsers(email),
    queryFn: () => api<PlatformUserDto[]>(`/platform/users?email=${encodeURIComponent(email)}`),
  })
}

// ── Inventory ───────────────────────────────────────────────────────────────

export const inventoryKeys = {
  /** Everything inventory-related in a nursery: invalidate this after any plant changes. */
  all: (orgId: number) => ['orgs', orgId, 'inventory'] as const,
  dashboard: (orgId: number) => ['orgs', orgId, 'inventory', 'dashboard'] as const,
  potSizes: (orgId: number) => ['orgs', orgId, 'inventory', 'pot-sizes'] as const,
  batches: (orgId: number) => ['orgs', orgId, 'inventory', 'batches'] as const,
  batch: (orgId: number, batchId: number) => ['orgs', orgId, 'inventory', 'batches', batchId] as const,
  plantNames: (orgId: number, search: string) => ['orgs', orgId, 'plant-names', search] as const,
  variegations: (orgId: number) => ['orgs', orgId, 'variegations'] as const,
  plant: (orgId: number, barcode: string) => ['orgs', orgId, 'inventory', 'plants', barcode] as const,
  products: (orgId: number) => ['orgs', orgId, 'inventory', 'products'] as const,
  plantPhotos: (orgId: number, barcode: string) => ['orgs', orgId, 'inventory', 'plants', barcode, 'photos'] as const,
}

export function useDashboard(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: inventoryKeys.dashboard(orgId), queryFn: () => api<DashboardDto>(`/orgs/${orgId}/dashboard`) })
}

export function usePotSizes(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: inventoryKeys.potSizes(orgId), queryFn: () => api<PotSizeDto[]>(`/orgs/${orgId}/pot-sizes`) })
}

export interface PotSizeInput {
  name?: string
  diameterInches?: number
  heightInches?: number
  sellable?: boolean
  description?: string
}

export function usePotSizeActions(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  const onSuccess = () =>
    Promise.all([
      client.invalidateQueries({ queryKey: inventoryKeys.potSizes(orgId) }),
      client.invalidateQueries({ queryKey: inventoryKeys.dashboard(orgId) }),
    ])
  return {
    create: useMutation({
      mutationFn: (body: PotSizeInput) => api<PotSizeDto>(`/orgs/${orgId}/pot-sizes`, { method: 'POST', json: body }),
      onSuccess,
    }),
    update: useMutation({
      mutationFn: ({ id, ...body }: PotSizeInput & { id: number }) =>
        api<PotSizeDto>(`/orgs/${orgId}/pot-sizes/${id}`, { method: 'PATCH', json: body }),
      onSuccess,
    }),
    reorder: useMutation({
      mutationFn: (ids: number[]) => api<PotSizeDto[]>(`/orgs/${orgId}/pot-sizes/order`, { method: 'PUT', json: { ids } }),
      onSuccess,
    }),
    remove: useMutation({
      mutationFn: (id: number) => api<void>(`/orgs/${orgId}/pot-sizes/${id}`, { method: 'DELETE' }),
      onSuccess,
    }),
  }
}

export function useBatches(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: inventoryKeys.batches(orgId), queryFn: () => api<BatchDto[]>(`/orgs/${orgId}/batches`) })
}

export function useBatch(orgId: number, batchId: number) {
  const api = useApi()
  return useQuery({
    queryKey: inventoryKeys.batch(orgId, batchId),
    queryFn: () => api<BatchWithPlantsDto>(`/orgs/${orgId}/batches/${batchId}`),
  })
}

/**
 * Plant names containing search (2+ characters), from the shared list and the nursery's own. Families
 * are left out unless includeFamilies (for choosing where a new genus goes).
 */
export function usePlantNames(orgId: number, search: string, includeFamilies = false) {
  const api = useApi()
  const term = search.trim()
  return useQuery({
    queryKey: [...inventoryKeys.plantNames(orgId, term), includeFamilies],
    queryFn: () =>
      api<PlantNameDto[]>(`/orgs/${orgId}/plant-names?search=${encodeURIComponent(term)}${includeFamilies ? '&includeFamilies=true' : ''}`),
    enabled: term.length >= 2,
    staleTime: 5 * 60_000,
  })
}

export function useVariegations(orgId: number) {
  const api = useApi()
  return useQuery({
    queryKey: inventoryKeys.variegations(orgId),
    queryFn: () => api<VariegationDto[]>(`/orgs/${orgId}/variegations`),
    staleTime: 5 * 60_000,
  })
}

export interface ReceiveBatchInput {
  plantNameId: number
  variegationId?: number
  potSizeId: number
  quantity: number
  receivedOn: string
  source?: string
  notes?: string
  unitCostCents?: number
}

/** Receiving batches, repotting, and marking for sale: each refreshes everything inventory shows. */
export function usePlantActions(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  const onSuccess = () => client.invalidateQueries({ queryKey: inventoryKeys.all(orgId) })
  return {
    receiveBatch: useMutation({
      mutationFn: (body: ReceiveBatchInput) => api<BatchWithPlantsDto>(`/orgs/${orgId}/batches`, { method: 'POST', json: body }),
      onSuccess,
    }),
    repot: useMutation({
      mutationFn: (body: { potSizeId: number; batchId?: number; barcodes?: string[] }) =>
        api<RepotResult>(`/orgs/${orgId}/repot`, { method: 'POST', json: body }),
      onSuccess,
    }),
    markForSale: useMutation({
      mutationFn: (body: { batchId?: number; barcodes?: string[] }) =>
        api<SaleResult>(`/orgs/${orgId}/plants/mark-for-sale`, { method: 'POST', json: body }),
      onSuccess,
    }),
    unmarkForSale: useMutation({
      mutationFn: (barcodes: string[]) =>
        api<{ unmarked: number }>(`/orgs/${orgId}/plants/unmark-for-sale`, { method: 'POST', json: { barcodes } }),
      onSuccess,
    }),
    discard: useMutation({
      mutationFn: (body: { barcodes: string[]; reason?: string }) =>
        api<{ discarded: number }>(`/orgs/${orgId}/plants/discard`, { method: 'POST', json: body }),
      onSuccess,
    }),
    /** Fix name and/or variegation. Leave a field out to keep it; variegationId null means none. */
    correct: useMutation({
      mutationFn: (body: { batchId?: number; barcodes?: string[]; plantNameId?: number; variegationId?: number | null }) =>
        api<CorrectionResult>(`/orgs/${orgId}/plants/correct`, { method: 'POST', json: body }),
      onSuccess,
    }),
    setNotes: useMutation({
      mutationFn: ({ barcode, notes }: { barcode: string; notes: string }) =>
        api<PlantWithHistoryDto>(`/orgs/${orgId}/plants/by-barcode/${encodeURIComponent(barcode)}`, { method: 'PATCH', json: { notes } }),
      onSuccess,
    }),
    /** Fix a batch's details. Leave a field out to keep it; "" clears text; unitCostCents null clears it. */
    updateBatch: useMutation({
      mutationFn: ({ batchId, ...body }: { batchId: number; source?: string; notes?: string; receivedOn?: string; unitCostCents?: number | null }) =>
        api<BatchWithPlantsDto>(`/orgs/${orgId}/batches/${batchId}`, { method: 'PATCH', json: body }),
      onSuccess,
    }),
  }
}

/** Looks up a plant by its barcode (for the plant page). */
export function usePlant(orgId: number, barcode: string) {
  const api = useApi()
  return useQuery({
    queryKey: inventoryKeys.plant(orgId, barcode),
    queryFn: () => api<PlantWithHistoryDto>(`/orgs/${orgId}/plants/by-barcode/${encodeURIComponent(barcode)}`),
  })
}

/** Returns a function that looks up a plant by barcode, using the cache when possible (for scanning). */
export function useLookupPlant(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  return useCallback(
    (barcode: string) =>
      client.fetchQuery({
        queryKey: inventoryKeys.plant(orgId, barcode),
        queryFn: () => api<PlantWithHistoryDto>(`/orgs/${orgId}/plants/by-barcode/${encodeURIComponent(barcode)}`),
      }),
    [api, client, orgId],
  )
}

export function useProducts(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: inventoryKeys.products(orgId), queryFn: () => api<ProductDto[]>(`/orgs/${orgId}/products`) })
}

/** Product details and prices. Products and SKUs themselves are created by marking plants for sale. */
export function useProductActions(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  const onSuccess = () => client.invalidateQueries({ queryKey: inventoryKeys.all(orgId) })
  return {
    update: useMutation({
      mutationFn: ({ id, ...body }: { id: number; title?: string; description?: string; productType?: string; tags?: string[] }) =>
        api<ProductDto>(`/orgs/${orgId}/products/${id}`, { method: 'PATCH', json: body }),
      onSuccess,
    }),
    updateVariant: useMutation({
      mutationFn: ({ productId, potSizeId, ...body }: { productId: number; potSizeId: number; sku?: string; priceCents?: number }) =>
        api<ProductDto>(`/orgs/${orgId}/products/${productId}/variants/${potSizeId}`, { method: 'PATCH', json: body }),
      onSuccess,
    }),
    removePrice: useMutation({
      mutationFn: ({ productId, potSizeId }: { productId: number; potSizeId: number }) =>
        api<void>(`/orgs/${orgId}/products/${productId}/variants/${potSizeId}`, { method: 'DELETE' }),
      onSuccess,
    }),
    /** Replaces the product's images with photoIds, in order. */
    setImages: useMutation({
      mutationFn: ({ productId, photoIds }: { productId: number; photoIds: number[] }) =>
        api<ProductDto>(`/orgs/${orgId}/products/${productId}/photos`, { method: 'PUT', json: { photoIds } }),
      onSuccess,
    }),
  }
}

export function usePlantPhotos(orgId: number, barcode: string) {
  const api = useApi()
  return useQuery({
    queryKey: inventoryKeys.plantPhotos(orgId, barcode),
    queryFn: () => api<PhotoDto[]>(`/orgs/${orgId}/plants/by-barcode/${encodeURIComponent(barcode)}/photos`),
    staleTime: 30 * 60_000, // view links last an hour
  })
}

/**
 * Adding and deleting a plant's photos. Adding takes three steps so the file goes straight from the
 * phone to storage: get an upload link from the API, PUT the file there, then record it.
 */
export function usePlantPhotoActions(orgId: number, barcode: string) {
  const api = useApi()
  const client = useQueryClient()
  const path = `/orgs/${orgId}/plants/by-barcode/${encodeURIComponent(barcode)}/photos`
  const onSuccess = () => client.invalidateQueries({ queryKey: inventoryKeys.plantPhotos(orgId, barcode) })
  return {
    add: useMutation({
      mutationFn: async ({ photo, caption, takenAt }: { photo: Blob; caption?: string; takenAt?: string }) => {
        const link = await api<{ uploadUrl: string; contentType: string; key: string }>(`${path}/upload-link`, {
          method: 'POST',
          json: { contentType: 'image/jpeg' },
        })
        // Straight to storage: no API session token here, the link itself grants the upload.
        const upload = await fetch(link.uploadUrl, { method: 'PUT', headers: { 'Content-Type': link.contentType }, body: photo })
        if (!upload.ok) throw new Error(`The photo didn't upload (${upload.status}). Try again.`)
        return api<PhotoDto>(path, { method: 'POST', json: { key: link.key, caption, takenAt } })
      },
      onSuccess,
    }),
    remove: useMutation({
      mutationFn: (photoId: number) => api<void>(`/orgs/${orgId}/photos/${photoId}`, { method: 'DELETE' }),
      onSuccess,
    }),
  }
}

/** Photos of a product's plants that can be its images (only fetched while choosing). */
export function useProductPhotoCandidates(orgId: number, productId: number, enabled: boolean) {
  const api = useApi()
  return useQuery({
    queryKey: [...inventoryKeys.products(orgId), productId, 'photo-candidates'],
    queryFn: () => api<PhotoCandidateDto[]>(`/orgs/${orgId}/products/${productId}/photo-candidates`),
    enabled,
  })
}

/** Adding the nursery's own plant names and variegations to the shared lists. */
export function useNameActions(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  return {
    addPlantName: useMutation({
      mutationFn: (body: { parentId?: number; rank: PlantNameDto['rank']; name: string; notes?: string }) =>
        api<PlantNameDto>(`/orgs/${orgId}/plant-names`, { method: 'POST', json: body }),
      onSuccess: () => client.invalidateQueries({ queryKey: ['orgs', orgId, 'plant-names'] }),
    }),
    addVariegation: useMutation({
      mutationFn: (body: { name: string; notes?: string }) => api<VariegationDto>(`/orgs/${orgId}/variegations`, { method: 'POST', json: body }),
      onSuccess: () => client.invalidateQueries({ queryKey: inventoryKeys.variegations(orgId) }),
    }),
  }
}

// ── Shopify (Owners) ────────────────────────────────────────────────────────

export function useShopifyConnection(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: queryKeys.shopify(orgId), queryFn: () => api<ShopifyConnectionDto>(`/orgs/${orgId}/shopify`) })
}

/** Starts connecting a store: on success, the browser goes to Shopify to approve Plant Stock. */
export function useConnectShopify(orgId: number) {
  const api = useApi()
  return useMutation({
    mutationFn: (shop: string) => api<{ authorizeUrl: string }>(`/orgs/${orgId}/shopify/connect`, { method: 'POST', json: { shop } }),
    onSuccess: ({ authorizeUrl }) => window.location.assign(authorizeUrl),
  })
}

export function useDisconnectShopify(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => api<void>(`/orgs/${orgId}/shopify`, { method: 'DELETE' }),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.shopify(orgId) }),
  })
}

/** The store's locations (asks Shopify, so only when connected). */
export function useShopifyLocations(orgId: number, enabled: boolean) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.shopifyLocations(orgId),
    queryFn: () => api<ShopifyLocationDto[]>(`/orgs/${orgId}/shopify/locations`),
    enabled,
  })
}

export function useSetShopifyLocation(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (locationId: string) => api<ShopifyConnectionDto>(`/orgs/${orgId}/shopify/location`, { method: 'PUT', json: { locationId } }),
    onSuccess: (connection) => client.setQueryData(queryKeys.shopify(orgId), connection),
  })
}

/** Products sent to the store (or refused by it), problems first. Refreshes while the page is open,
 *  since the worker pushes products in the background. */
export function useShopifyProducts(orgId: number, enabled: boolean) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.shopifyProducts(orgId),
    queryFn: () => api<ShopifyProductStatusDto[]>(`/orgs/${orgId}/shopify/products`),
    enabled,
    refetchInterval: 10_000,
  })
}

/** Pushes every product to the store again (the worker does it in the background). */
export function useSyncShopify(orgId: number) {
  const api = useApi()
  return useMutation({ mutationFn: () => api<void>(`/orgs/${orgId}/shopify/sync`, { method: 'POST' }) })
}

// ── Orders ──────────────────────────────────────────────────────────────────

/** The nursery's online orders with status. Refreshes each minute: the worker imports orders in the background. */
export function useOrders(orgId: number, status: OrderStatus) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.orders(orgId, status),
    queryFn: () => api<OrderDto[]>(`/orgs/${orgId}/orders?status=${status}`),
    refetchInterval: 60_000,
  })
}

/** Whether orders are arriving from Shopify, and why not. */
export function useOrdersStatus(orgId: number) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.ordersStatus(orgId),
    queryFn: () => api<OrdersStatusDto>(`/orgs/${orgId}/shopify/orders/status`),
    refetchInterval: 60_000,
  })
}

/** Moving an order along: ready for pickup (in the locker), then picked up. */
export function useOrderActions(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  const refresh = () => client.invalidateQueries({ queryKey: ['orgs', orgId, 'orders'] })
  return {
    ready: useMutation({
      mutationFn: (orderId: number) => api<void>(`/orgs/${orgId}/orders/${orderId}/ready`, { method: 'POST' }),
      onSuccess: refresh,
    }),
    notReady: useMutation({
      mutationFn: (orderId: number) => api<void>(`/orgs/${orgId}/orders/${orderId}/not-ready`, { method: 'POST' }),
      onSuccess: refresh,
    }),
    pickedUp: useMutation({
      mutationFn: (orderId: number) => api<void>(`/orgs/${orgId}/orders/${orderId}/picked-up`, { method: 'POST' }),
      onSuccess: refresh,
    }),
  }
}

/** Reads new orders from Shopify now (instead of within the minute); refreshes the list shortly after. */
export function useCheckOrders(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => api<void>(`/orgs/${orgId}/shopify/orders/import`, { method: 'POST' }),
    onSuccess: () => {
      setTimeout(() => client.invalidateQueries({ queryKey: ['orgs', orgId, 'orders'] }), 4000)
    },
  })
}

// ── Returns ─────────────────────────────────────────────────────────────────

/** Return requests still needing something (active), or finished ones. */
export function useReturns(orgId: number, show: 'active' | 'done') {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.returns(orgId, show),
    queryFn: () => api<ReturnDto[]>(`/orgs/${orgId}/returns?show=${show}`),
    refetchInterval: 60_000,
  })
}

/** Receiving a returned plant, and settling it after its check. */
export function useReturnActions(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  const refresh = () => client.invalidateQueries({ queryKey: ['orgs', orgId, 'orders', 'returns'] })
  return {
    receive: useMutation({
      mutationFn: ({ returnId, barcode }: { returnId: number; barcode: string }) =>
        api<void>(`/orgs/${orgId}/returns/${returnId}/receive`, { method: 'POST', json: { barcode } }),
      onSuccess: refresh,
    }),
    check: useMutation({
      mutationFn: ({ barcode, to, reason }: { barcode: string; to: 'for_sale' | 'growing' | 'discarded'; reason?: string }) =>
        api<void>(`/orgs/${orgId}/returned-plants/${barcode}/check`, { method: 'POST', json: { to, reason } }),
      onSuccess: refresh,
    }),
  }
}

/** Refunds issued without a return, not yet reviewed. */
export function useUnreturnedRefunds(orgId: number) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.unreturnedRefunds(orgId),
    queryFn: () => api<UnreturnedRefundDto[]>(`/orgs/${orgId}/refunds/unreturned`),
    refetchInterval: 60_000,
  })
}

/** Refunds without a return approved as special cases, newest first. */
export function useApprovedRefunds(orgId: number, enabled: boolean) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.approvedRefunds(orgId),
    queryFn: () => api<ApprovedRefundDto[]>(`/orgs/${orgId}/refunds/approved`),
    enabled,
  })
}

/** An Owner approves a refund without a return as a special case, saying why. */
export function useApproveRefund(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ refundId, note }: { refundId: number; note: string }) =>
      api<void>(`/orgs/${orgId}/refunds/${refundId}/approve`, { method: 'POST', json: { note } }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: queryKeys.unreturnedRefunds(orgId) })
      client.invalidateQueries({ queryKey: queryKeys.approvedRefunds(orgId) })
    },
  })
}

/** Compares the store with Plant Stock now (the worker does it; it also runs nightly at 3 am). */
export function useCheckDrift(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => api<void>(`/orgs/${orgId}/shopify/check`, { method: 'POST' }),
    onSuccess: () => setTimeout(() => client.invalidateQueries({ queryKey: queryKeys.shopify(orgId) }), 5000),
  })
}
