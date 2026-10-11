package shopify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// fakeProduct is a product on the fake store.
type fakeProduct struct {
	ID, Title, DescriptionHTML, ProductType, Status string
	Tags                                            []string
	Variants                                        []fakeVariant
}

type fakeVariant struct {
	ID, InventoryItemID, SKU, Price, PotSize string
}

type gqlError struct {
	Field   []string `json:"field"`
	Message string   `json:"message"`
	Code    string   `json:"code,omitempty"`
}

// graphQL answers the queries Plant Stock sends (callers hold f.mu).
func (f *fakeShopify) graphQL(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Query     string          `json:"query"`
		Variables json.RawMessage `json:"variables"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	var data any
	switch {
	case strings.Contains(request.Query, "locations("):
		data = map[string]any{"locations": map[string]any{"nodes": []map[string]string{
			{"id": "gid://shopify/Location/1", "name": "Greenhouse"},
			{"id": "gid://shopify/Location/2", "name": "Market Stall"},
		}}}
	case strings.Contains(request.Query, "productSet("):
		data = map[string]any{"productSet": f.productSet(request.Variables)}
	case strings.Contains(request.Query, "productUpdate("):
		data = map[string]any{"productUpdate": f.productUpdate(request.Variables)}
	case strings.Contains(request.Query, "inventoryActivate("):
		data = map[string]any{"inventoryActivate": f.inventoryActivate(request.Variables)}
	case strings.Contains(request.Query, "inventorySetQuantities("):
		data = map[string]any{"inventorySetQuantities": f.inventorySetQuantities(request.Variables)}
	default:
		http.Error(w, "unexpected query", http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func (f *fakeShopify) id(kind string) string {
	f.nextID++
	return fmt.Sprintf("gid://shopify/%s/%d", kind, f.nextID)
}

func (f *fakeShopify) productSet(raw json.RawMessage) map[string]any {
	var variables struct {
		Identifier *struct{ ID string } `json:"identifier"`
		Input      struct {
			Title           string   `json:"title"`
			DescriptionHTML string   `json:"descriptionHtml"`
			ProductType     string   `json:"productType"`
			Tags            []string `json:"tags"`
			Status          string   `json:"status"`
			ProductOptions  []struct {
				Name   string `json:"name"`
				Values []struct{ Name string }
			} `json:"productOptions"`
			Variants []struct {
				ID           string `json:"id"`
				Price        string `json:"price"`
				OptionValues []struct {
					OptionName string `json:"optionName"`
					Name       string `json:"name"`
				} `json:"optionValues"`
				InventoryItem struct {
					SKU     string `json:"sku"`
					Tracked bool   `json:"tracked"`
				} `json:"inventoryItem"`
			} `json:"variants"`
		} `json:"input"`
	}
	_ = json.Unmarshal(raw, &variables)
	fail := func(code, message string) map[string]any {
		return map[string]any{"product": nil, "userErrors": []gqlError{{Field: []string{"input"}, Message: message, Code: code}}}
	}
	if f.refuse != "" {
		return fail("INVALID_PRODUCT", f.refuse)
	}

	if f.products == nil {
		f.products = map[string]*fakeProduct{}
	}
	product := &fakeProduct{ID: f.id("Product"), Status: "ACTIVE"} // Shopify's default status
	if variables.Identifier != nil {
		existing, ok := f.products[variables.Identifier.ID]
		if !ok {
			return fail("NOT_FOUND", "Product does not exist")
		}
		product = existing
	}
	input := variables.Input
	if len(input.ProductOptions) != 1 || input.ProductOptions[0].Name != potSizeOption {
		return fail("INVALID_INPUT", "want one Pot size option")
	}
	existing := map[string]fakeVariant{}
	for _, v := range product.Variants {
		existing[v.ID] = v
	}
	var variants []fakeVariant
	for _, v := range input.Variants {
		if !v.InventoryItem.Tracked || len(v.OptionValues) != 1 || v.OptionValues[0].OptionName != potSizeOption {
			return fail("INVALID_VARIANT", "want tracked stock and a Pot size value")
		}
		variant := fakeVariant{ID: v.ID, SKU: v.InventoryItem.SKU, Price: v.Price, PotSize: v.OptionValues[0].Name}
		if v.ID == "" {
			variant.ID, variant.InventoryItemID = f.id("ProductVariant"), f.id("InventoryItem")
		} else if old, ok := existing[v.ID]; ok {
			variant.InventoryItemID = old.InventoryItemID
		} else {
			return fail("PRODUCT_VARIANT_DOES_NOT_EXIST", "Product variant does not exist")
		}
		variants = append(variants, variant)
	}

	product.Title, product.DescriptionHTML, product.ProductType, product.Tags = input.Title, input.DescriptionHTML, input.ProductType, input.Tags
	if input.Status != "" {
		product.Status = input.Status
	}
	product.Variants = variants
	f.products[product.ID] = product

	nodes := make([]map[string]any, len(variants))
	for i, v := range variants {
		nodes[i] = map[string]any{"id": v.ID, "sku": v.SKU, "inventoryItem": map[string]string{"id": v.InventoryItemID}}
	}
	return map[string]any{
		"product":    map[string]any{"id": product.ID, "variants": map[string]any{"nodes": nodes}},
		"userErrors": []gqlError{},
	}
}

func (f *fakeShopify) productUpdate(raw json.RawMessage) map[string]any {
	var variables struct {
		Product struct{ ID, Status string } `json:"product"`
	}
	_ = json.Unmarshal(raw, &variables)
	product, ok := f.products[variables.Product.ID]
	if !ok {
		return map[string]any{"product": nil, "userErrors": []gqlError{{Field: []string{"id"}, Message: "Product does not exist"}}}
	}
	product.Status = variables.Product.Status
	return map[string]any{"product": map[string]string{"id": product.ID}, "userErrors": []gqlError{}}
}

// only returns the store's one product (failing the test if there isn't exactly one).
func (s *shopTest) onlyProduct() *fakeProduct {
	s.t.Helper()
	s.fake.mu.Lock()
	defer s.fake.mu.Unlock()
	if len(s.fake.products) != 1 {
		s.t.Fatalf("store has %d products, want 1", len(s.fake.products))
	}
	for _, p := range s.fake.products {
		return p
	}
	return nil
}

func (f *fakeShopify) inventoryItemExists(id string) bool {
	for _, p := range f.products {
		for _, v := range p.Variants {
			if v.InventoryItemID == id {
				return true
			}
		}
	}
	return false
}

func (f *fakeShopify) inventoryActivate(raw json.RawMessage) map[string]any {
	var variables struct{ InventoryItemID, LocationID, IdempotencyKey string }
	_ = json.Unmarshal(raw, &variables)
	if !f.inventoryItemExists(variables.InventoryItemID) || variables.IdempotencyKey == "" {
		return map[string]any{"inventoryLevel": nil, "userErrors": []gqlError{{Message: "Inventory item does not exist"}}}
	}
	if f.stocked == nil {
		f.stocked, f.onHand = map[string]bool{}, map[string]int{}
	}
	f.stocked[variables.InventoryItemID+"@"+variables.LocationID] = true
	f.activations++
	return map[string]any{"inventoryLevel": map[string]string{"id": "gid://shopify/InventoryLevel/1"}, "userErrors": []gqlError{}}
}

func (f *fakeShopify) inventorySetQuantities(raw json.RawMessage) map[string]any {
	var variables struct {
		IdempotencyKey string `json:"idempotencyKey"`
		Input          struct {
			Name, Reason, ReferenceDocumentURI string
			Quantities                         []map[string]any
		} `json:"input"`
	}
	_ = json.Unmarshal(raw, &variables)
	fail := func(code, message string) map[string]any {
		return map[string]any{"inventoryAdjustmentGroup": nil, "userErrors": []gqlError{{Field: []string{"input"}, Message: message, Code: code}}}
	}
	if variables.Input.Name != "on_hand" || variables.Input.Reason == "" || variables.IdempotencyKey == "" {
		return fail("INVALID_NAME", "want on_hand, a reason, and an idempotency key")
	}
	for _, q := range variables.Input.Quantities {
		item, location := q["inventoryItemId"].(string), q["locationId"].(string)
		if from, present := q["changeFromQuantity"]; !present || from != nil {
			return fail("CHANGE_FROM_QUANTITY_STALE", "want changeFromQuantity: null")
		}
		if !f.inventoryItemExists(item) {
			return fail("INVALID_INVENTORY_ITEM", "The specified inventory item could not be found.")
		}
		if !f.stocked[item+"@"+location] {
			return fail("ITEM_NOT_STOCKED_AT_LOCATION", "The specified inventory item is not stocked at the location.")
		}
	}
	for _, q := range variables.Input.Quantities {
		f.onHand[q["inventoryItemId"].(string)+"@"+q["locationId"].(string)] = int(q["quantity"].(float64))
	}
	return map[string]any{"inventoryAdjustmentGroup": map[string]string{"id": "gid://shopify/InventoryAdjustmentGroup/1"}, "userErrors": []gqlError{}}
}
