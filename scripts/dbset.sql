DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS transfers;

CREATE TABLE accounts (
  id          BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  currency    VARCHAR(3) NOT NULL,
  balance     MONEY DEFAULT 0.00,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT current_timestamp
);

CREATE TABLE transfers (
  id          BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
  source_id   BIGINT REFERENCES accounts NOT NULL,
  dest_id     BIGINT REFERENCES accounts NOT NULL,
  amount      MONEY NOT NULL,
  currency    VARCHAR(3) NOT NULL,
  status      VARCHAR(16) NOT NULL
              CHECK (status IN ('pending', 'completed', 'failed'))
              DEFAULT 'pending',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT current_timestamp
);
