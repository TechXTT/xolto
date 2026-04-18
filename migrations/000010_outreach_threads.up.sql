-- XOL-24 C-5: outreach thread reply-time tracking.
-- Creates outreach_threads table with full state machine support.

CREATE TABLE outreach_threads (
  id                          BIGSERIAL PRIMARY KEY,
  user_id                     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  listing_id                  TEXT NOT NULL,
  marketplace_id              TEXT NOT NULL,
  mission_id                  BIGINT,
  draft_text                  TEXT NOT NULL,
  draft_shape                 TEXT NOT NULL,
  draft_lang                  TEXT NOT NULL,
  sent_at                     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  replied_at                  TIMESTAMPTZ,
  reply_text                  TEXT,
  state                       TEXT NOT NULL DEFAULT 'awaiting_reply',
  last_state_transition_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (user_id, listing_id, marketplace_id)
);

CREATE INDEX idx_outreach_threads_user_state ON outreach_threads (user_id, state);
CREATE INDEX idx_outreach_threads_mission ON outreach_threads (mission_id);
CREATE INDEX idx_outreach_threads_last_transition ON outreach_threads (last_state_transition_at);
