CREATE TABLE quotes (
    base       CHAR(3) NOT NULL,
    quote      CHAR(3) NOT NULL,
    price      NUMERIC NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (base, quote),
    CONSTRAINT no_self_pair CHECK (base <> quote)
);
