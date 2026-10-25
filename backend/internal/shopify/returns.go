package shopify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// Returns come in with orders: whenever a picked-up order changes (a return requested, approved,
// refunded…), its returns and refunds are read again. Plant Stock only reads them; approving,
// declining, and refunding happen in Shopify. A refund not tied to a return is flagged.

const returnsQuery = `query OrderReturns($id: ID!) {
  order(id: $id) {
    returns(first: 10) {
      nodes {
        id name status createdAt
        returnLineItems(first: 50) {
          nodes {
            ... on ReturnLineItem {
              id quantity refundedQuantity customerNote
              returnReasonDefinition { name }
              fulfillmentLineItem { lineItem { id } }
            }
          }
        }
      }
    }
    refunds(first: 20) {
      id createdAt
      return { id }
      totalRefundedSet { shopMoney { amount } }
      refundLineItems(first: 50) { nodes { quantity lineItem { title } } }
    }
  }
}`

type shopifyRefund struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Return    *struct {
		ID string `json:"id"`
	} `json:"return"`
	TotalRefundedSet struct {
		ShopMoney struct {
			Amount string `json:"amount"`
		} `json:"shopMoney"`
	} `json:"totalRefundedSet"`
	RefundLineItems struct {
		Nodes []struct {
			Quantity int `json:"quantity"`
			LineItem struct {
				Title string `json:"title"`
			} `json:"lineItem"`
		} `json:"nodes"`
	} `json:"refundLineItems"`
}

type shopifyReturn struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	ReturnLineItems struct {
		Nodes []struct {
			ID                     string  `json:"id"`
			Quantity               int     `json:"quantity"`
			RefundedQuantity       int     `json:"refundedQuantity"`
			CustomerNote           *string `json:"customerNote"`
			ReturnReasonDefinition *struct {
				Name string `json:"name"`
			} `json:"returnReasonDefinition"`
			FulfillmentLineItem *struct {
				LineItem struct {
					ID string `json:"id"`
				} `json:"lineItem"`
			} `json:"fulfillmentLineItem"`
		} `json:"nodes"`
	} `json:"returnLineItems"`
}

// canReadReturns reports whether the store granted Plant Stock its returns scope (stores connected
// before returns existed must connect again; Settings asks them to).
func canReadReturns(scopes string) bool {
	granted := strings.Split(scopes, ",")
	return slices.Contains(granted, "read_returns") || slices.Contains(granted, "write_returns")
}

// importReturns reads one picked-up order's returns and refunds into Plant Stock.
func (s *Store) importReturns(ctx context.Context, orgID int64, shop, token, shopifyOrderID string) error {
	var data struct {
		Order *struct {
			Returns struct {
				Nodes []shopifyReturn `json:"nodes"`
			} `json:"returns"`
			Refunds []shopifyRefund `json:"refunds"`
		} `json:"order"`
	}
	if err := s.app.GraphQL(ctx, shop, token, returnsQuery, map[string]any{"id": shopifyOrderID}, &data); err != nil {
		return err
	}
	if data.Order == nil {
		return nil
	}
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		order, err := q.GetOrderByShopifyID(ctx, dbgen.GetOrderByShopifyIDParams{OrganizationID: orgID, ShopifyOrderID: shopifyOrderID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // an order Plant Stock never had
		} else if err != nil {
			return err
		}
		for _, r := range data.Order.Returns.Nodes {
			returnID, err := q.UpsertReturn(ctx, dbgen.UpsertReturnParams{
				OrganizationID: orgID, OrderID: order.ID, ShopifyReturnID: r.ID, Name: r.Name,
				ShopifyStatus: r.Status, RequestedAt: r.CreatedAt,
			})
			if err != nil {
				return err
			}
			for _, item := range r.ReturnLineItems.Nodes {
				var orderItemID *int64
				if item.FulfillmentLineItem != nil {
					id, err := q.OrderItemByShopifyLineItem(ctx, dbgen.OrderItemByShopifyLineItemParams{
						OrganizationID: orgID, OrderID: order.ID, ShopifyLineItemID: item.FulfillmentLineItem.LineItem.ID,
					})
					if err == nil {
						orderItemID = &id
					} else if !errors.Is(err, pgx.ErrNoRows) {
						return err
					}
				}
				var reason *string
				if item.ReturnReasonDefinition != nil {
					reason = &item.ReturnReasonDefinition.Name
				}
				err := q.UpsertReturnItem(ctx, dbgen.UpsertReturnItemParams{
					OrganizationID: orgID, ReturnID: returnID, ShopifyReturnLineItemID: item.ID, OrderItemID: orderItemID,
					Quantity: int32(item.Quantity), RefundedQuantity: int32(item.RefundedQuantity), Reason: reason, CustomerNote: item.CustomerNote,
				})
				if err != nil {
					return err
				}
			}
			if err := q.SetReturnRefunded(ctx, dbgen.SetReturnRefundedParams{OrganizationID: orgID, ID: returnID}); err != nil {
				return err
			}
		}
		for _, refund := range data.Order.Refunds {
			if err := saveRefund(ctx, q, orgID, order.ID, refund); err != nil {
				return err
			}
		}
		return nil
	})
}

func saveRefund(ctx context.Context, q *dbgen.Queries, orgID, orderID int64, refund shopifyRefund) error {
	var returnID, items *string
	if refund.Return != nil {
		returnID = &refund.Return.ID
	}
	var lines []string
	for _, line := range refund.RefundLineItems.Nodes {
		lines = append(lines, fmt.Sprintf("%d × %s", line.Quantity, line.LineItem.Title))
	}
	if len(lines) > 0 {
		joined := strings.Join(lines, ", ")
		items = &joined
	}
	if returnID == nil {
		slog.Warn("refund without a return", "organization", orgID, "refund", refund.ID, "items", lines)
	}
	return q.UpsertRefund(ctx, dbgen.UpsertRefundParams{
		OrganizationID: orgID, OrderID: orderID, ShopifyRefundID: refund.ID, ShopifyReturnID: returnID,
		RefundedAt: refund.CreatedAt, AmountCents: priceCents(refund.TotalRefundedSet.ShopMoney.Amount), Items: items,
	})
}
