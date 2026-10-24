# Pickup slots: the storefront contract

How a nursery's Shopify theme (Root Access: `root_access_shopify_theme`) gets pickup slots from Plant Stock and
books one. Plant Stock owns the slot rules and bookings (see `ROADMAP.md`, Decisions: Pickup slot rules,
Booking from the cart); the theme only shows them and passes the shopper's choice to the order.

Everything goes through the Plant Stock app's **App Proxy**: `/apps/plant-stock/…` on the store's own domain,
forwarded by Shopify, with a signature, to Plant Stock's `/api/shopify/proxy/…`
(`backend/internal/shopify/proxy.go`). The theme calls relative URLs, with no keys and no CORS. Requests carry no
cookies and no identity: Plant Stock knows which store a request came through, not who the shopper is.

| Endpoint | Status |
|---|---|
| `GET pickup-slots` | Live on staging |
| `POST pickup-holds`, `…/refresh`, `…/release` | Built (with `?cartToken` on `GET pickup-slots`); on staging once merged |
| Hold → booking when the order is imported | Next |

## Slots, holds, and bookings

- A **slot** is one locker × one date × one window. The theme never sees lockers: Plant Stock picks a free
  locker when it holds a slot. With one locker, a date and window has one slot.
- A **hold** keeps a slot for one cart for **30 minutes**, so another shopper can't take it while the first
  pays. Holds that never become an order expire, and the slot is free again.
- A **booking** is a hold that became an order: when Plant Stock imports the order, it finds the hold from
  the order's `Pickup hold` attribute and turns it into a booking.
- The database allows at most one live hold or booking per slot, so two shoppers asking at the same moment
  can't both win.
- Plant Stock can't stop Shopify's checkout itself (on our plan). If a hold runs out while a shopper sits on
  the payment page and someone else takes the slot, both orders go through; Plant Stock flags the clash
  for staff to sort out with the customer. Refreshing the hold at Checkout makes this rare.

## Identifying the cart

Holds belong to a cart, identified by the **cart token**: `token` from the Ajax Cart API
(`fetch('/cart.js')`). Send it as `cartToken` with every hold request; treat it as an opaque string. One cart
holds at most **one** slot (one pickup per order): holding a new slot releases the cart's previous hold.

To stop one visitor holding many slots across carts, Plant Stock allows at most **3** live holds per visitor
(by the client IP Shopify forwards). Past that, holding fails with `too_many_holds`.

## Cart attributes the theme saves

| Attribute | Value | Example |
|---|---|---|
| `Pickup date` | the slot's `date` | `2026-10-24` |
| `Pickup window` | the slot's `window` (the nursery's name for it) | `Morning` |
| `Pickup hold` | the hold's `holdId` | `ph_8f3k2…` |
| `Pickup note` | optional free text from the shopper | `Gate code 1234` |

Save them with `POST /cart/update.js` after the hold succeeds. When the hold is released or replaced, update
all three pickup attributes together. Orders over 4 plants don't use the locker: no slots, no hold, no
attributes (in-person pickup instead).

## Errors

Every failure is JSON with a code for the theme to act on and a sentence it can show:

```json
{ "error": "taken", "message": "That pickup time was just taken. Please choose another." }
```

| `error` | HTTP | Meaning | What the theme does |
|---|---|---|---|
| `taken` | 409 | Someone else holds or booked the slot | Reload slots, ask the shopper to pick again |
| `too_soon` | 409 | The window now starts less than the lead time (8 hours) away | Same |
| `closed` | 409 | The nursery closed the day or window | Same |
| `too_far` | 409 | Past the booking range | Same |
| `not_found` | 404 | No such window, or no such hold for this cart | Reload slots; drop the saved hold |
| `too_many_holds` | 429 | This visitor already holds 3 slots | Show the message; don't retry automatically |
| `invalid` | 400 | Missing or malformed fields | A bug in the theme: log it |
| (none) | 401 | Not signed by Shopify (shouldn't happen through the proxy) | Treat as unavailable |
| (none) | 5xx, network error | Plant Stock unavailable | "Pickup times are unavailable", disable Checkout |

## `GET /apps/plant-stock/pickup-slots`

Every slot that can be booked now, for each date from today through the booking range (30 days). Load it
once when the cart opens, and again after any `409`, or if the page has been open more than 5 minutes.

```json
{
  "timeZone": "America/Chicago",
  "leadHours": 8,
  "bookingDays": 30,
  "days": [
    { "date": "2026-10-24", "slots": [
      { "windowId": 12, "window": "Morning", "start": "2026-10-24T08:00:00-05:00", "end": "2026-10-24T16:00:00-05:00" }
    ]},
    { "date": "2026-10-25", "slots": [] }
  ]
}
```

- Every date in the range is listed, in order. `slots` holds only what can be booked: closed windows, ones
  starting within the lead time, ones past the range, and (once holds exist) held or booked ones are left
  out. An empty `slots` means the day is full or closed.
- Send `?cartToken=…` (once holds exist): slots held by other carts are left out, but the cart's own held
  slot stays listed, so the shopper's choice still shows.

## `POST /apps/plant-stock/pickup-holds`

Hold a slot for the cart. Call it when the shopper picks a window.

```json
{ "cartToken": "c1-7a2…", "date": "2026-10-24", "windowId": 12 }
```

`201 Created`:
```json
{
  "holdId": "ph_8f3k2…",
  "date": "2026-10-24",
  "windowId": 12,
  "window": "Morning",
  "start": "2026-10-24T08:00:00-05:00",
  "end": "2026-10-24T16:00:00-05:00",
  "expiresAt": "2026-10-23T16:31:00-05:00"
}
```

- The cart's previous hold, if any, is released in the same step.
- Holding the slot the cart already holds just refreshes it (same `holdId`, new `expiresAt`).
- Then save `Pickup date`, `Pickup window` and `Pickup hold` on the cart.
- On `409`: reload slots and ask the shopper to pick again; keep the previous choice cleared.

## `POST /apps/plant-stock/pickup-holds/refresh`

Keep the hold for another 30 minutes. Call it when the shopper clicks **Checkout**, before leaving the cart,
and go to checkout only if it succeeds.

```json
{ "cartToken": "c1-7a2…", "holdId": "ph_8f3k2…" }
```

`200 OK`: the same body as holding, with the new `expiresAt`.

- Checks everything again: the window must still start at least the lead time (8 hours) from now, and still
  be open. A window picked 8 hours 10 minutes ahead and refreshed 30 minutes later fails with `too_soon`.
- A hold that has run out is taken again if the slot is still free (`200`), or fails with `taken`.
- On any `409`: stay on the cart, show the message, reload slots.

## `POST /apps/plant-stock/pickup-holds/release`

Give the slot back. Call it when the cart is emptied, or switches to in-person pickup (over 4 plants).
(Picking a different window doesn't need it: holding the new slot releases the old one.)

```json
{ "cartToken": "c1-7a2…", "holdId": "ph_8f3k2…" }
```

`204 No Content`, also when the hold had already gone. Then clear the three pickup attributes.

## The theme's flow

```
Cart opens ── GET pickup-slots ──▶ dates and windows
Shopper picks a window ── POST pickup-holds ──▶ 201: save Pickup date / window / hold
                                              409: reload slots, pick again
Checkout clicked ── POST pickup-holds/refresh ──▶ 200: go to checkout
                                                 409: stay, reload slots, pick again
Cart emptied / over 4 plants ── POST pickup-holds/release ──▶ clear the attributes
Order placed ── (Plant Stock imports it) ──▶ the hold becomes a booking
```

If any call can't reach Plant Stock, show "Pickup times are unavailable right now. Please try again
shortly." and disable Checkout.
