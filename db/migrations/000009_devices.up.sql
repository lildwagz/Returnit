-- Phones that receive push notifications. One row per push token. A token belongs to exactly one
-- user: when the same phone signs in as someone else, registering the token moves it (so the
-- previous user's reminders never reach that phone). Rows go away with the user (cascade), when
-- the user signs out in the app (DELETE /devices/{id}), or when the push service reports the token
-- as no longer valid.
CREATE TABLE devices (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform     VARCHAR(10) NOT NULL CHECK (platform IN ('android', 'ios')),
    push_token   TEXT NOT NULL UNIQUE CHECK (char_length(push_token) BETWEEN 20 AND 4096),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_devices_user ON devices (user_id, last_seen_at DESC);
