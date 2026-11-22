# Go Ödeme Gateway

[![CI](https://github.com/EnesCiftci007/go-odeme-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/EnesCiftci007/go-odeme-gateway/actions/workflows/ci.yml)

Kart bakiyesinden ödeme alan, iade yapan ve her kuruşu çift taraflı defterde izleyen bir ödeme servisi.
Go ile yazılmıştır; SQLite veya PostgreSQL ile çalışır, ön yüzü ikili dosyanın içine gömülüdür.

## Özellikler

- **Doğru para hesabı:** tutarlar kuruş cinsinden `int64` tutulur; `float` hiç kullanılmaz.
- **Eşzamanlılık güvenliği:** bakiye tek bir koşullu `UPDATE ... WHERE bakiye >= tutar` ile düşülür. Aynı anda gelen 100 ödeme kartı eksiye düşüremez (test edildi).
- **Idempotency:** `Idempotency-Key` başlığıyla tekrar gönderilen istek yeni ödeme oluşturmaz, ilk yanıt aynen döner.
- **Tam ve kısmi iade:** birden fazla kısmi iade yapılabilir; toplamları ödeme tutarını aşamaz.
- **Çift taraflı defter:** her para hareketi iki satırdır ve toplamı sıfırdır. `/admin/reconciliation` defterin dengede olduğunu ve her kart bakiyesinin defterle uyuştuğunu doğrular.
- **Webhook'lar (outbox deseni):** olaylar ödeme ile aynı transaction'da yazılır, HMAC-SHA256 ile imzalanır ve başarısız olursa üstel bekleme ile 8 kez tekrar denenir.
- **Yetkilendirme:** bcrypt şifreler, 1 saatlik JWT, admin/kullanıcı rolleri. Kullanıcılar yalnızca kendi kartlarını ve ödemelerini görür.
- **Güvenlik:** sıkı CSP, giriş denemesi sınırı (IP başına), istek boyutu sınırı, maskelenmiş kart numaraları.
- **Gözlemlenebilirlik:** Prometheus metrikleri (`/metrics`), yapılandırılmış loglar, `X-Request-ID`, `/healthz` ve `/readyz`.

## Hızlı başlangıç

```powershell
$env:JWT_SECRET = "<en-az-32-karakter-rastgele-anahtar>"
$env:ADMIN_EMAIL = "admin@ornek.com"
$env:ADMIN_PASSWORD = "<guclu-bir-sifre>"
go run ./cmd/server
```

Ardından <http://localhost:8080> adresini açın. İlk açılışta veritabanında kullanıcı yoksa admin hesabı oluşturulur.
Yönetim sekmesinden kullanıcı ekleyip onlara kart tanımlayabilirsiniz.

### Docker + PostgreSQL

```powershell
Copy-Item .env.example .env   # değerleri doldurun
docker compose up --build
```

## Ayarlar

Tümü ortam değişkenidir; ayrıntılar için [.env.example](.env.example).

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `JWT_SECRET` | — | **Zorunlu**, en az 32 karakter |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | — | Veritabanı boşsa ilk admin hesabı |
| `DB_DRIVER` | `sqlite` | `sqlite` veya `postgres` |
| `DATABASE_URL` | `odeme_sistemi.db` | SQLite dosyası veya PostgreSQL adresi |
| `PORT` | `8080` | |
| `WEBHOOK_URL`, `WEBHOOK_SECRET` | — | Ayarlıysa olaylar bu adrese gönderilir |
| `TRUST_PROXY` | `false` | Ters proxy arkasında istemci IP'sini `X-Forwarded-For`'dan okur |
| `LOG_FORMAT` | `text` | `text` veya `json` |

## API

Tam doküman: sunucu çalışırken [`/openapi.yaml`](web/openapi.yaml) (ör. <https://editor.swagger.io> ile açılabilir).

| Metot | Yol | Yetki |
|---|---|---|
| POST | `/api/v1/auth/login` | — |
| GET | `/api/v1/me` | oturum |
| GET | `/api/v1/cards` | oturum |
| POST | `/api/v1/cards/batch` | admin |
| POST | `/api/v1/payments` | oturum, `Idempotency-Key` önerilir |
| GET | `/api/v1/payments?limit=20&once=<imlec>` | oturum |
| GET | `/api/v1/payments/{id}` | oturum |
| POST | `/api/v1/payments/{id}/refunds` | oturum, `Idempotency-Key` önerilir |
| GET, POST | `/api/v1/users` | admin |
| GET | `/api/v1/admin/reconciliation` | admin |

Örnek ödeme:

```http
POST /api/v1/payments
Authorization: Bearer <token>
Idempotency-Key: 7f3c2a1e-...
Content-Type: application/json

{"kart_id": 1, "tutar": "250.50", "aciklama": "Kitap"}
```

Hatalar her zaman `{"basarili": false, "kod": "yetersiz_bakiye", "mesaj": "..."}` biçimindedir.

### Webhook imzasını doğrulama

Her istek `X-Odeme-Imza: t=<unix>,v1=<hex>` başlığı taşır. `v1`, `HMAC-SHA256(WEBHOOK_SECRET, "<t>.<gövde>")` değeridir.
Go ile alıcı tarafta: `webhook.Dogrula(gizli, r.Header.Get("X-Odeme-Imza"), govde, 5*time.Minute, time.Now())`.
Tekrarları ayıklamak için `X-Odeme-Olay-ID` başlığını kullanın.

## Proje yapısı

```
cmd/server        giriş noktası: ayarlar, bağlantılar, düzgün kapanış
internal/para     Kurus tipi (kuruş cinsinden tutar)
internal/veri     veritabanı modelleri ve bağlantı (SQLite / PostgreSQL)
internal/kimlik   kullanıcılar, bcrypt, JWT
internal/odeme    kartlar, ödeme, iade, defter, mutabakat
internal/webhook  imzalı olay teslimi ve tekrar deneme
internal/api      HTTP handler'ları, idempotency, istek sınırı, metrikler
internal/ayarlar  ortam değişkenleri ve doğrulama
web/              gömülü ön yüz ve OpenAPI dokümanı
```

## Test

```powershell
go test -race ./...
```

Testleri PostgreSQL üzerinde çalıştırmak için (her test kendi şemasını kullanır):

```powershell
$env:TEST_DATABASE_URL = "postgres://kullanici:sifre@localhost:5432/test?sslmode=disable"
go test -race ./...
```

CI, testleri hem SQLite hem PostgreSQL ile çalıştırır, golangci-lint uygular ve Docker imajını derleyip duman testi yapar.

## Bilinen sınırlar

- Webhook göndericisi tek bir sunucu örneği varsayar; çok örnekli kurulumda PostgreSQL `SELECT ... FOR UPDATE SKIP LOCKED` eklenmelidir.
- İstek sınırı bellek içindedir; birden fazla örnekte paylaşılmaz.
- Kart numaraları düz metin saklanır (yanıtlarda maskelenir). Gerçek kart verisi için PCI DSS gereği tokenizasyon/şifreleme gerekir.
