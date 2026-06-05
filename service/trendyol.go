package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"trendyol-api-service/config"
	"trendyol-api-service/models"
)

const (
	productDetailPath = "/discovery-web-productgw-service/api/productDetail/%d"
	trendyolWebBase   = "https://www.trendyol.com"
	cdnBase           = "https://cdn.dsmcdn.com"
)

// Per-strategy time budgets (must sum to less than TotalTimeout).
const (
	apiStrategyBudget     = 10 * time.Second
	htmlStrategyBudget    = 12 * time.Second
	// browser gets whatever is left from TotalTimeout
)

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:125.0) Gecko/20100101 Firefox/125.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_4) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4.1 Safari/605.1.15",
}

type TrendyolService struct {
	cfg    *config.Config
	client *http.Client
	sem    chan struct{}
	cache  *cache

	// browser is set asynchronously after startup so it never blocks service init.
	browserMu sync.RWMutex
	browser   *browserPool
}

func NewTrendyolService(cfg *config.Config) *TrendyolService {
	transport := &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		Proxy:               http.ProxyFromEnvironment,
	}

	svc := &TrendyolService{
		cfg: cfg,
		client: &http.Client{
			Transport: transport,
			Timeout:   cfg.RequestTimeout,
		},
		sem:   make(chan struct{}, cfg.MaxConcurrent),
		cache: newCache(cfg.CacheTTL),
	}

	// Browser init is slow (Chrome launch ~5-10s). Run in background so the
	// HTTP server starts immediately. Browser becomes available after it's ready.
	go svc.initBrowser()

	return svc
}

func (s *TrendyolService) initBrowser() {
	bp := newBrowserPool()
	s.browserMu.Lock()
	s.browser = bp
	s.browserMu.Unlock()
}

func (s *TrendyolService) getBrowser() *browserPool {
	s.browserMu.RLock()
	defer s.browserMu.RUnlock()
	return s.browser
}

// ─── Public API ───────────────────────────────────────────────────────────────

// GetProduct fetches a single product. productURL is optional; if provided it
// skips the search/discovery step in strategies 2 & 3.
func (s *TrendyolService) GetProduct(ctx context.Context, productID int64, productURL string) (*models.Product, error) {
	if s.cfg.CacheEnabled {
		if p, ok := s.cache.get(productID); ok {
			return p, nil
		}
	}

	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	product, err := s.tryStrategies(ctx, productID, productURL)
	if err != nil {
		return nil, err
	}

	if s.cfg.CacheEnabled {
		s.cache.set(productID, product)
	}
	return product, nil
}

// GetProducts fetches multiple products concurrently.
func (s *TrendyolService) GetProducts(ctx context.Context, productIDs []int64) map[int64]*models.ProductResult {
	results := make(map[int64]*models.ProductResult, len(productIDs))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, id := range productIDs {
		wg.Add(1)
		go func(productID int64) {
			defer wg.Done()
			product, err := s.GetProduct(ctx, productID, "")
			mu.Lock()
			defer mu.Unlock()
			r := &models.ProductResult{ProductID: productID}
			if err != nil {
				r.Error = err.Error()
			} else {
				r.Product = product
			}
			results[productID] = r
		}(id)
	}

	wg.Wait()
	return results
}

// ─── Strategy orchestration ───────────────────────────────────────────────────

// tryStrategies runs strategies in order, each with its own time budget.
//
//  1. JSON API    — public.trendyol.com   (budget: 10s)
//  2. HTML scrape — www.trendyol.com      (budget: 12s)
//  3. Browser     — headless Chromium     (budget: remaining ctx time)
func (s *TrendyolService) tryStrategies(ctx context.Context, productID int64, productURL string) (*models.Product, error) {
	// ── Strategy 1: JSON API ──────────────────────────────────────────────────
	apiCtx, apiCancel := context.WithTimeout(ctx, apiStrategyBudget)
	p, err := s.fetchWithRetry(apiCtx, productID)
	apiCancel()
	if err == nil {
		return p, nil
	}
	log.Printf("[WARN] [%d] API failed (%v) — trying HTML", productID, summarise(err))

	if ctx.Err() != nil {
		return nil, fmt.Errorf("product %d: request timed out", productID)
	}

	// ── Strategy 2: HTML scraping ─────────────────────────────────────────────
	// Trendyol often blocks plain Go HTTP requests (bot detection).
	// Only attempt if the caller passed a URL (skip the slow search step).
	if productURL != "" {
		htmlCtx, htmlCancel := context.WithTimeout(ctx, htmlStrategyBudget)
		p, err = s.fetchFromHTML(htmlCtx, productID, productURL)
		htmlCancel()
		if err == nil {
			return p, nil
		}
		log.Printf("[WARN] [%d] HTML failed (%v) — trying browser", productID, summarise(err))
	} else {
		log.Printf("[INFO] [%d] Skipping HTML strategy (no URL) — going to browser", productID)
	}

	if ctx.Err() != nil {
		return nil, fmt.Errorf("product %d: request timed out", productID)
	}

	// ── Strategy 3: Headless browser ──────────────────────────────────────────
	bp := s.getBrowser()
	if bp == nil {
		return nil, fmt.Errorf("product %d: all strategies failed (browser not ready)", productID)
	}
	p, err = s.fetchFromBrowser(ctx, productID, productURL)
	if err != nil {
		return nil, fmt.Errorf("product %d: all strategies failed: %w", productID, err)
	}
	return p, nil
}

// ─── Strategy 1: JSON API ─────────────────────────────────────────────────────

func (s *TrendyolService) fetchWithRetry(ctx context.Context, productID int64) (*models.Product, error) {
	var lastErr error
	for attempt := 0; attempt <= s.cfg.RetryCount; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}

		product, err := s.fetchProductAPI(ctx, productID)
		if err == nil {
			return product, nil
		}
		lastErr = err

		// DNS / connection-refused errors won't be fixed by retrying.
		if isNetworkUnreachable(err) {
			break
		}
		log.Printf("[WARN] API attempt %d for %d: %v", attempt+1, productID, err)
	}
	return nil, lastErr
}

func (s *TrendyolService) fetchProductAPI(ctx context.Context, productID int64) (*models.Product, error) {
	url := s.cfg.TrendyolBaseURL + fmt.Sprintf(productDetailPath, productID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	s.setAPIHeaders(req, productID)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, fmt.Errorf("product %d not found", productID)
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("rate limited (429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := readBody(resp)
	if err != nil {
		return nil, err
	}

	var raw models.TrendyolProductResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	return s.mapProduct(&raw.Result, productID), nil
}

func (s *TrendyolService) setAPIHeaders(req *http.Request, productID int64) {
	req.Header.Set("User-Agent", userAgents[rand.Intn(len(userAgents))])
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "tr-TR,tr;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Origin", trendyolWebBase)
	req.Header.Set("Referer", fmt.Sprintf("%s/p-%d", trendyolWebBase, productID))
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
}

// ─── Strategy 3: Headless browser ────────────────────────────────────────────

func (s *TrendyolService) fetchFromBrowser(ctx context.Context, productID int64, productURL string) (*models.Product, error) {
	bp := s.getBrowser()

	pageURL := productURL
	if pageURL == "" {
		pageURL = fmt.Sprintf("%s/p/p-p-%d", trendyolWebBase, productID)
	}
	log.Printf("[INFO] [%d] Browser strategy → %s", productID, pageURL)

	// ── A: Network interception (fastest when API is reachable) ──────────────
	rawJSON, err := bp.getProductJSON(ctx, pageURL, productID)
	if err == nil {
		var tResp models.TrendyolProductResponse
		if json.Unmarshal([]byte(rawJSON), &tResp) == nil &&
			(tResp.Result.ContentID != 0 || tResp.Result.Name != "") {
			log.Printf("[INFO] [%d] Browser: network interception succeeded", productID)
			return s.mapProduct(&tResp.Result, productID), nil
		}
	}
	log.Printf("[INFO] [%d] Network interception: %v — loading page for HTML", productID, summarise(err))

	// ── B: Full page load — get HTML + window state scan ─────────────────────
	pd, err := bp.getPageData(ctx, pageURL)
	if err != nil {
		return nil, fmt.Errorf("browser: %w", err)
	}

	// B1: DOM reader — reads from rendered React/Puzzle elements (price, variants, etc.)
	if pd.DOMJSON != "" && pd.DOMJSON != "null" {
		if p, err := s.parseDOMData(pd.DOMJSON, productID, pd.FinalURL); err == nil {
			log.Printf("[INFO] [%d] Browser: parsed via DOM reader", productID)
			return p, nil
		} else {
			log.Printf("[DEBUG] [%d] DOM reader: %v (raw: %s)", productID, err, truncate(pd.DOMJSON, 300))
		}
	}

	// B2: window variable scan (covers __PRODUCT_DETAIL_APP_INITIAL_STATE__ etc.)
	if pd.StateJSON != "" && pd.StateJSON != "null" {
		if p := s.parseWindowScan(pd.StateJSON, productID, pd.FinalURL); p != nil {
			log.Printf("[INFO] [%d] Browser: parsed via window scan", productID)
			return p, nil
		}
	}

	// B3: __NEXT_DATA__ (Next.js SSR)
	if p, err := s.parseNextData(pd.HTML, productID, pd.FinalURL); err == nil {
		log.Printf("[INFO] [%d] Browser: parsed via __NEXT_DATA__", productID)
		return p, nil
	}

	// B4: window.__PRODUCT_DETAIL_APP_INITIAL_STATE__ in HTML
	if p, err := s.parseWindowState(pd.HTML, productID, pd.FinalURL); err == nil {
		log.Printf("[INFO] [%d] Browser: parsed via window state HTML", productID)
		return p, nil
	}

	// B5: Search for contentId in the HTML and extract surrounding JSON
	if p, err := s.parseByContentIDSearch(pd.HTML, productID, pd.FinalURL); err == nil {
		log.Printf("[INFO] [%d] Browser: parsed via contentId search", productID)
		return p, nil
	}

	// B6: JSON-LD schema.org
	if p, err := s.parseJSONLD(pd.HTML, productID, pd.FinalURL); err == nil {
		log.Printf("[INFO] [%d] Browser: parsed via JSON-LD", productID)
		return p, nil
	}

	// B7: OpenGraph (basic info — name + image, no price)
	if p, err := s.parseOpenGraph(pd.HTML, productID, pd.FinalURL); err == nil {
		log.Printf("[INFO] [%d] Browser: parsed via OpenGraph (basic only)", productID)
		return p, nil
	}

	// All parsers failed.
	log.Printf("[DEBUG] [%d] DOM JSON: %s", productID, truncate(pd.DOMJSON, 400))
	log.Printf("[DEBUG] [%d] State JSON: %s", productID, truncate(pd.StateJSON, 400))

	return nil, fmt.Errorf("browser: page loaded at %s but no product data found", pd.FinalURL)
}

// parseDOMData parses the output of domReaderJS and builds a Product.
// This is the primary browser parsing strategy — reads from rendered DOM elements.
func (s *TrendyolService) parseDOMData(domJSON string, productID int64, pageURL string) (*models.Product, error) {
	var dom struct {
		Name            string  `json:"name"`
		Brand           string  `json:"brand"`
		DiscountedPrice float64 `json:"discountedPrice"`
		OriginalPrice   float64 `json:"originalPrice"`
		Currency        string  `json:"currency"`
		InStock         bool    `json:"inStock"`
		Valid           bool    `json:"valid"`
		Variants        []struct {
			Value   string `json:"value"`
			InStock bool   `json:"inStock"`
		} `json:"variants"`
		Images []string `json:"images"`
	}

	if err := json.Unmarshal([]byte(domJSON), &dom); err != nil {
		return nil, fmt.Errorf("unmarshal DOM JSON: %w", err)
	}
	if !dom.Valid {
		return nil, fmt.Errorf("DOM reader found no valid product data (name=%q price=%.2f)", dom.Name, dom.DiscountedPrice)
	}

	variants := make([]models.Variant, 0, len(dom.Variants))
	for _, v := range dom.Variants {
		variants = append(variants, models.Variant{
			AttributeName:   "Beden",
			AttributeValue:  v.Value,
			InStock:         v.InStock,
			Price:           dom.DiscountedPrice,
			DiscountedPrice: dom.DiscountedPrice,
		})
	}

	orig := dom.OriginalPrice
	if orig == 0 {
		orig = dom.DiscountedPrice
	}
	cur := dom.Currency
	if cur == "" {
		cur = "TRY"
	}

	return &models.Product{
		ContentID: productID,
		Name:      dom.Name,
		Brand:     dom.Brand,
		URL:       pageURL,
		Price: models.Price{
			Original:   orig,
			Discounted: dom.DiscountedPrice,
			Currency:   cur,
		},
		Variants:  variants,
		InStock:   dom.InStock,
		Images:    dom.Images,
		FetchedAt: time.Now().UTC(),
	}, nil
}

// parseWindowScan parses the result of windowScanJS: {"k":"varName","v":{...}}
func (s *TrendyolService) parseWindowScan(stateJSON string, productID int64, pageURL string) *models.Product {
	var scan struct {
		K string          `json:"k"`
		V json.RawMessage `json:"v"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &scan); err != nil || len(scan.V) == 0 {
		return nil
	}
	log.Printf("[DEBUG] [%d] Window scan found key: %s", productID, scan.K)

	// Try direct product
	if p, err := s.tryUnmarshalProduct(scan.V, productID, pageURL); err == nil {
		return p
	}
	// Try wrapper
	var wrapper map[string]json.RawMessage
	if json.Unmarshal(scan.V, &wrapper) != nil {
		return nil
	}
	for _, key := range []string{"product", "productDetail", "result", "data", "ssrData", "initialData"} {
		raw, ok := wrapper[key]
		if !ok {
			continue
		}
		if p, err := s.tryUnmarshalProduct(raw, productID, pageURL); err == nil {
			return p
		}
		var inner map[string]json.RawMessage
		if json.Unmarshal(raw, &inner) == nil {
			for _, ikey := range []string{"product", "productDetail", "result"} {
				if ir, ok := inner[ikey]; ok {
					if p, err := s.tryUnmarshalProduct(ir, productID, pageURL); err == nil {
						return p
					}
				}
			}
		}
	}
	return nil
}

// parseByContentIDSearch finds "contentId":{id} in the HTML and extracts the
// enclosing JSON object. Handles Trendyol's "Puzzle" SSR format.
func (s *TrendyolService) parseByContentIDSearch(body []byte, productID int64, pageURL string) (*models.Product, error) {
	needle := []byte(fmt.Sprintf(`"contentId":%d`, productID))
	needleAlt := []byte(fmt.Sprintf(`"contentId": %d`, productID))

	idx := bytes.Index(body, needle)
	if idx == -1 {
		idx = bytes.Index(body, needleAlt)
	}
	if idx == -1 {
		return nil, fmt.Errorf("contentId %d not found in HTML", productID)
	}
	log.Printf("[DEBUG] [%d] contentId found at offset %d", productID, idx)

	// Walk backwards with increasing window to find the enclosing JSON object.
	for _, lookback := range []int{500, 2000, 10000, 50000} {
		start := idx - lookback
		if start < 0 {
			start = 0
		}
		// Find last '{' before our needle in this window.
		openIdx := bytes.LastIndexByte(body[start:idx], '{')
		if openIdx == -1 {
			continue
		}
		openIdx += start

		jsonBytes, err := extractBalancedJSON(body[openIdx:])
		if err != nil || len(jsonBytes) < 50 {
			continue
		}

		if p, err := s.tryUnmarshalProduct(json.RawMessage(jsonBytes), productID, pageURL); err == nil {
			return p, nil
		}

		var wrapper map[string]json.RawMessage
		if json.Unmarshal(jsonBytes, &wrapper) == nil {
			for _, key := range []string{"product", "result", "productDetail", "data"} {
				if raw, ok := wrapper[key]; ok {
					if p, err := s.tryUnmarshalProduct(raw, productID, pageURL); err == nil {
						return p, nil
					}
				}
			}
		}
	}
	return nil, fmt.Errorf("contentId found but couldn't extract surrounding JSON")
}

// parseOpenGraph extracts basic product data from Open Graph meta tags.
// Always present on Trendyol product pages; gives name, price, image.
func (s *TrendyolService) parseOpenGraph(body []byte, productID int64, pageURL string) (*models.Product, error) {
	ogTitle := ogMeta(body, "og:title")
	ogPrice := ogMeta(body, "product:price:amount")
	if ogPrice == "" {
		ogPrice = ogMeta(body, "og:price:amount")
	}
	ogCurrency := ogMeta(body, "product:price:currency")
	if ogCurrency == "" {
		ogCurrency = "TRY"
	}
	ogImage := ogMeta(body, "og:image")
	ogAvail := ogMeta(body, "product:availability")
	ogBrand := ogMeta(body, "og:brand")
	if ogBrand == "" {
		ogBrand = ogMeta(body, "product:brand")
	}

	if ogTitle == "" {
		return nil, fmt.Errorf("no og:title found")
	}

	p := &models.Product{
		ContentID: productID,
		Name:      ogTitle,
		Brand:     ogBrand,
		URL:       pageURL,
		FetchedAt: time.Now().UTC(),
	}

	if ogPrice != "" {
		priceF, _ := strconv.ParseFloat(strings.ReplaceAll(ogPrice, ",", "."), 64)
		p.Price = models.Price{
			Original:   priceF,
			Discounted: priceF,
			Currency:   ogCurrency,
		}
	}
	if ogImage != "" {
		p.Images = []string{ogImage}
	}
	p.InStock = !strings.Contains(strings.ToLower(ogAvail), "out")

	return p, nil
}

func ogMeta(body []byte, property string) string {
	// Match both attribute orderings: property="X" content="Y" and content="Y" property="X"
	re := regexp.MustCompile(`(?i)<meta[^>]+property="` + regexp.QuoteMeta(property) + `"[^>]+content="([^"]*)"`)
	if m := re.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	re2 := regexp.MustCompile(`(?i)<meta[^>]+content="([^"]*)"[^>]+property="` + regexp.QuoteMeta(property) + `"`)
	if m := re2.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ─── Strategy 2: HTML scraping ────────────────────────────────────────────────

func (s *TrendyolService) fetchFromHTML(ctx context.Context, productID int64, knownURL string) (*models.Product, error) {
	pageURL := knownURL
	if pageURL == "" {
		discovered, err := s.discoverProductURL(ctx, productID)
		if err != nil {
			return nil, fmt.Errorf("URL discovery: %w", err)
		}
		pageURL = discovered
	}
	return s.scrapeProductPage(ctx, pageURL, productID)
}

func (s *TrendyolService) discoverProductURL(ctx context.Context, productID int64) (string, error) {
	searchURL := fmt.Sprintf("%s/sr?q=%d&qt=%d&st=%d&os=1", trendyolWebBase, productID, productID, productID)
	body, err := s.fetchHTML(ctx, searchURL)
	if err != nil {
		return "", fmt.Errorf("search: %w", err)
	}

	re := regexp.MustCompile(fmt.Sprintf(`href="(/[^"]*-p-%d[?"#][^"]*)`, productID))
	matches := re.FindSubmatch(body)
	if matches == nil {
		return "", fmt.Errorf("product %d not in search results", productID)
	}

	href := string(matches[1])
	if idx := strings.IndexByte(href, '?'); idx != -1 {
		href = href[:idx]
	}
	return trendyolWebBase + href, nil
}

func (s *TrendyolService) scrapeProductPage(ctx context.Context, pageURL string, productID int64) (*models.Product, error) {
	body, err := s.fetchHTML(ctx, pageURL)
	if err != nil {
		return nil, fmt.Errorf("fetching page: %w", err)
	}

	if p, err := s.parseNextData(body, productID, pageURL); err == nil {
		return p, nil
	}
	if p, err := s.parseWindowState(body, productID, pageURL); err == nil {
		return p, nil
	}
	return s.parseJSONLD(body, productID, pageURL)
}

func (s *TrendyolService) fetchHTML(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgents[rand.Intn(len(userAgents))])
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "tr-TR,tr;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Referer", trendyolWebBase+"/")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("page not found: %s", url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return readBody(resp)
}

// ─── HTML parsers ─────────────────────────────────────────────────────────────

func (s *TrendyolService) parseNextData(body []byte, productID int64, pageURL string) (*models.Product, error) {
	idx := bytes.Index(body, []byte(`"__NEXT_DATA__"`))
	if idx == -1 {
		return nil, fmt.Errorf("__NEXT_DATA__ not found")
	}
	gt := bytes.IndexByte(body[idx:], '>')
	if gt == -1 {
		return nil, fmt.Errorf("malformed __NEXT_DATA__")
	}
	jsonStart := idx + gt + 1
	end := bytes.Index(body[jsonStart:], []byte("</script>"))
	if end == -1 {
		return nil, fmt.Errorf("__NEXT_DATA__ not closed")
	}
	jsonBytes := bytes.TrimSpace(body[jsonStart : jsonStart+end])

	var root map[string]json.RawMessage
	if err := json.Unmarshal(jsonBytes, &root); err != nil {
		return nil, fmt.Errorf("parsing __NEXT_DATA__: %w", err)
	}

	pageProps, err := walkJSON(root, "props", "pageProps")
	if err != nil {
		return nil, err
	}
	var pp map[string]json.RawMessage
	if err := json.Unmarshal(pageProps, &pp); err != nil {
		return nil, err
	}

	for _, key := range []string{"product", "ssrData", "initialData", "initialState"} {
		raw, ok := pp[key]
		if !ok {
			continue
		}
		if p, err := s.tryUnmarshalProduct(raw, productID, pageURL); err == nil {
			return p, nil
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err == nil {
			for _, nk := range []string{"product", "productDetail", "result"} {
				if nr, ok := nested[nk]; ok {
					if p, err := s.tryUnmarshalProduct(nr, productID, pageURL); err == nil {
						return p, nil
					}
				}
			}
		}
	}
	return nil, fmt.Errorf("product not found in __NEXT_DATA__")
}

func (s *TrendyolService) parseWindowState(body []byte, productID int64, pageURL string) (*models.Product, error) {
	marker := []byte("window.__PRODUCT_DETAIL_APP_INITIAL_STATE__=")
	idx := bytes.Index(body, marker)
	if idx == -1 {
		return nil, fmt.Errorf("window state not found")
	}
	jsonBytes, err := extractBalancedJSON(body[idx+len(marker):])
	if err != nil {
		return nil, err
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(jsonBytes, &state); err != nil {
		return nil, err
	}
	for _, key := range []string{"product", "productDetail", "result"} {
		if raw, ok := state[key]; ok {
			if p, err := s.tryUnmarshalProduct(raw, productID, pageURL); err == nil {
				return p, nil
			}
		}
	}
	return nil, fmt.Errorf("product not found in window state")
}

func (s *TrendyolService) parseJSONLD(body []byte, productID int64, pageURL string) (*models.Product, error) {
	remaining := body
	for {
		idx := bytes.Index(remaining, []byte(`"application/ld+json"`))
		if idx == -1 {
			break
		}
		gt := bytes.IndexByte(remaining[idx:], '>')
		if gt == -1 {
			break
		}
		jsonStart := idx + gt + 1
		end := bytes.Index(remaining[jsonStart:], []byte("</script>"))
		if end == -1 {
			remaining = remaining[idx+1:]
			continue
		}
		var ld models.JSONLDProduct
		if err := json.Unmarshal(bytes.TrimSpace(remaining[jsonStart:jsonStart+end]), &ld); err != nil || ld.Type != "Product" {
			remaining = remaining[idx+1:]
			continue
		}
		return s.mapJSONLD(&ld, productID, pageURL), nil
	}
	return nil, fmt.Errorf("no Product JSON-LD found")
}

// ─── Mapping helpers ──────────────────────────────────────────────────────────

func (s *TrendyolService) tryUnmarshalProduct(raw json.RawMessage, productID int64, pageURL string) (*models.Product, error) {
	var tp models.TrendyolProduct
	if err := json.Unmarshal(raw, &tp); err != nil {
		return nil, err
	}
	if tp.ContentID == 0 && tp.ProductID == 0 && tp.Name == "" {
		return nil, fmt.Errorf("empty product")
	}
	return s.mapProduct(&tp, productID), nil
}

func (s *TrendyolService) mapProduct(tp *models.TrendyolProduct, fallbackID int64) *models.Product {
	variants := make([]models.Variant, 0, len(tp.AllVariants))
	inStock := false

	for _, v := range tp.AllVariants {
		qty := v.Quantity
		if qty == 0 {
			qty = v.Stock
		}
		if v.InStock {
			inStock = true
		}
		variants = append(variants, models.Variant{
			AttributeName:   v.AttributeName,
			AttributeValue:  v.AttributeValue,
			InStock:         v.InStock,
			Quantity:        qty,
			Price:           v.Price,
			DiscountedPrice: v.DiscountedPrice,
			Barcode:         v.Barcode,
			ItemNumber:      v.ItemNumber,
		})
	}

	images := make([]string, 0, len(tp.Images))
	for _, img := range tp.Images {
		if img.URL != "" {
			images = append(images, cdnBase+"/"+img.URL)
		}
	}

	cid := tp.ContentID
	if cid == 0 {
		cid = fallbackID
	}

	return &models.Product{
		ProductID: tp.ProductID,
		ContentID: cid,
		Name:      tp.Name,
		Brand:     tp.Brand.Name,
		URL:       fmt.Sprintf("%s/p-%d", trendyolWebBase, cid),
		Price: models.Price{
			Original:   tp.Price.OriginalPrice,
			Discounted: tp.Price.DiscountedPrice,
			Currency:   tp.Price.Currency,
		},
		Variants:  variants,
		InStock:   inStock,
		Images:    images,
		FetchedAt: time.Now().UTC(),
	}
}

func (s *TrendyolService) mapJSONLD(ld *models.JSONLDProduct, productID int64, pageURL string) *models.Product {
	p := &models.Product{
		ContentID: productID,
		Name:      ld.Name,
		Brand:     ld.Brand.Name,
		URL:       pageURL,
		FetchedAt: time.Now().UTC(),
	}
	if ld.Offers.Price != "" {
		var price float64
		fmt.Sscanf(ld.Offers.Price, "%f", &price)
		p.Price = models.Price{Original: price, Discounted: price, Currency: ld.Offers.PriceCurrency}
	}
	p.InStock = strings.Contains(ld.Offers.Availability, "InStock")
	return p
}

// ─── Utilities ────────────────────────────────────────────────────────────────

// isNetworkUnreachable returns true for errors that won't improve with a retry.
func isNetworkUnreachable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, needle := range []string{"no such host", "connection refused", "NXDOMAIN", "i/o timeout"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// summarise returns a short error string for log lines.
func summarise(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

func walkJSON(root map[string]json.RawMessage, keys ...string) (json.RawMessage, error) {
	current := root
	for i, key := range keys {
		raw, ok := current[key]
		if !ok {
			return nil, fmt.Errorf("key %q not found", key)
		}
		if i == len(keys)-1 {
			return raw, nil
		}
		var next map[string]json.RawMessage
		if err := json.Unmarshal(raw, &next); err != nil {
			return nil, fmt.Errorf("at %q: %w", key, err)
		}
		current = next
	}
	return nil, fmt.Errorf("empty keys")
}

func extractBalancedJSON(data []byte) ([]byte, error) {
	if len(data) == 0 || data[0] != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	depth, inStr, esc := 0, false, false
	for i, c := range data {
		if esc {
			esc = false
			continue
		}
		switch c {
		case '\\':
			if inStr {
				esc = true
			}
		case '"':
			inStr = !inStr
		case '{':
			if !inStr {
				depth++
			}
		case '}':
			if !inStr {
				depth--
				if depth == 0 {
					return data[:i+1], nil
				}
			}
		}
	}
	return nil, fmt.Errorf("unclosed JSON")
}

func readBody(resp *http.Response) ([]byte, error) {
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		defer gr.Close()
		reader = gr
	}
	return io.ReadAll(io.LimitReader(reader, 10<<20))
}
