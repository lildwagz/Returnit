# Local development settings for PowerShell. Use it "dot-sourced" so the variables stay in your window:
#   . .\scripts\dev-env.ps1
#   go run ./cmd/web      (in one window)
#   go run ./cmd/worker   (in a second window, after running this script there too)
# Dev passwords only. Never use these values anywhere real.
$env:DATABASE_URL = "postgres://returnit:returnit_dev@localhost:5432/returnit?sslmode=disable"
$env:REDIS_ADDR = "localhost:6379"
$env:S3_ENDPOINT = "localhost:9000"
$env:S3_ACCESS_KEY = "returnit"
$env:S3_SECRET_KEY = "returnit_dev_secret"
$env:CLAMAV_ADDR = "localhost:3310"
$env:OIDC_ISSUER = "http://localhost:8180/realms/returnit"
$env:OIDC_AUDIENCE = "returnit-api"
# Worker: e-mail goes to Mailpit (http://localhost:8025), erased users are deleted at Keycloak.
$env:SMTP_ADDR = "localhost:1025"
$env:SMTP_FROM = "noreply@returnit.local"
$env:SMTP_TLS = "none"
$env:KEYCLOAK_URL = "http://localhost:8180"
$env:KEYCLOAK_REALM = "returnit"
$env:KEYCLOAK_ADMIN_CLIENT_ID = "returnit-worker"
$env:KEYCLOAK_ADMIN_CLIENT_SECRET = "dev-worker-secret"
Write-Host "ReturnIt dev environment set for this window."
