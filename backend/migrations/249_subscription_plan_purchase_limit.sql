-- Per-user lifetime purchase limit; zero preserves unlimited purchases.
ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS purchase_limit INTEGER NOT NULL DEFAULT 0
    CHECK (purchase_limit >= 0);

CREATE INDEX IF NOT EXISTS payment_orders_user_plan_idx
    ON payment_orders (user_id, plan_id);
