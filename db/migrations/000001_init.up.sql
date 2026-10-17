-- 000001_init.up.sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(254) NOT NULL UNIQUE,
    display_name VARCHAR(100),
    identity_subject VARCHAR(255) UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE purchases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    product_name VARCHAR(160) NOT NULL CHECK (length(trim(product_name)) BETWEEN 1 AND 160),
    store_name VARCHAR(160),
    purchase_date DATE NOT NULL,
    price NUMERIC(12,2) CHECK (price IS NULL OR price >= 0),
    currency CHAR(3) NOT NULL DEFAULT 'IDR',
    warranty_start_date DATE,
    warranty_end_date DATE,
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT warranty_date_order CHECK (
      warranty_start_date IS NULL OR warranty_end_date IS NULL OR warranty_end_date >= warranty_start_date
    )
);

CREATE INDEX idx_purchases_user_purchase_date ON purchases(user_id, purchase_date DESC);
CREATE INDEX idx_purchases_user_warranty_end ON purchases(user_id, warranty_end_date);

CREATE TABLE receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    purchase_id UUID NOT NULL UNIQUE REFERENCES purchases(id) ON DELETE CASCADE,
    storage_key VARCHAR(512) NOT NULL UNIQUE,
    file_name VARCHAR(255) NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE reminder_preferences (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT true,
    days_before JSONB NOT NULL DEFAULT '[30,14,7,3,1]'::jsonb,
    channel VARCHAR(20) NOT NULL DEFAULT 'push'
      CHECK (channel IN ('push', 'email', 'in_app')),
    quiet_hours_start TIME,
    quiet_hours_end TIME,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purchase_id UUID REFERENCES purchases(id) ON DELETE SET NULL,
    title VARCHAR(160) NOT NULL,
    body TEXT NOT NULL,
    scheduled_at TIMESTAMPTZ,
    sent_at TIMESTAMPTZ,
    read_at TIMESTAMPTZ,
    status VARCHAR(20) NOT NULL DEFAULT 'scheduled'
      CHECK (status IN ('scheduled', 'sent', 'failed', 'cancelled')),
    idempotency_key VARCHAR(255) UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_notifications_user_status_schedule
    ON notifications(user_id, status, scheduled_at);
