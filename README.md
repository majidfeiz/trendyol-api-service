# Trendyol API Service

یک سرویس Go برای گرفتن **قیمت، موجودی، سایزها و تصاویر** محصولات Trendyol از طریق `contentId`.

---

## فهرست مطالب

- [نحوه کار](#نحوه-کار)
- [نصب و راه‌اندازی](#نصب-و-راه‌اندازی)
- [تنظیمات](#تنظیمات)
- [API Reference](#api-reference)
  - [Health Check](#health-check)
  - [یک محصول](#گرفتن-یک-محصول)
  - [چند محصول — GET](#گرفتن-چند-محصول-get)
  - [چند محصول — POST](#گرفتن-چند-محصول-post)
- [ساختار Response](#ساختار-response)
- [کدهای خطا](#کدهای-خطا)
- [نکات مهم](#نکات-مهم)

---

## نحوه کار

سرویس برای هر محصول سه استراتژی را به ترتیب امتحان می‌کند:

1. **JSON API** — درخواست مستقیم به `public.trendyol.com` (سریع‌ترین، ۱۰ ثانیه budget)
2. **HTML Scraping** — پارس کردن صفحه محصول در صورتی که URL پاس داده شده باشد (۱۲ ثانیه budget)
3. **Headless Browser** — Chromium بدون GUI؛ اگر دو روش قبلی block شوند صفحه را رندر کرده و از DOM و window state داده می‌خواند (باقی‌مانده timeout)

در صورت موفقیت هر استراتژی، نتیجه در حافظه کش می‌شود.

---

## نصب و راه‌اندازی

### پیش‌نیاز

- Docker + Docker Compose

### مراحل

```bash
# ۱. کپی فایل تنظیمات
cp .env.example .env

# ۲. ویرایش .env در صورت نیاز (اختیاری)
nano .env

# ۳. Build و اجرا
docker compose up -d

# بررسی وضعیت
docker compose ps

# مشاهده لاگ
docker compose logs -f

# متوقف کردن
docker compose down
```

سرویس روی پورت `8080` اجرا می‌شود.

> **توجه:** Docker image شامل Chromium است و نیاز به حداقل **256 MB** حافظه `/dev/shm` دارد (در `docker-compose.yml` تنظیم شده).

---

## تنظیمات

فایل `.env` را بر اساس `.env.example` بسازید:

| متغیر | پیش‌فرض | توضیح |
|-------|---------|-------|
| `PORT` | `8080` | پورت سرویس |
| `TRENDYOL_BASE_URL` | `https://public.trendyol.com` | آدرس پایه API ترندیول |
| `REQUEST_TIMEOUT_SECONDS` | `8` | timeout هر درخواست HTTP به ترندیول (ثانیه) |
| `TOTAL_TIMEOUT_SECONDS` | `60` | حداکثر زمان کل برای یک محصول (شامل هر سه استراتژی) |
| `MAX_CONCURRENT` | `20` | حداکثر درخواست همزمان به ترندیول |
| `RETRY_COUNT` | `1` | تعداد تلاش مجدد در صورت خطای API |
| `CACHE_ENABLED` | `true` | فعال/غیرفعال کردن کش |
| `CACHE_TTL_SECONDS` | `120` | مدت اعتبار کش (ثانیه) |
| `MAX_BATCH_SIZE` | `50` | حداکثر تعداد محصول در یک درخواست batch |
| `CHROME_PATH` | `/usr/bin/chromium-browser` | مسیر باینری Chromium |

---

## API Reference

### Health Check

بررسی سالم بودن سرویس.

```
GET /health
```

**Response:**
```json
{
  "status": "ok",
  "service": "trendyol-api-service"
}
```

---

### گرفتن یک محصول

```
GET /api/v1/product/{contentId}
```

`contentId` همان عدد انتهای URL ترندیول است:
`https://www.trendyol.com/nike/air-force-p-`**`38173346`**

**پارامترهای Query (اختیاری):**

| پارامتر | توضیح |
|---------|-------|
| `url` | لینک کامل صفحه محصول؛ باعث می‌شود استراتژی HTML مستقیماً بدون جستجو اجرا شود |

**مثال:**
```bash
curl http://localhost:8080/api/v1/product/38173346

# با URL (سریع‌تر):
curl "http://localhost:8080/api/v1/product/38173346?url=https://www.trendyol.com/nike/air-force-p-38173346"
```

**Response:**
```json
{
  "success": true,
  "data": {
    "productId": 38173346,
    "contentId": 38173346,
    "name": "Nike Air Force 1 07 Erkek Sneaker",
    "brand": "Nike",
    "url": "https://www.trendyol.com/p-38173346",
    "price": {
      "original": 2999.99,
      "discounted": 2499.99,
      "currency": "TRY"
    },
    "inStock": true,
    "variants": [
      {
        "attributeName": "Beden",
        "attributeValue": "40",
        "inStock": true,
        "quantity": 8,
        "price": 2999.99,
        "discountedPrice": 2499.99,
        "barcode": "1234567890123",
        "itemNumber": 987654321
      },
      {
        "attributeName": "Beden",
        "attributeValue": "41",
        "inStock": false,
        "quantity": 0,
        "price": 2999.99,
        "discountedPrice": 2499.99,
        "barcode": "1234567890124",
        "itemNumber": 987654322
      }
    ],
    "images": [
      "https://cdn.dsmcdn.com/ty123/prod/QC/20241119/photo1_org_zoom.jpg",
      "https://cdn.dsmcdn.com/ty123/prod/QC/20241119/photo2_org_zoom.jpg"
    ],
    "fetchedAt": "2026-06-05T10:30:00Z"
  }
}
```

---

### گرفتن چند محصول (GET)

مناسب برای استفاده ساده از browser یا curl بدون body.

```
GET /api/v1/products?ids={id1},{id2},{id3}
```

**مثال:**
```bash
curl "http://localhost:8080/api/v1/products?ids=38173346,47894770,12345678"
```

---

### گرفتن چند محصول (POST)

مناسب برای ارسال لیست بزرگ‌تر از طریق request body.

```
POST /api/v1/products
Content-Type: application/json
```

**Body:**
```json
{
  "ids": [38173346, 47894770, 12345678]
}
```

**مثال:**
```bash
curl -X POST http://localhost:8080/api/v1/products \
  -H "Content-Type: application/json" \
  -d '{"ids": [38173346, 47894770, 12345678]}'
```

**Response:**
```json
{
  "success": true,
  "total": 3,
  "results": {
    "38173346": {
      "productId": 38173346,
      "product": { "..." }
    },
    "47894770": {
      "productId": 47894770,
      "product": { "..." }
    },
    "12345678": {
      "productId": 12345678,
      "error": "product 12345678 not found"
    }
  }
}
```

درخواست‌های batch به صورت **همزمان (concurrent)** ارسال می‌شوند — حتی اگر یک محصول خطا داشت، بقیه برگردانده می‌شوند.

---

## ساختار Response

### فیلدهای Product

| فیلد | نوع | توضیح |
|------|-----|-------|
| `productId` | int | شناسه محصول در ترندیول |
| `contentId` | int | شناسه URL (همان عدد انتهای لینک) |
| `name` | string | نام محصول |
| `brand` | string | برند |
| `url` | string | لینک مستقیم محصول |
| `price.original` | float | قیمت اصلی (TRY) |
| `price.discounted` | float | قیمت با تخفیف |
| `price.currency` | string | واحد ارز (`TRY`) |
| `inStock` | bool | آیا حداقل یک variant موجود است |
| `variants` | array | لیست سایزها/رنگ‌ها با قیمت و موجودی |
| `images` | array | تصاویر اصلی محصول از CDN (بدون آیکون و لوگو) |
| `fetchedAt` | datetime | زمان دریافت اطلاعات (UTC) |

### فیلدهای Variant

| فیلد | نوع | توضیح |
|------|-----|-------|
| `attributeName` | string | نوع صفت (مثال: `Beden`, `Renk`) |
| `attributeValue` | string | مقدار (مثال: `42`, `Beyaz`) |
| `inStock` | bool | موجودی این variant |
| `quantity` | int | تعداد موجود |
| `price` | float | قیمت اصلی این variant |
| `discountedPrice` | float | قیمت با تخفیف این variant |
| `barcode` | string | بارکد |
| `itemNumber` | int | شماره آیتم داخلی |

---

## کدهای خطا

| HTTP Status | توضیح |
|-------------|-------|
| `200` | موفق |
| `400` | ورودی نامعتبر (مثلاً ID اشتباه یا batch خالی) |
| `404` | محصول پیدا نشد |
| `502` | خطا در ارتباط با API ترندیول |

**نمونه خطا:**
```json
{
  "success": false,
  "error": "product 99999999 not found"
}
```

---

## نکات مهم

### IP ترکیه

`public.trendyol.com` ممکن است درخواست‌های خارج از ترکیه را block کند. در صورت بروز خطا:

- سرویس را روی سرور ترکیه deploy کنید
- یا از یک proxy ترکیه استفاده کنید و آدرس آن را از طریق متغیر محیطی `HTTP_PROXY` تنظیم کنید

### کش

نتایج موفق به مدت `CACHE_TTL_SECONDS` ثانیه (پیش‌فرض ۱۲۰) در حافظه نگه داشته می‌شوند تا درخواست‌های تکراری سریع‌تر پاسخ بگیرند و بار روی ترندیول کاهش یابد.

### Headless Browser

استراتژی سوم از **Chromium headless** استفاده می‌کند. این استراتژی:
- هنگامی فعال می‌شود که API و HTML مستقیم block شوند
- صفحه را کامل رندر کرده و داده را از `window.__PRODUCT_DETAIL_APP_INITIAL_STATE__` یا DOM می‌خواند
- کندتر است (تا ۳۵ ثانیه) اما داده کامل‌تری برمی‌گرداند
- نیاز به `shm_size: 256m` در Docker دارد

### Rate Limiting

- حداکثر `MAX_CONCURRENT` درخواست همزمان به ترندیول ارسال می‌شود
- در صورت خطا، تا `RETRY_COUNT` بار با تاخیر ۳۰۰ms تلاش مجدد انجام می‌شود
