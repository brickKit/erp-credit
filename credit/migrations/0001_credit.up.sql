-- Credit policies, limits with their exposure, and the credit reservation branches of protocol 04. The rules are
-- docs/design.md ("规则").

-- A legal entity's handling of customers without a limit in a currency; no row is 'block'.
CREATE TABLE credit_policy (
    legal_entity_id uuid PRIMARY KEY,
    without_limit   text NOT NULL CHECK (without_limit IN ('block', 'unlimited')),
    version         bigint NOT NULL DEFAULT 1,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- The authority of credit: every hold, confirm, cancel and consume locks this row first. amount null: the customer is
-- not under credit control (held and counted, never refused). Lowering the amount below what is used is allowed:
-- only later holds see it.
CREATE TABLE credit_limit (
    id                  uuid PRIMARY KEY,
    partner_id          uuid NOT NULL,
    legal_entity_id     uuid NOT NULL,
    currency            text NOT NULL,
    amount              numeric(19,4) CHECK (amount >= 0),
    state               text NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'suspended')),
    order_exposure      numeric(19,4) NOT NULL DEFAULT 0 CHECK (order_exposure >= 0),
    receivable_exposure numeric(19,4) NOT NULL DEFAULT 0 CHECK (receivable_exposure >= 0),
    version             bigint NOT NULL DEFAULT 1,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (partner_id, legal_entity_id, currency)
);
CREATE INDEX credit_limit_list ON credit_limit (partner_id, legal_entity_id, currency, id);

-- Reservation branches (protocol 04): the branch ID is the coordinator's; a tombstone is a 'cancelled' row with no
-- limit. content_digest makes "the same ID with other content" a conflict.
CREATE TABLE branch (
    id              uuid PRIMARY KEY,
    limit_id        uuid REFERENCES credit_limit (id),
    partner_id      uuid,
    legal_entity_id uuid,
    currency        text,
    amount          numeric(19,4) CHECK (amount > 0),
    consumed        numeric(19,4) NOT NULL DEFAULT 0 CHECK (consumed >= 0),
    ref_kind        text,
    ref_id          text,
    ref_revision    bigint,
    state           text NOT NULL CHECK (state IN ('held', 'committed', 'consumed', 'released', 'expired', 'rejected', 'cancelled')),
    reason          text,
    available       numeric(19,4),
    content_digest  text,
    hold_expires_at timestamptz,
    version         bigint NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (consumed <= amount)
);
CREATE INDEX branch_held ON branch (hold_expires_at) WHERE state = 'held';

-- One consume a consumption ID.
CREATE TABLE consumption (
    id             text PRIMARY KEY,
    branch_id      uuid NOT NULL REFERENCES branch (id),
    amount         numeric(19,4) NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
