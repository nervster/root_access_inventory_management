# Picking plants: the storefront contract

How a nursery's Shopify theme (Root Access: `root_access_shopify_theme`) shows shoppers the actual plants in
stock for a SKU, one photo each, and lets them choose which ones they buy. Plant Stock owns the plants, their
photos and the holds; the theme only shows them and puts the chosen plants in the cart. Shoppers never see
barcodes or IDs: they choose pictures.

Like pickup slots (`pickup-storefront.md`), everything goes through the Plant Stock app's **App Proxy**:
`/apps/plant-stock/…` on the store's own domain, signed by Shopify, with no keys and no CORS. Requests carry no
cookies and no identity.

| Endpoint | Status |
|---|---|
| `GET plants` | Built; on staging once merged |
| `POST plant-holds`, `…/refresh`, `…/release` | Built; on staging once merged |
| Chosen plants held when the order is imported | Built; on staging once merged |

## How it looks

- The product page is still one product per plant (name + variegation), with one variant per pot size (the
  SKU). Its gallery is the product's own images: the **grown form**, chosen in Plant Stock.
- When the shopper picks a pot size, the page shows **one photo per plant** in stock for that SKU: the
  plant's latest photo. They tap one or more, then add them to the cart.
- Each chosen plant is its own cart line, quantity 1.

Only plants that are for sale **and have a photo** are offered, and only those count toward the stock Plant
Stock sends to Shopify, so a plant without a photo can't be bought.

## Plants, holds, and the cart

- A plant is identified to the theme by its **plant ID**, Plant Stock's number for it (e.g. `1041`), never
  its barcode. It never changes.
- A **hold** keeps a plant for one cart for **30 minutes**, so another shopper can't choose it while the first
  pays. Plants held by other carts are left out of `GET plants`. Holds that never become an order expire.
- One live hold per plant (a database rule), so two shoppers choosing the same plant at the same moment
  can't both win.
- A visitor (by the client IP Shopify forwards) can hold at most **20** plants at once. Past that, holding
  fails with `too_many_holds`.
- Holds belong to a cart, by the **cart token** (`token` from `fetch('/cart.js')`), sent as `cartToken`.

As with pickup slots, Plant Stock can't stop Shopify's checkout itself: if a hold runs out while a shopper sits
on the payment page and someone else orders that plant, the second order is flagged for staff. Refreshing the
holds at Checkout makes this rare.

## The cart line

Add each held plant with the Ajax Cart API, quantity 1, with a hidden line item property `_plant`:

```js
fetch('/cart/add.js', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ items: [
    { id: 4567890123, quantity: 1, properties: { _plant: '1041' } },
    { id: 4567890123, quantity: 1, properties: { _plant: '1042' } }
  ]})
});
```

- Properties starting with `_` are hidden from the shopper by Shopify's cart and checkout (the theme must not
  print them either). Different `_plant` values keep the lines separate.
- **Don't let the shopper change the quantity** of a `_plant` line (show it as 1, with a remove button). If an
  order still arrives with a quantity above 1, Plant Stock holds the chosen plant and flags the extra for staff.
- Removing a line: release that plant's hold (`POST plant-holds/release`).

## Errors

The same shape and codes as pickup slots:

```json
{ "error": "taken", "message": "Someone else just chose that plant. Please pick another.", "plants": [1041] }
```

| `error` | HTTP | Meaning | What the theme does |
|---|---|---|---|
| `taken` | 409 | One or more plants are held by another cart, ordered, or no longer for sale; `plants` lists them | Reload plants; remove those from the selection (or cart lines) |
| `not_found` | 404 | No such variant on this store | Hide the picker |
| `too_many_holds` | 429 | This visitor already holds 20 plants | Show the message; don't retry automatically |
| `invalid` | 400 | Missing or malformed fields | A bug in the theme: log it |
| (none) | 401 | Not signed by Shopify | Treat as unavailable |
| (none) | 5xx, network error | Plant Stock unavailable | "Plants are unavailable right now", disable Add to cart |

## `GET /apps/plant-stock/plants?variant=<variant ID>&cartToken=…`

The plants a shopper can choose for one SKU. `variant` is the Shopify variant's numeric ID (Liquid
`variant.id`). Load it when the shopper picks a pot size, again after a `409`, and when the page has been open
more than 30 minutes (the photo links expire after an hour).

```json
{
  "variant": 4567890123,
  "plants": [
    { "id": 1041, "photo": "https://storage.googleapis.com/…", "held": false },
    { "id": 1042, "photo": "https://storage.googleapis.com/…", "held": true }
  ]
}
```

- `photo` is a signed link to the plant's latest photo, valid for an hour; use it as an `<img src>`.
- Plants held by other carts are left out. With `cartToken`, plants this cart holds stay listed with
  `held: true`, so the shopper's choices still show.
- Oldest plants first. An empty `plants` means none can be chosen: show the size as sold out.

## `POST /apps/plant-stock/plant-holds`

Hold the chosen plants for the cart. Call it when the shopper adds plants to the cart, then add the lines.

```json
{ "cartToken": "c1-7a2…", "plants": [1041, 1042] }
```

`201 Created`:
```json
{ "plants": [1041, 1042], "expiresAt": "2026-10-25T16:31:00-05:00" }
```

- All or nothing: if any plant can't be held, none are, and it fails with `taken` listing which. Reload the
  plants and let the shopper choose again.
- Plants the cart already holds are just refreshed. Holding doesn't release the cart's other plants.

## `POST /apps/plant-stock/plant-holds/refresh`

Keep every plant in the cart for another 30 minutes. Call it when the shopper clicks **Checkout** (with the
pickup hold's refresh), and go to checkout only if both succeed.

```json
{ "cartToken": "c1-7a2…", "plants": [1041, 1042] }
```

`200 OK`: the same body as holding. Holds that ran out are taken again if the plant is still free. On `taken`:
stay on the cart, remove the listed lines, and tell the shopper which plants were taken (by their photos).

## `POST /apps/plant-stock/plant-holds/release`

Give plants back. Call it when lines are removed or the cart is emptied.

```json
{ "cartToken": "c1-7a2…", "plants": [1041] }
```

`204 No Content`, also when the holds had already gone.

## When the order is imported

Plant Stock reads each line's `_plant`:
- The chosen plant is held for the order (its cart hold becomes the order's), whether or not the hold had
  run out, as long as it's still for sale.
- A chosen plant that's no longer for sale (another order got it, or it was sold in person or discarded) is
  **not swapped** for another: the Orders page names the plant the customer chose, for staff to contact them
  (offer a similar plant, or refund).
- Only for-sale plants **with a photo** count as stock on Shopify, so a SKU whose plants have no photos shows as
  sold out.
- Lines without `_plant` (carts from before the picker) hold the oldest plants of the SKU, as before.

## The theme's flow

```
Pot size picked ── GET plants?variant=… ──▶ one photo per plant
Shopper taps plants, Add to cart ── POST plant-holds ──▶ 201: add one line per plant with _plant
                                                       409: reload plants, choose again
Line removed / cart emptied ── POST plant-holds/release
Checkout clicked ── POST plant-holds/refresh (+ pickup-holds/refresh) ──▶ 200: go to checkout
                                                                        409: remove taken lines, stay
Order placed ── (Plant Stock imports it) ──▶ the chosen plants are held for the order
```
