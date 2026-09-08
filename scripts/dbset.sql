DROP TABLE IF EXISTS transfers;
DROP TABLE IF EXISTS accounts;

CREATE TABLE accounts (
  id          BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  currency    VARCHAR(3) NOT NULL,
  balance     NUMERIC(15,2) DEFAULT 0.00,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT current_timestamp
);

CREATE TABLE transfers (
  id          BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  source_id   BIGINT REFERENCES accounts NOT NULL,
  dest_id     BIGINT REFERENCES accounts NOT NULL,
  amount      NUMERIC(15,2) NOT NULL,
  currency    VARCHAR(3) NOT NULL,
  status      VARCHAR(16) NOT NULL
              CHECK (status IN ('pending', 'completed', 'failed'))
              DEFAULT 'pending',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT current_timestamp
);

-- Seed accounts
INSERT INTO accounts (currency, balance)
VALUES
  ('USD', 1500.00),
  ('USD', 850.50),
  ('EUR', 2300.00),
  ('EUR', 125.75),
  ('RUB', 150000.00),
  ('RUB', 42000.00);


-- Seed transfers
INSERT INTO transfers (
  source_id,
  dest_id,
  amount,
  currency,
  status
)
VALUES
  (1, 2, 100.00,   'USD', 'completed'),
  (2, 1, 50.00,    'USD', 'completed'),
  (3, 4, 250.00,   'EUR', 'completed'),
  (4, 3, 75.50,    'EUR', 'pending'),
  (5, 6, 10000.00, 'RUB', 'completed'),
  (6, 5, 5000.00,  'RUB', 'failed'),
  (1, 2, 25.00,    'USD', 'pending'),
  (3, 4, 10.00,    'EUR', 'failed');
