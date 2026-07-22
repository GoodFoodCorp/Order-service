CREATE TABLE IF NOT EXISTS menu_items (
    id          UUID PRIMARY KEY,
    name        TEXT             NOT NULL,
    description TEXT             NOT NULL DEFAULT '',
    price_cents BIGINT           NOT NULL CHECK (price_cents > 0),
    category    TEXT             NOT NULL,
    emoji       TEXT             NOT NULL DEFAULT '',
    rating      DOUBLE PRECISION NOT NULL DEFAULT 0,
    available   BOOLEAN          NOT NULL DEFAULT TRUE,
    sort_order  INT              NOT NULL DEFAULT 0
);

-- Seed the initial catalog (idempotent). Prices in cents.
INSERT INTO menu_items (id, name, description, price_cents, category, emoji, rating, sort_order) VALUES
 ('11111111-1111-1111-1111-111111111111', 'Burger Deluxe',        'Steak haché, cheddar, bacon, oignons caramélisés', 1299, 'Burgers',  '🍔', 4.8, 1),
 ('11111111-1111-1111-1111-111111111112', 'Cheeseburger Classic', 'Steak, double cheddar, cornichons, sauce maison',  1099, 'Burgers',  '🍔', 4.6, 2),
 ('22222222-2222-2222-2222-222222222221', 'Pizza Margherita',     'Mozzarella, tomates fraîches, basilic',            1499, 'Pizzas',   '🍕', 4.9, 3),
 ('22222222-2222-2222-2222-222222222222', 'Pizza Regina',         'Jambon, champignons, mozzarella, olives',          1599, 'Pizzas',   '🍕', 4.7, 4),
 ('33333333-3333-3333-3333-333333333331', 'Salade César',         'Poulet grillé, parmesan, croûtons, sauce César',    999, 'Salades',  '🥗', 4.7, 5),
 ('33333333-3333-3333-3333-333333333332', 'Salade Chèvre Miel',   'Chèvre chaud, miel, noix, roquette',               1049, 'Salades',  '🥗', 4.5, 6),
 ('44444444-4444-4444-4444-444444444441', 'Fondant au Chocolat',  'Cœur coulant, glace vanille',                       699, 'Desserts', '🍫', 4.9, 7),
 ('44444444-4444-4444-4444-444444444442', 'Tiramisu Maison',      'Mascarpone, café, cacao',                           649, 'Desserts', '🍰', 4.6, 8)
ON CONFLICT (id) DO NOTHING;
