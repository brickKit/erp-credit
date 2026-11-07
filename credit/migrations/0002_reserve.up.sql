-- The branches' state moves to the SDK's participant runtime (be-protocol chapter 16, migration 0024): the state,
-- reason, content digest, hold period, version and times are be_reserve_branch's; this table keeps what a branch
-- takes (the limit, the customer, legal entity and currency, the amount and what was consumed, the reference, the
-- available credit when held). A tombstone has no row here. The consumptions' idempotency is be_reserve_consumption's
-- (the content digest is the amount); consumption keeps the amount.
INSERT INTO be_reserve_branch (id, kind, state, reason, content_digest, hold_expires_at, version, created_at, updated_at)
SELECT id, 'erp.credit.order', state, reason, content_digest, hold_expires_at, version, created_at, updated_at FROM branch;
INSERT INTO be_reserve_consumption (id, branch_id, content_digest, created_at)
SELECT id, branch_id, 'amount:' || trim_scale(amount)::text, created_at FROM consumption;
DELETE FROM branch WHERE limit_id IS NULL AND amount IS NULL AND state = 'cancelled';
DROP INDEX branch_held;
ALTER TABLE branch DROP COLUMN state, DROP COLUMN reason, DROP COLUMN content_digest, DROP COLUMN hold_expires_at,
    DROP COLUMN version, DROP COLUMN updated_at;
ALTER TABLE branch ADD CONSTRAINT branch_reserve FOREIGN KEY (id) REFERENCES be_reserve_branch (id);
