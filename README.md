# Go Ödeme Gateway

JWT ile korunan, işlemleri SQLite'a kaydeden küçük bir ödeme servisi.

## Çalıştırma

Gerekli ortam değişkenleri (`.env.example` dosyasına bakın):

| Değişken | Açıklama |
|---|---|
| `JWT_SECRET` | Token imzalama anahtarı, **en az 32 karakter**. Üretmek için: `openssl rand -base64 48` |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | Veritabanında hiç kullanıcı yoksa ilk açılışta bu admin hesabı oluşturulur |
| `DB_PATH` | SQLite dosyası (varsayılan `odeme_sistemi.db`) |
| `PORT` | Varsayılan `8080` |

```powershell
$env:JWT_SECRET = "<en-az-32-karakter-rastgele-anahtar>"
$env:ADMIN_EMAIL = "admin@ornek.com"
$env:ADMIN_PASSWORD = "<guclu-bir-sifre>"
go run .
```

Docker ile:

```powershell
docker build -t odeme-gateway .
docker run -p 8080:8080 -v odeme-data:/app/data -e JWT_SECRET=... -e ADMIN_EMAIL=... -e ADMIN_PASSWORD=... odeme-gateway
```

## API

| Metot | Yol | Yetki | Gövde |
|---|---|---|---|
| POST | `/api/v1/login` | — | `{"eposta","sifre"}` |
| POST | `/api/v1/pay` | JWT | `{"kart_no":"1111-2222","tutar":"250.50"}` |
| GET | `/api/v1/transactions` | JWT | — |
| POST | `/api/v1/cards/batch` | JWT + admin | `[{"kart_no":"1111-2222","bakiye":1500}]` |

Tutarlar JSON'da TL cinsinden ondalık sayıdır (en fazla 2 basamak) ve içeride kuruş olarak (`int64`) tutulur.
Bakiye yalnızca sunucuda tutulur; ödeme isteği bakiye gönderemez ve var olmayan bir kart için ödeme yapılamaz.

## Test

```powershell
go test -race ./...
```
