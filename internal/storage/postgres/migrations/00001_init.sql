-- +goose Up
CREATE TABLE IF NOT EXISTS links (
    code         TEXT        NOT NULL,
    original_url TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT links_pkey             PRIMARY KEY (code),
    CONSTRAINT links_original_url_key UNIQUE (original_url),
    CONSTRAINT links_code_format      CHECK (code ~ '^[A-Za-z0-9_]{10}$'),
    CONSTRAINT links_url_len          CHECK (char_length(original_url) BETWEEN 1 AND 2048)
);

-- +goose Down
DROP TABLE IF EXISTS links;