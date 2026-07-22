# Order Service

Microservice Go gérant le cycle de vie des commandes et le paiement Stripe
(mode test) pour Good Food 3.0.

## Architecture (Clean / Hexagonal)

```
internal/
├── domain/          # Entités (Order, OrderItem, Payment), transitions de statut,
│                    # erreurs métier typées, ports (interfaces repository/gateway)
├── application/     # Un fichier par cas d'usage (place_order, confirm_order, …),
│                    # règles d'autorisation par rôle — aucune dépendance HTTP/SQL
├── adapter/
│   ├── http/        # chi router, middlewares (auth JWT, request-id, logs), DTO
│   ├── postgres/    # Repositories pgx + migrations SQL embarquées (golang-migrate)
│   └── stripe/      # Client Stripe REST isolé derrière le port PaymentGateway
│                    # + FakeGateway (démo hors-ligne sans clé)
└── config/          # Config typée chargée depuis les variables d'environnement
```

Cycle de vie d'une commande :
`PLACED → PAYMENT_PENDING → CONFIRMED → IN_PREPARATION → READY_FOR_PICKUP → IN_DELIVERY → DELIVERED`
(`CANCELLED` possible jusqu'à `READY_FOR_PICKUP` inclus).

## Prérequis

- Docker (le service est autonome : il embarque sa propre base PostgreSQL)
- Le réseau Docker partagé : `docker network create microservices-net` (une fois)
- `auth-service` démarré (émission des JWT)

## Lancement

```bash
cp .env.example .env   # renseigner POSTGRES_PASSWORD et JWT_SECRET
                       # ⚠️ JWT_SECRET doit être IDENTIQUE à celui du auth-service
docker compose up -d --build

# Ou en dev hors docker (nécessite la DB du compose) :
go run ./cmd/main.go
```

Les migrations s'appliquent automatiquement au démarrage.

## Variables d'environnement

| Variable | Requis | Description |
|---|---|---|
| `PORT` | non (8082) | Port HTTP |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | oui | Credentials de la base dédiée `order-db` |
| `DATABASE_URL` | oui | `postgres://user:pass@localhost:5433/orders_db?sslmode=disable` (hors docker) |
| `JWT_SECRET` | oui | Secret HS256 **identique à celui du auth-service** (validation des tokens) |
| `STRIPE_SECRET_KEY` | non | Clé test `sk_test_…` ; vide → gateway fake (démo hors-ligne) |
| `LOG_LEVEL` | non (info) | `debug`, `info`, `warn`, `error` |

## Endpoints

Documentation interactive : `GET /docs` (Scalar) · spec brute : `/docs/openapi.yaml`

| Méthode | Route | Rôles |
|---|---|---|
| POST | `/api/orders` | user |
| GET | `/api/orders?customerId=` | user (soi-même), admin |
| GET | `/api/orders/ready-for-delivery` | livreur, admin |
| GET | `/api/orders/{id}` | propriétaire, manager (son resto), livreur, admin |
| POST | `/api/orders/{id}/payment-intent` | user (propriétaire) |
| POST | `/api/orders/{id}/confirm` | user (propriétaire), admin |
| PATCH | `/api/orders/{id}/status` | manager (son resto), admin ; livreur limité à IN_DELIVERY/DELIVERED |
| GET | `/healthz`, `/readyz` | public (probes K8s) |

Montants en **centimes** (`total_amount_cents`) pour éviter les flottants.

## Tests

```bash
go test ./internal/... -cover     # unitaires (domain 89%, application 82%)
go vet ./... && gofmt -l .        # analyse statique
golangci-lint run                 # lint complet (config .golangci.yml)
```

## Notes

- La confirmation de paiement se fait par appel direct après checkout (mode
  test) ; en production ce serait un webhook Stripe — voir `confirm_order.go`.
- Les prix viennent du client pour ce POC (catalogue statique côté web-app) ;
  un futur menu-service serait l'autorité tarifaire.
