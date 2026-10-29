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
│   ├── menuclient/        # Client REST vers menu-service (recettes, clé de service)
│   ├── stockclient/       # Client REST vers stock-service (check/consume/restore, clé de service)
│   ├── paymentclient/     # Client REST interne vers payment-service (X-Service-Key)
│   └── promoclient/       # promo-service : preview (JWT client), redeem/release (X-Service-Key)
└── config/                # Configuration typée depuis l'environnement
```

**Règle** : aucune logique métier dans les handlers. Les transitions de statut
sont la **source de vérité unique** du domaine.

---

## Fonctionnalités

- **Passer une commande** (panier → commande) — montants stockés en **centimes**,
  jamais en flottants
- **Code promo optionnel** — prévisualisé (jamais consommé) auprès de
  `promo-service` à la création, puis **réellement consommé** seulement après
  succès du paiement (dans `confirm`), pour ne jamais brûler une utilisation
  sur un panier abandonné
- **Livraison ou click & collect** (`fulfillment` = `DELIVERY` | `PICKUP`) :
  à la création, `franchise-service` donne un **devis** — restaurant ouvert,
  commandes non suspendues, adresse dans la zone — avec les **frais de
  livraison**, le **minimum de commande** et le **délai estimé**, enregistrés
  sur la commande. Refus explicite sinon (`400`). Un retrait n'a ni adresse ni
  frais et n'est jamais proposé aux livreurs.
- **Pourboire** (`tip_cents`, 0 à 100 €) ajouté au montant dû
- **Montant dû** = sous-total − remise + frais de livraison + pourboire
- **Création idempotente** : en-tête `Idempotency-Key` (unique par client) —
  un double clic ne crée qu'une commande
- **Consulter une commande** : le client propriétaire, le franchisé du
  restaurant, le **livreur assigné** ou le siège
- **Historique** d'un client et **commandes d'un restaurant**, paginés
  (`limit`/`offset`, 50 par défaut, 200 max, filtre `status=A,B`, en-tête
  `X-Next-Offset`)
- **Annulation par le client** (`POST /{id}/cancel`) tant que la cuisine n'a
  pas commencé ; toute annulation d'une commande payée est **remboursée**
- **Créer une intention de paiement** — via l'API interne de `payment-service`
  (`X-Service-Key`) pour **le montant calculé ici** ; le client choisit au plus
  une carte enregistrée. Passe la commande en `PAYMENT_PENDING`
- **Contrôle du stock à la commande** — les recettes des plats sont lues dans
  `menu-service`, puis les ingrédients cumulés sont vérifiés auprès de
  `stock-service` : une commande dont un ingrédient suivi manque est refusée
  (`409`, le message nomme le plat et l'ingrédient). Les plats sans recette ne
  sont pas contrôlés.
- **Confirmer la commande** : le paiement est relu chez `payment-service`
  (réconcilié avec Stripe) et doit être `SUCCEEDED` pour **exactement** le
  montant dû, le client et la devise de la commande. Déclenchée par le client
  ou par l'événement `payment.succeeded` (webhook Stripe) — idempotente ; les
  ingrédients sont alors **consommés**, et **restitués** si la commande payée
  est annulée
- **Changer le statut** : le franchisé sur son restaurant, le siège partout, le
  livreur **assigné** uniquement pour `IN_DELIVERY` et `DELIVERED`
- **API interne pour `delivery-service`** : commandes à attribuer, assignation
  exclusive du livreur (compare-and-set), transitions au nom du livreur

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
| GET | `/api/orders/ready-for-delivery` | `admin` |
| GET | `/api/orders/{id}` | propriétaire, franchisé du resto, livreur assigné, `admin` |
| POST | `/api/orders/{id}/payment-intent` | `user` (propriétaire) — `{payment_method_id?}` |
| POST | `/api/orders/{id}/confirm` | `user` (propriétaire), `admin` |
| POST | `/api/orders/{id}/cancel` | `user` (propriétaire, avant préparation) |
| PATCH | `/api/orders/{id}/status` | `manager` (le sien), `admin`, livreur assigné (limité) |
| GET | `/internal/orders/ready-for-delivery` | `X-Service-Key` (delivery-service) |
| GET | `/internal/orders/{id}` | `X-Service-Key` |
| PUT | `/internal/orders/{id}/courier` | `X-Service-Key` — `{courier_id}` |
| DELETE | `/internal/orders/{id}/courier/{courierId}` | `X-Service-Key` |
| PATCH | `/internal/orders/{id}/status` | `X-Service-Key` — `{status, courier_id}` |
| GET | `/healthz`, `/readyz`, `/metrics` | sondes et Prometheus (hors ingress pour `/metrics`) |

### Événements (RabbitMQ `goodfood.events`, outbox transactionnelle)

Chaque changement de cycle de vie publie `order.<statut>` (`order.confirmed`,
`order.in_preparation`, `order.ready_for_pickup`, `order.in_delivery`,
`order.delivered`, `order.cancelled`), écrit **dans la même transaction** que
le changement puis relayé avec confirmations du broker (au moins une fois,
`FOR UPDATE SKIP LOCKED`). Données : `orderId`, `customerId`, `restaurantId`,
`status`, `deliveryAddress`, `amountDueCents`, `courierId`, `fulfillment`,
`tipCents`, `dropoffLat/Lng`.

Consommé : `payment.succeeded` (file `order-service.payment-events`, DLQ
`.dlq`) — confirme la commande ; échec transitoire = relivraison.

### Résilience

Les appels sortants passent par un client commun : timeout par cible, pool de
connexions, `X-Request-ID` propagé, **circuit breaker** (ouvert après 5 échecs
consécutifs, 15 s). Le worker de reprise (stock + remboursements) prend un
**verrou consultatif PostgreSQL** : un seul réplica l'exécute à la fois.

---

## Dépendances

> **Légende** — 🔴 indispensable (le service ne démarre pas ou ne sert à rien) ·
> 🟠 nécessaire à une fonctionnalité (le reste continue de marcher) ·
> 🟡 optionnelle (dégradation silencieuse, journalisée)

| Dépendance | Type | Conséquence si absente |
|---|---|---|
| **PostgreSQL** (`order-db`) | 🔴 | Le service ne démarre pas |
| **payment-service** | 🟠 | **Le paiement est impossible** : `payment-intent` et `confirm` renvoient `502`. Le reste fonctionne : création de commande, consultation, historique, changements de statut par le franchisé. Une commande reste bloquée en `PLACED`. |
| **menu-service** | 🔴 | **Aucune commande ne peut être passée** : la lecture des recettes (`POST /internal/recipes/lookup`) a lieu à chaque `POST /api/orders`, qui renvoie `503` (message générique) si elle échoue. Consultation, paiement et changements de statut des commandes existantes continuent ; la consommation de stock d'une commande confirmée est différée (reprise par le worker). |
| **stock-service** | 🟠 | **Les commandes contenant un plat avec recette sont refusées** (`503`, message générique) car le stock ne peut pas être vérifié (`/internal/stocks/check`) ; les commandes sans plat suivi passent. La consommation (`consume`) à la confirmation et la restitution (`restore`) à l'annulation sont différées et rejouées par le worker. |
| **promo-service** | 🟡 | Un `promo_code` envoyé à la création est refusé (`400`) ; une commande sans code fonctionne normalement. Une consommation échouée après paiement est journalisée sans bloquer la confirmation. |
| **franchise-service** | 🟠 | Si `FRANCHISE_SERVICE_URL` est défini et que le service est injoignable, les nouvelles commandes sont refusées (`503`) : les frais de livraison font partie du prix. |
| **RabbitMQ** | 🟡 | Les événements restent dans l'outbox ; la confirmation repose sur le parcours client |
| **auth-service** | 🟠 | Aucun appel réseau, mais toutes les routes exigent un jeton valide |

### Qui dépend de ce service

| Service | Type | Conséquence si `order-service` est arrêté |
|---|---|---|
| `web-app` | 🔴 | Commandes et checkout inutilisables |
| `delivery-service` | 🟡 | La synchronisation des courses échoue (journalisée) ; les livraisons **déjà créées** restent utilisables, mais les changements de statut ne remontent plus aux commandes |

**Sans lien** : `reservation-service`, `user-service` et `franchise-service`
fonctionnent indépendamment.

### Remboursements (worker)

Une annulation à partir de `PAYMENT_PENDING` écrit `refund_sync = PENDING` dans
la même requête ; le remboursement (ou la fermeture de l'intention non payée)
est demandé à `payment-service`, puis l'utilisation du code promo est rendue.
En cas d'échec, le worker rejoue le remboursement à chaque passage.

### Synchronisation du stock (worker)

L'intention de stock est persistée avec la commande (colonne `stock_sync`) :
`CONSUME_PENDING` est écrit **dans la même requête** que le passage en
`CONFIRMED`, `RESTORE_PENDING` dans celle du passage en `CANCELLED` (pour une
commande payée). Si `stock-service` ou `menu-service` est indisponible, le
travail reste en attente et un **worker en arrière-plan** le rejoue **toutes
les 30 s** (premier passage 10 s après le démarrage), **50 commandes au plus
par passage**, les plus anciennes d'abord. `consume` et `restore` sont
idempotents côté `stock-service` (par identifiant de commande) ; une
annulation arrivée avant la consommation laisse une « pierre tombale » qui
rend la consommation tardive sans effet.

Les écritures de statut sont gardées par le statut lu (une écriture concurrente
renvoie `409`, à réessayer) : une confirmation tardive ne peut pas réactiver
une commande annulée, et une annulation restitue toujours le stock d'une
commande payée, même si la confirmation a eu lieu entre-temps.

---

## Lancement

```bash
docker network create microservices-net   # une seule fois, partagé
cp .env.example .env                      # renseigner POSTGRES_PASSWORD, JWT_SECRET et SERVICE_API_KEY
docker compose up -d --build
```

⚠️ `JWT_SECRET` doit être **identique** à celui de `auth-service`.
⚠️ `SERVICE_API_KEY` est **obligatoire** (le service refuse de démarrer sans) et
doit être **identique** à celui de `stock-service` et `menu-service`.

**Ordre de déploiement** : 1) les secrets (`SERVICE_API_KEY` partagé,
`JWT_SECRET`) ; 2) `stock-service` et `menu-service` (leurs routes
`/internal/...` doivent exister) ; 3) `order-service` ; 4) `web-app`
(Front-web).
`payment-service` doit tourner pour que le paiement fonctionne ; `promo-service`
uniquement si un code promo est utilisé.

### Variables d'environnement

| Variable | Requis | Description |
|---|---|---|
| `PORT` | non (8082) | Port HTTP |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | oui | Base dédiée `order-db` |
| `DATABASE_URL` | oui | Chaîne pgx (le compose la construit pour le conteneur) |
| `JWT_SECRET` | oui | Secret HS256 partagé avec `auth-service` |
| `JWT_ISSUER` / `JWT_AUDIENCE` | non | Défauts `goodfood-auth` / `goodfood-api` |
| `PAYMENT_SERVICE_URL` | non | Défaut `http://payment-service:8086` |
| `PROMO_SERVICE_URL` | non | Défaut `http://promo-service:8092` |
| `FRANCHISE_SERVICE_URL` | non | Active horaires, zone et frais (devis interne) |
| `RABBITMQ_URL` | non | Relais de l'outbox et consommation de `payment.succeeded` |
| `STOCK_SERVICE_URL` | non | Défaut `http://stock-service:8083` |
| `MENU_SERVICE_URL` | non | Défaut `http://menu-service:8085` |
| `SERVICE_API_KEY` | oui | Secret des appels internes (en-tête `X-Service-Key`), **identique** à celui de `stock-service` et `menu-service` ; jamais loggé |
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
