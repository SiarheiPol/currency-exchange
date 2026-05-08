CREATE TABLE quotes (
    currency   CHAR(3) PRIMARY KEY,
    price      NUMERIC NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
