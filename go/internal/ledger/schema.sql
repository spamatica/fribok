CREATE TABLE IF NOT EXISTS company (
    id       INTEGER PRIMARY KEY,
    name     TEXT NOT NULL,
    org_nr   TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL DEFAULT 'SEK',
    kp_type  TEXT NOT NULL DEFAULT ''
);

-- idx follows SIE: 0 is the current year, -1 the previous one, and so on.
CREATE TABLE IF NOT EXISTS fiscal_year (
    id         INTEGER PRIMARY KEY,
    company_id INTEGER NOT NULL REFERENCES company(id) ON DELETE CASCADE,
    idx        INTEGER NOT NULL,
    start_date TEXT NOT NULL,
    end_date   TEXT NOT NULL,
    UNIQUE (company_id, idx)
);

CREATE TABLE IF NOT EXISTS account (
    company_id INTEGER NOT NULL REFERENCES company(id) ON DELETE CASCADE,
    number     TEXT NOT NULL,
    name       TEXT NOT NULL,
    -- T asset, S debt/equity, K cost, I income
    type       TEXT NOT NULL CHECK (type IN ('T','S','K','I')),
    PRIMARY KEY (company_id, number)
);

-- Opening balance per account and fiscal year, in öre (debit positive).
CREATE TABLE IF NOT EXISTS opening_balance (
    fiscal_year_id INTEGER NOT NULL REFERENCES fiscal_year(id) ON DELETE CASCADE,
    account        TEXT NOT NULL,
    amount         INTEGER NOT NULL,
    PRIMARY KEY (fiscal_year_id, account)
);

CREATE TABLE IF NOT EXISTS voucher (
    id             INTEGER PRIMARY KEY,
    company_id     INTEGER NOT NULL REFERENCES company(id) ON DELETE CASCADE,
    fiscal_year_id INTEGER NOT NULL REFERENCES fiscal_year(id) ON DELETE CASCADE,
    series         TEXT NOT NULL,
    number         INTEGER NOT NULL,
    date           TEXT NOT NULL,
    text           TEXT NOT NULL DEFAULT '',
    UNIQUE (fiscal_year_id, series, number)
);

-- amount in öre: debit positive, credit negative. A voucher's lines sum to 0.
CREATE TABLE IF NOT EXISTS voucher_line (
    id         INTEGER PRIMARY KEY,
    voucher_id INTEGER NOT NULL REFERENCES voucher(id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,
    account    TEXT NOT NULL,
    amount     INTEGER NOT NULL,
    text       TEXT NOT NULL DEFAULT '',
    UNIQUE (voucher_id, seq)
);

CREATE INDEX IF NOT EXISTS voucher_line_account ON voucher_line(account);
