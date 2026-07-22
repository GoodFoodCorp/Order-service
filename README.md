# Order Service

Microservice **Go** gérant le **cycle de vie des commandes** : du panier validé
jusqu'à la livraison.

| | |
|---|---|
| **Langage / techno** | Go 1.26, chi (routeur), pgx (PostgreSQL), zerolog, golang-migrate |
| **Base de données** | PostgreSQL (port hôte `5433`) |
| **Port HTTP** | `8082` |
| **Documentation API** | http://localhost:8082/docs |

> ℹ️ **Ce service ne gère plus ni les menus ni les paiements.** Les menus sont
> passés dans **`menu-service`** (par restaurant) et les paiements dans
> **`payment-service`** (qui possède Stripe). Le contrat public est inchangé :
> les routes de paiement existent toujours ici, mais délèguent en interne.

---

## Architecture — Clean / Hexagonale

```
cmd/main.go                # Démarrage, migrations, injection des dépendances
internal/
├── domain/                # Order, OrderItem, statuts et transitions,
│                          # erreurs métier typées, ports (interfaces)
├── application/           # Un fichier par cas d'usage :
│                          #   place_order, get_order, list_customer_orders,
│                          #   list_restaurant_orders, create_payment_intent,
│                          #   confirm_order, update_order_status,
│                          #   list_ready_for_delivery
├── adapter/
│   ├── http/              # Routeur chi, middlewares (JWT, request-id, logs), DTO
│   ├── postgres/          # Repository pgx + migrations SQL embarquées
│   └── paymentclient/     # Client REST vers payment-service (JWT transmis)
└── config/                # Configuration typée depuis l'environnement
```

**Règle** : aucune logique métier dans les handlers. Les transitions de statut
sont la **source de vérité unique** du domaine.

---

## Fonctionnalités

- **Passer une commande** (panier → commande) — montants stockés en **centimes**,
  jamais en flottants
- **Consulter une commande** : le client propriétaire, le franchisé du restaurant,
  le livreur ou le siège
- **Historique des commandes** d'un client
- **Liste des commandes d'un restaurant** (portail franchisé)
- **Créer une intention de paiement** — délègue à `payment-service` en transmettant
  le JWT du client, puis passe la commande en `PAYMENT_PENDING`
- **Confirmer la commande** après vérification du paiement auprès de
  `payment-service` — idempotent
- **Changer le statut** : le franchisé sur son restaurant, le siège partout, le
  livreur uniquement pour `IN_DELIVERY` et `DELIVERED`
- **Lister les commandes prêtes à livrer** — consommé par `delivery-service`

### Cycle de vie d'une commande

```
PLACED → PAYMENT_PENDING → CONFIRMED → IN_PREPARATION
       → READY_FOR_PICKUP → IN_DELIVERY → DELIVERED
```

`CANCELLED` est possible jusqu'à `READY_FOR_PICKUP` inclus. Toute transition non
prévue est refusée (`409`) et l'état reste inchangé.

### Cloisonnement
- Un client ne voit que **ses** commandes
- Un franchisé n'agit que sur les commandes de **son** restaurant (`403` sinon)
- Un livreur ne peut déclencher que les deux transitions de livraison

---

## Endpoints

| Méthode | Route | Accès |
|---|---|---|
| POST | `/api/orders` | `user` |
| GET | `/api/orders?customerId=` | `user` (soi-même), `admin` |
| GET | `/api/orders?restaurantId=` | `manager` (le sien), `admin` |
| GET | `/api/orders/ready-for-delivery` | `livreur`, `admin` |
| GET | `/api/orders/{id}` | propriétaire, franchisé du resto, livreur, `admin` |
| POST | `/api/orders/{id}/payment-intent` | `user` (propriétaire) |
| POST | `/api/orders/{id}/confirm` | `user` (propriétaire), `admin` |
| PATCH | `/api/orders/{id}/status` | `manager` (le sien), `admin`, `livreur` (limité) |
| GET | `/healthz`, `/readyz` | public (sondes) |

---

## Lancement

```bash
docker network create microservices-net   # une seule fois, partagé
cp .env.example .env                      # renseigner POSTGRES_PASSWORD et JWT_SECRET
docker compose up -d --build
```

⚠️ `JWT_SECRET` doit être **identique** à celui de `auth-service`.
`payment-service` doit tourner pour que le paiement fonctionne.

### Variables d'environnement

| Variable | Requis | Description |
|---|---|---|
| `PORT` | non (8082) | Port HTTP |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | oui | Base dédiée `order-db` |
| `DATABASE_URL` | oui | Chaîne pgx (le compose la construit pour le conteneur) |
| `JWT_SECRET` | oui | Secret HS256 partagé avec `auth-service` |
| `PAYMENT_SERVICE_URL` | non | Défaut `http://payment-service:8086` |
| `LOG_LEVEL` | non (info) | `debug`, `info`, `warn`, `error` |

---

## Tests

```bash
go test ./internal/... -cover     # domaine ~89 %, application ~85 %
go vet ./... && gofmt -l .
golangci-lint run                 # configuration fournie (.golangci.yml)
```

Les tests utilisent des repositories et un faux `payment-service` en mémoire —
aucune base ni service externe requis.

> ⚠️ **Aucune CI n'est configurée sur ce projet** — les tests doivent être lancés
> manuellement.

---

## Notes

- Les prix proviennent du client (catalogue affiché par le front) pour ce POC ;
  `menu-service` fait autorité sur les tarifs et pourrait les revalider ici.
- L'idempotence du paiement est désormais garantie par `payment-service`.
