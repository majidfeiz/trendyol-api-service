package models

import "time"

// ─── API Response Wrappers ────────────────────────────────────────────────────

type ProductResponse struct {
	Success bool     `json:"success"`
	Data    *Product `json:"data,omitempty"`
	Error   string   `json:"error,omitempty"`
}

type ProductResult struct {
	ProductID int64    `json:"productId"`
	Product   *Product `json:"product,omitempty"`
	Error     string   `json:"error,omitempty"`
}

type BatchResponse struct {
	Success bool                     `json:"success"`
	Total   int                      `json:"total"`
	Results map[int64]*ProductResult `json:"results"`
}

// ─── Our Normalized Models ────────────────────────────────────────────────────

type Product struct {
	ProductID int64     `json:"productId"`
	ContentID int64     `json:"contentId"`
	Name      string    `json:"name"`
	Brand     string    `json:"brand"`
	URL       string    `json:"url"`
	Price     Price     `json:"price"`
	Variants  []Variant `json:"variants"`
	InStock   bool      `json:"inStock"`
	Images    []string  `json:"images"`
	FetchedAt time.Time `json:"fetchedAt"`
}

type Price struct {
	Original   float64 `json:"original"`
	Discounted float64 `json:"discounted"`
	Currency   string  `json:"currency"`
}

type Variant struct {
	AttributeName  string  `json:"attributeName"`
	AttributeValue string  `json:"attributeValue"`
	InStock        bool    `json:"inStock"`
	Quantity       int     `json:"quantity"`
	Price          float64 `json:"price"`
	DiscountedPrice float64 `json:"discountedPrice"`
	Barcode        string  `json:"barcode,omitempty"`
	ItemNumber     int64   `json:"itemNumber,omitempty"`
}

// ─── Trendyol Raw API Response ────────────────────────────────────────────────

type TrendyolProductResponse struct {
	Result    TrendyolProduct `json:"result"`
	IsSuccess bool            `json:"isSuccess"`
}

type TrendyolProduct struct {
	ProductID   int64             `json:"productId"`
	ContentID   int64             `json:"contentId"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Brand       TrendyolBrand     `json:"brand"`
	Price       TrendyolPrice     `json:"price"`
	AllVariants []TrendyolVariant `json:"allVariants"`
	Images      []TrendyolImage   `json:"images"`
	MerchantID  int64             `json:"merchantId"`
	CategoryID  int64             `json:"categoryId"`
}

type TrendyolBrand struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type TrendyolPrice struct {
	SellingPrice        float64 `json:"sellingPrice"`
	OriginalPrice       float64 `json:"originalPrice"`
	DiscountedPrice     float64 `json:"discountedPrice"`
	PriceWithoutDiscount float64 `json:"priceWithoutDiscount"`
	Currency            string  `json:"currency"`
}

type TrendyolVariant struct {
	AttributeName   string  `json:"attributeName"`
	AttributeValue  string  `json:"attributeValue"`
	Price           float64 `json:"price"`
	DiscountedPrice float64 `json:"discountedPrice"`
	InStock         bool    `json:"inStock"`
	Quantity        int     `json:"quantity"`
	Stock           int     `json:"stock"`
	ItemNumber      int64   `json:"itemNumber"`
	Barcode         string  `json:"barcode"`
}

type TrendyolImage struct {
	URL         string `json:"url"`
	CreatedDate int64  `json:"createdDate"`
}

// ─── JSON-LD (schema.org) ─────────────────────────────────────────────────────

type JSONLDProduct struct {
	Context string      `json:"@context"`
	Type    string      `json:"@type"`
	Name    string      `json:"name"`
	Brand   JSONLDBrand `json:"brand"`
	Offers  JSONLDOffer `json:"offers"`
}

type JSONLDBrand struct {
	Type string `json:"@type"`
	Name string `json:"name"`
}

type JSONLDOffer struct {
	Type          string `json:"@type"`
	Price         string `json:"price"`
	PriceCurrency string `json:"priceCurrency"`
	Availability  string `json:"availability"`
}
