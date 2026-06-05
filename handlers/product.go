package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"trendyol-api-service/models"
	"trendyol-api-service/service"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *service.TrendyolService
}

func New(svc *service.TrendyolService) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "trendyol-api-service",
	})
}

// GetProduct godoc
// GET /api/v1/product/:id?url=https://www.trendyol.com/...  (url is optional)
// Returns stock + price for a single Trendyol product.
func (h *Handler) GetProduct(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ProductResponse{
			Success: false,
			Error:   "invalid product id: must be a positive integer",
		})
		return
	}

	productURL := c.Query("url") // optional — skips search step in HTML fallback

	product, err := h.svc.GetProduct(c.Request.Context(), id, productURL)
	if err != nil {
		status := http.StatusBadGateway
		if isNotFound(err) {
			status = http.StatusNotFound
		}
		c.JSON(status, models.ProductResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.ProductResponse{
		Success: true,
		Data:    product,
	})
}

// GetProductBatch godoc
// POST /api/v1/products
// Body: {"ids": [123, 456, 789]}
// Returns stock + price for multiple products concurrently.
func (h *Handler) GetProductBatch(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "body must be {\"ids\": [id1, id2, ...]}",
		})
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "ids list cannot be empty",
		})
		return
	}

	results := h.svc.GetProducts(c.Request.Context(), req.IDs)

	c.JSON(http.StatusOK, models.BatchResponse{
		Success: true,
		Total:   len(results),
		Results: results,
	})
}

// GetProductsQuery godoc
// GET /api/v1/products?ids=123,456,789
// Convenience GET version for batch lookup.
func (h *Handler) GetProductsQuery(c *gin.Context) {
	raw := c.Query("ids")
	if raw == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "query param 'ids' is required (comma-separated list)",
		})
		return
	}

	ids, err := parseIDList(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	results := h.svc.GetProducts(c.Request.Context(), ids)

	c.JSON(http.StatusOK, models.BatchResponse{
		Success: true,
		Total:   len(results),
		Results: results,
	})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, strconv.ErrSyntax
	}
	return id, nil
}

func parseIDList(raw string) ([]int64, error) {
	var ids []int64
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == ',' {
			token := raw[start:i]
			start = i + 1
			if token == "" {
				continue
			}
			id, err := parseID(token)
			if err != nil {
				return nil, fmt.Errorf("invalid id %q in list", token)
			}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, strconv.ErrSyntax
	}
	return ids, nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for i := 0; i+8 <= len(msg); i++ {
		if msg[i:i+8] == "not foun" {
			return true
		}
	}
	return false
}
