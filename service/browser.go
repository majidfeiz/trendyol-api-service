package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// stealthJS hides Chromium's automation signatures from Cloudflare Bot Management.
const stealthJS = `(function(){
Object.defineProperty(navigator,'webdriver',{get:()=>undefined});
if(!window.chrome){window.chrome={runtime:{},loadTimes:function(){},csi:function(){},app:{}};}
Object.defineProperty(navigator,'languages',{get:()=>['tr-TR','tr','en-US','en']});
const fp=[{name:'Chrome PDF Plugin',filename:'internal-pdf-viewer',description:'Portable Document Format'},{name:'Chrome PDF Viewer',filename:'mhjfbmdgcfjbbpaeojofohoefgiehjai',description:''},{name:'Native Client',filename:'internal-nacl-plugin',description:''}];
Object.defineProperty(navigator,'plugins',{get:()=>{const a=Object.create(PluginArray.prototype);fp.forEach((p,i)=>Object.defineProperty(a,i,{value:p,enumerable:true}));Object.defineProperty(a,'length',{value:fp.length});a.item=i=>fp[i];a.namedItem=n=>fp.find(p=>p.name===n)||null;a.refresh=()=>{};return a;}});
Object.defineProperty(screen,'width',{get:()=>1920});Object.defineProperty(screen,'height',{get:()=>1080});
Object.defineProperty(navigator,'hardwareConcurrency',{get:()=>8});
try{const o=window.navigator.permissions.query;window.navigator.permissions.query=(p)=>p.name==='notifications'?Promise.resolve({state:Notification.permission}):o(p);}catch(e){}
})()`

// domReaderJS reads product data directly from the rendered DOM elements.
// Selectors are based on DevTools inspection of actual Trendyol product pages.
const domReaderJS = `(function(){
function txt(sel){var e=document.querySelector(sel);return e?e.textContent.trim():'';}
function num(sel){
  var t=txt(sel);if(!t)return 0;
  // Turkish: "3.233,35 TL" → remove thousand-sep dots → swap comma → strip non-numeric
  return parseFloat(t.replace(/\./g,'').replace(',','.').replace(/[^\d.]/g,''))||0;
}

var r={};

// ── Name ──────────────────────────────────────────────────────────────────────
r.name = txt('h1.pr-new-br')||txt('h1[class*="prdct"]')||txt('h1[class*="product"]')||txt('h1');

// ── Brand ─────────────────────────────────────────────────────────────────────
r.brand = txt('.pr-in-w a span')||txt('[class*="brand-name"]')||txt('.product-brand');

// ── Price ─────────────────────────────────────────────────────────────────────
r.discountedPrice = num('.prc-box-dscntd')||num('[class*="prc-dscntd"]')||
                    num('p.new-price')||num('[class*="new-price"]')||
                    num('[class*="discountedPrice"]')||num('[class*="discounted-price"]')||
                    num('[data-testid*="price"]')||num('[class*="product-price"]')||
                    num('.campaign-price-wrapper p:last-child');
r.originalPrice   = num('.prc-box-orgnl')||num('[class*="prc-orgnl"]')||
                    num('p.old-price')||num('[class*="old-price"]')||
                    num('[class*="originalPrice"]')||num('[class*="original-price"]')||
                    r.discountedPrice;
r.currency = 'TRY';

// ── Stock ─────────────────────────────────────────────────────────────────────
var oosEl = document.querySelector(
  '[data-testid="out-of-stock"], [class="out-of-stock-wrapper"], '+
  'button[class*="add-to-basket"][disabled]'
);
r.inStock = !oosEl;

// ── Variants / Sizes ──────────────────────────────────────────────────────────
r.variants=[];
var seen={};
document.querySelectorAll(
  'button.size-box, [data-testid="size-box"], '+
  '.sp-itm, [class*="sp-itm"], '+
  'button[class*="size"], [class*="size-box"], [class*="sizeBox"], '+
  '.slicing-attr-item button, [class*="variant-size"], [class*="size-btn"]'
).forEach(function(el){
  var t=el.textContent.trim();
  if(!t||t.length>25||seen[t])return;
  seen[t]=true;
  var oos = el.disabled ||
            el.getAttribute('aria-disabled')==='true' ||
            !!(el.className+'').match(/disabled|outofstock|out-of-stock|pasif|no-stock/i);
  r.variants.push({value:t, inStock:!oos});
});

// ── Images ────────────────────────────────────────────────────────────────────
// Only product photos: skip SVGs, icons, stickers, logos, related-product thumbnails.
r.images=[];
var imgSeen={};

function isProductImg(s){
  if(!s||s.indexOf('cdn.dsmcdn.com')===-1)return false;
  if(s.indexOf('.svg')!==-1)return false;
  if(s.indexOf('sticker')!==-1||s.indexOf('stamp')!==-1)return false;
  if(s.indexOf('web/production/')!==-1||s.indexOf('sfint/')!==-1)return false;
  if(s.indexOf('web/flags/')!==-1||s.indexOf('retailfs-banner/')!==-1)return false;
  if(s.indexOf('seller-store/')!==-1||s.indexOf('mobile/pdp/')!==-1)return false;
  if(s.indexOf('mobile/reviewrating/')!==-1)return false;
  if(s.indexOf('/30/30/')!==-1||s.indexOf('/48/48/')!==-1||s.indexOf('/120/')!==-1)return false;
  return s.indexOf('_org_zoom')!==-1||s.indexOf('/1_org.')!==-1||
         s.indexOf('/2_org.')!==-1||s.indexOf('/3_org.')!==-1||s.indexOf('/4_org.')!==-1;
}

// Primary: look inside known product gallery containers
var galContainers=[
  '.product-slide-showcase','.gallery-modal-content',
  '.product-images-wrapper','[class*="product-slide"]',
  '.pr-img-w','[class*="pdp-img"]','[class*="product-img"]'
];
for(var ci=0;ci<galContainers.length;ci++){
  var gc=document.querySelector(galContainers[ci]);
  if(gc){
    gc.querySelectorAll('img').forEach(function(e){
      var s=e.src||e.getAttribute('data-src')||'';
      if(isProductImg(s)&&!imgSeen[s]){imgSeen[s]=true;r.images.push(s);}
    });
    if(r.images.length)break;
  }
}

// Fallback: data-testid="image" filtered to product photos only
if(!r.images.length){
  document.querySelectorAll('img[data-testid="image"]').forEach(function(e){
    var s=e.src||'';
    if(isProductImg(s)&&!imgSeen[s]){imgSeen[s]=true;r.images.push(s);}
  });
}

// Last resort: any CDN product photo
if(!r.images.length){
  document.querySelectorAll('img[src*="cdn.dsmcdn.com"]').forEach(function(e){
    var s=e.src||'';
    if(isProductImg(s)&&!imgSeen[s]){imgSeen[s]=true;r.images.push(s);}
  });
}

// Require both name AND price; if price is missing the page's window state
// parsers (parseWindowScan) will provide complete structured data instead.
r.valid=!!(r.name&&r.discountedPrice>0);
return JSON.stringify(r);
})()`

// windowScanJS scans ALL window properties for Trendyol product state.
// Returns JSON: {"k": "variableName", "v": {...product data...}}
// or null if nothing is found.
const windowScanJS = `(function(){
var known=['__PRODUCT_DETAIL_APP_INITIAL_STATE__','__INITIAL_STATE__','__APP_INITIAL_STATE__','__APP_DATA__','__STATE__','__DATA__','__STORE__','pageData','productData'];
for(var i=0;i<known.length;i++){if(window[known[i]]){try{return JSON.stringify({k:known[i],v:window[known[i]]});}catch(e){}}}
var keys=Object.keys(window);
for(var j=0;j<keys.length;j++){
  var k=keys[j];if(k.length<2||typeof window[k]!=='object'||!window[k])continue;
  try{
    var s=JSON.stringify(window[k]);
    if(s&&s.length>200&&(s.indexOf('"contentId"')!==-1||s.indexOf('"allVariants"')!==-1||s.indexOf('"productId"')!==-1)){
      return JSON.stringify({k:k,v:window[k]});
    }
  }catch(e){}
}
return null;
})()`

type browserPool struct {
	allocCtx context.Context
	cancel   context.CancelFunc
}

func newBrowserPool() *browserPool {
	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("blink-settings", "imagesEnabled=false"),
		chromedp.Flag("lang", "tr-TR"),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.WindowSize(1920, 1080),
	}
	if p := os.Getenv("CHROME_PATH"); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)

	probeCtx, probeCancel := chromedp.NewContext(allocCtx)
	probeCtx, probeTimeoutCancel := context.WithTimeout(probeCtx, 15*time.Second)
	defer probeTimeoutCancel()
	defer probeCancel()
	if err := chromedp.Run(probeCtx); err != nil {
		cancel()
		log.Printf("[WARN] Headless Chrome unavailable: %v", err)
		return nil
	}
	log.Println("[INFO] Headless Chrome ready (stealth mode)")
	return &browserPool{allocCtx: allocCtx, cancel: cancel}
}

func (bp *browserPool) close() {
	if bp != nil {
		bp.cancel()
	}
}

// PageData holds what we extracted from the browser.
type PageData struct {
	HTML      []byte
	StateJSON string // from window variable scan
	DOMJSON   string // from DOM reader (rendered elements)
	FinalURL  string
}

// getPageData navigates to pageURL with stealth mode, waits for React to render,
// then collects: full HTML, window state scan, and DOM-reader output.
func (bp *browserPool) getPageData(ctx context.Context, pageURL string) (*PageData, error) {
	tabCtx, tabCancel := chromedp.NewContext(bp.allocCtx)
	defer tabCancel()
	tCtx, tCancel := context.WithTimeout(tabCtx, 35*time.Second)
	defer tCancel()

	var html, stateJSON, domJSON, finalURL string

	err := chromedp.Run(tCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(stealthJS).Do(ctx)
			return err
		}),
		network.Enable(),
		setHeaders(),
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		// Let React / Puzzle framework finish hydrating (renders price + variants).
		chromedp.Sleep(5*time.Second),
		chromedp.Location(&finalURL),
		chromedp.Evaluate(`document.documentElement.outerHTML`, &html),
		chromedp.Evaluate(windowScanJS, &stateJSON),
		chromedp.Evaluate(domReaderJS, &domJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("browser navigate: %w", err)
	}

	log.Printf("[INFO] Browser: %s → %s (HTML:%d DOM:%d state:%d bytes)",
		pageURL, finalURL, len(html), len(domJSON), len(stateJSON))

	return &PageData{
		HTML:      []byte(html),
		StateJSON: stateJSON,
		DOMJSON:   domJSON,
		FinalURL:  finalURL,
	}, nil
}

// getProductJSON intercepts the page's productDetail API call.
// Returns raw JSON from the API, or an error if no such call is made.
func (bp *browserPool) getProductJSON(ctx context.Context, pageURL string, productID int64) (string, error) {
	tabCtx, tabCancel := chromedp.NewContext(bp.allocCtx)
	defer tabCancel()
	tCtx, tCancel := context.WithTimeout(tabCtx, 30*time.Second)
	defer tCancel()

	var (
		mu          sync.Mutex
		productJSON string
		found       = make(chan struct{}, 1)
	)

	chromedp.ListenTarget(tCtx, func(ev interface{}) {
		e, ok := ev.(*network.EventResponseReceived)
		if !ok {
			return
		}
		u := e.Response.URL
		if !strings.Contains(u, "productDetail") && !strings.Contains(u, "productdetail") {
			return
		}
		log.Printf("[INFO] [%d] Intercepted: %s (HTTP %d)", productID, u, e.Response.Status)
		go func(id network.RequestID) {
			body, err := network.GetResponseBody(id).Do(tCtx)
			if err != nil || len(body) == 0 {
				return
			}
			mu.Lock()
			productJSON = string(body)
			mu.Unlock()
			select {
			case found <- struct{}{}:
			default:
			}
		}(e.RequestID)
	})

	err := chromedp.Run(tCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(stealthJS).Do(ctx)
			return err
		}),
		network.Enable(),
		setHeaders(),
		chromedp.Navigate(pageURL),
	)
	if err != nil {
		return "", err
	}

	select {
	case <-found:
		mu.Lock()
		j := productJSON
		mu.Unlock()
		return j, nil
	case <-tCtx.Done():
		return "", fmt.Errorf("productDetail API not intercepted")
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func setHeaders() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		if err := network.SetExtraHTTPHeaders(network.Headers{
			"Accept-Language": "tr-TR,tr;q=0.9,en;q=0.8",
		}).Do(ctx); err != nil {
			return err
		}
		return emulation.SetUserAgentOverride(chromeUA).
			WithAcceptLanguage("tr-TR,tr;q=0.9").
			Do(ctx)
	}
}
