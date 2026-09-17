-- 000001_initial_schema: the whole MVP schema.
--
-- This SQL runs unmodified on PostgreSQL and on SQLite, which is what lets the
-- integration tests and local development work with no infrastructure. Keeping
-- one file instead of a directory per dialect means there is exactly one
-- description of the schema and nothing to keep in step.
--
-- The portable subset these migrations stay inside:
--
--   * types: VARCHAR(n), TEXT, BIGINT, INTEGER, BOOLEAN, DOUBLE PRECISION,
--     TIMESTAMP. No timestamptz, no uuid, no jsonb, no serial, no enums.
--   * identifiers are VARCHAR(36) text uuids, dates are VARCHAR(10) as
--     YYYY-MM-DD, money is BIGINT minor units, JSON blobs are TEXT.
--   * timestamps are written by the application in UTC; no DEFAULT now().
--   * CHECK constraints, partial indexes and inline REFERENCES only; no
--     dialect-specific DDL such as FILTER, INCLUDE or CONCURRENTLY.
--
-- The trip's timezone is a column of its own: every instant is stored in UTC
-- and converted for display at the edges.

-- ---------------------------------------------------------------- identity --

CREATE TABLE users (
    id            VARCHAR(36) PRIMARY KEY,
    telegram_id   BIGINT      NOT NULL UNIQUE,
    username      VARCHAR(64) NOT NULL DEFAULT '',
    first_name    VARCHAR(128) NOT NULL DEFAULT '',
    last_name     VARCHAR(128) NOT NULL DEFAULT '',
    language_code VARCHAR(16) NOT NULL DEFAULT '',
    photo_url     VARCHAR(512) NOT NULL DEFAULT '',
    is_premium    BOOLEAN     NOT NULL DEFAULT FALSE,
    -- The private chat the bot can reach this user in. NULL until they have
    -- talked to the bot, which is why a notification can be skipped rather
    -- than failed.
    chat_id       BIGINT,
    created_at    TIMESTAMP   NOT NULL,
    updated_at    TIMESTAMP   NOT NULL
);

CREATE INDEX idx_users_chat_id ON users (chat_id);

-- ------------------------------------------------------------------- trips --

CREATE TABLE trips (
    id          VARCHAR(36)  PRIMARY KEY,
    title       VARCHAR(120) NOT NULL,
    description VARCHAR(2000) NOT NULL DEFAULT '',
    start_date  VARCHAR(10)  NOT NULL,
    end_date    VARCHAR(10)  NOT NULL,
    timezone    VARCHAR(64)  NOT NULL DEFAULT 'UTC',
    currency    VARCHAR(3)   NOT NULL DEFAULT 'EUR',
    owner_id    VARCHAR(36)  NOT NULL REFERENCES users (id),
    status      VARCHAR(16)  NOT NULL DEFAULT 'planning'
                CHECK (status IN ('planning', 'active', 'completed', 'archived')),
    created_at  TIMESTAMP    NOT NULL,
    updated_at  TIMESTAMP    NOT NULL,
    CONSTRAINT trips_dates_ordered CHECK (end_date >= start_date)
);

CREATE INDEX idx_trips_owner_id ON trips (owner_id);
CREATE INDEX idx_trips_status ON trips (status);
CREATE INDEX idx_trips_start_date ON trips (start_date);

CREATE TABLE trip_members (
    id           VARCHAR(36) PRIMARY KEY,
    trip_id      VARCHAR(36) NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    user_id      VARCHAR(36) NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Editable per trip: the group knows each other by nicknames, not by
    -- Telegram profiles.
    display_name VARCHAR(60) NOT NULL,
    role         VARCHAR(16) NOT NULL DEFAULT 'member'
                 CHECK (role IN ('owner', 'admin', 'member')),
    status       VARCHAR(16) NOT NULL DEFAULT 'active'
                 CHECK (status IN ('invited', 'active', 'declined', 'removed')),
    joined_at    TIMESTAMP,
    created_at   TIMESTAMP   NOT NULL,
    updated_at   TIMESTAMP   NOT NULL,
    CONSTRAINT idx_trip_members_user UNIQUE (trip_id, user_id)
);

CREATE INDEX idx_trip_members_trip_id ON trip_members (trip_id);
CREATE INDEX idx_trip_members_user_id ON trip_members (user_id);

-- Exactly one owner per trip. Ownership transfer swaps both rows inside one
-- transaction, so this never trips in normal operation: it is here to make an
-- ownerless or two-owner trip impossible.
CREATE UNIQUE INDEX idx_trip_members_single_owner
    ON trip_members (trip_id) WHERE role = 'owner';

CREATE TABLE trip_invites (
    id         VARCHAR(36) PRIMARY KEY,
    trip_id    VARCHAR(36) NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    -- The token, never the trip id, is what travels in a deep link.
    token      VARCHAR(64) NOT NULL UNIQUE,
    created_by VARCHAR(36) NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       VARCHAR(16) NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    label      VARCHAR(60) NOT NULL DEFAULT '',
    -- 0 means unlimited, the right default for a link dropped in a group chat.
    max_uses   INTEGER     NOT NULL DEFAULT 0 CHECK (max_uses >= 0),
    uses       INTEGER     NOT NULL DEFAULT 0 CHECK (uses >= 0),
    expires_at TIMESTAMP,
    revoked_at TIMESTAMP,
    created_at TIMESTAMP   NOT NULL
);

CREATE INDEX idx_trip_invites_trip_id ON trip_invites (trip_id);

-- ------------------------------------------------------------------ events --

CREATE TABLE events (
    id            VARCHAR(36)  PRIMARY KEY,
    trip_id       VARCHAR(36)  NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    title         VARCHAR(120) NOT NULL,
    description   VARCHAR(2000) NOT NULL DEFAULT '',
    start_at      TIMESTAMP    NOT NULL,
    end_at        TIMESTAMP,
    location_name VARCHAR(200) NOT NULL DEFAULT '',
    latitude      DOUBLE PRECISION,
    longitude     DOUBLE PRECISION,
    type          VARCHAR(20)  NOT NULL DEFAULT 'custom'
                  CHECK (type IN ('departure', 'arrival', 'accommodation', 'activity',
                                  'meal', 'transport', 'race', 'custom')),
    created_by    VARCHAR(36)  NOT NULL REFERENCES users (id),
    created_at    TIMESTAMP    NOT NULL,
    updated_at    TIMESTAMP    NOT NULL,
    CONSTRAINT events_range_ordered CHECK (end_at IS NULL OR end_at >= start_at)
);

CREATE INDEX idx_events_trip_start ON events (trip_id, start_at);

CREATE TABLE event_participants (
    event_id     VARCHAR(36) NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    member_id    VARCHAR(36) NOT NULL REFERENCES trip_members (id) ON DELETE CASCADE,
    status       VARCHAR(16) NOT NULL DEFAULT 'undecided'
                 CHECK (status IN ('attending', 'not_attending', 'maybe', 'undecided')),
    responded_at TIMESTAMP,
    created_at   TIMESTAMP   NOT NULL,
    PRIMARY KEY (event_id, member_id)
);

CREATE INDEX idx_event_participants_member ON event_participants (member_id);

-- --------------------------------------------------------------- decisions --

CREATE TABLE decisions (
    id                 VARCHAR(36)  PRIMARY KEY,
    trip_id            VARCHAR(36)  NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    title              VARCHAR(160) NOT NULL,
    description        VARCHAR(2000) NOT NULL DEFAULT '',
    deadline           TIMESTAMP,
    status             VARCHAR(16)  NOT NULL DEFAULT 'open'
                       CHECK (status IN ('open', 'closed', 'resolved', 'cancelled')),
    -- The winning option is never enforced automatically; an organiser states
    -- the call and that is what lands here.
    resolved_option_id VARCHAR(36),
    resolution_note    VARCHAR(500) NOT NULL DEFAULT '',
    created_by         VARCHAR(36)  NOT NULL REFERENCES users (id),
    created_at         TIMESTAMP    NOT NULL,
    updated_at         TIMESTAMP    NOT NULL,
    closed_at          TIMESTAMP,
    resolved_at        TIMESTAMP
);

CREATE INDEX idx_decisions_trip ON decisions (trip_id, status);
CREATE INDEX idx_decisions_deadline ON decisions (deadline);

CREATE TABLE decision_options (
    id          VARCHAR(36)  PRIMARY KEY,
    decision_id VARCHAR(36)  NOT NULL REFERENCES decisions (id) ON DELETE CASCADE,
    label       VARCHAR(120) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    position    INTEGER      NOT NULL DEFAULT 0
);

CREATE INDEX idx_decision_options ON decision_options (decision_id, position);

-- One vote per participant per decision: the primary key is the rule.
CREATE TABLE decision_votes (
    decision_id VARCHAR(36) NOT NULL REFERENCES decisions (id) ON DELETE CASCADE,
    member_id   VARCHAR(36) NOT NULL REFERENCES trip_members (id) ON DELETE CASCADE,
    option_id   VARCHAR(36) NOT NULL REFERENCES decision_options (id) ON DELETE CASCADE,
    created_at  TIMESTAMP   NOT NULL,
    updated_at  TIMESTAMP   NOT NULL,
    PRIMARY KEY (decision_id, member_id)
);

CREATE INDEX idx_decision_votes_option ON decision_votes (option_id);

-- --------------------------------------------------------------- logistics --

CREATE TABLE vehicles (
    id               VARCHAR(36) PRIMARY KEY,
    trip_id          VARCHAR(36) NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    name             VARCHAR(80) NOT NULL,
    type             VARCHAR(16) NOT NULL DEFAULT 'car'
                     CHECK (type IN ('car', 'van', 'train', 'bus', 'other')),
    capacity         INTEGER     NOT NULL CHECK (capacity >= 1),
    -- The driver occupies a seat; that rule lives in the domain, not here.
    driver_member_id VARCHAR(36) REFERENCES trip_members (id) ON DELETE SET NULL,
    notes            VARCHAR(500) NOT NULL DEFAULT '',
    created_at       TIMESTAMP   NOT NULL,
    updated_at       TIMESTAMP   NOT NULL
);

CREATE INDEX idx_vehicles_trip_id ON vehicles (trip_id);

CREATE TABLE vehicle_passengers (
    vehicle_id VARCHAR(36) NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    member_id  VARCHAR(36) NOT NULL REFERENCES trip_members (id) ON DELETE CASCADE,
    seat_note  VARCHAR(80) NOT NULL DEFAULT '',
    created_at TIMESTAMP   NOT NULL,
    PRIMARY KEY (vehicle_id, member_id)
);

CREATE INDEX idx_vehicle_passengers_member ON vehicle_passengers (member_id);

CREATE TABLE accommodations (
    id         VARCHAR(36)  PRIMARY KEY,
    trip_id    VARCHAR(36)  NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    name       VARCHAR(120) NOT NULL,
    address    VARCHAR(300) NOT NULL DEFAULT '',
    -- An arbitrary link to wherever the group actually booked. There is no
    -- booking-provider integration and none is planned.
    url        VARCHAR(512) NOT NULL DEFAULT '',
    check_in   TIMESTAMP,
    check_out  TIMESTAMP,
    -- 0 means "not tracked".
    capacity   INTEGER      NOT NULL DEFAULT 0 CHECK (capacity >= 0),
    notes      VARCHAR(1000) NOT NULL DEFAULT '',
    created_at TIMESTAMP    NOT NULL,
    updated_at TIMESTAMP    NOT NULL,
    CONSTRAINT accommodations_range_ordered
        CHECK (check_out IS NULL OR check_in IS NULL OR check_out >= check_in)
);

CREATE INDEX idx_accommodations_trip_id ON accommodations (trip_id);

CREATE TABLE accommodation_guests (
    accommodation_id VARCHAR(36) NOT NULL REFERENCES accommodations (id) ON DELETE CASCADE,
    member_id        VARCHAR(36) NOT NULL REFERENCES trip_members (id) ON DELETE CASCADE,
    status           VARCHAR(16) NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'confirmed', 'declined')),
    created_at       TIMESTAMP   NOT NULL,
    PRIMARY KEY (accommodation_id, member_id)
);

CREATE INDEX idx_accommodation_guests_member ON accommodation_guests (member_id);

-- -------------------------------------------------------------- checklists --

CREATE TABLE checklists (
    id              VARCHAR(36)  PRIMARY KEY,
    trip_id         VARCHAR(36)  NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    title           VARCHAR(120) NOT NULL,
    description     VARCHAR(1000) NOT NULL DEFAULT '',
    -- 'shared'   : one list the whole group works through
    -- 'personal' : belongs to owner_member_id and is private to them
    scope           VARCHAR(16)  NOT NULL DEFAULT 'shared'
                    CHECK (scope IN ('shared', 'personal')),
    owner_member_id VARCHAR(36)  REFERENCES trip_members (id) ON DELETE CASCADE,
    position        INTEGER      NOT NULL DEFAULT 0,
    created_by      VARCHAR(36)  NOT NULL REFERENCES users (id),
    created_at      TIMESTAMP    NOT NULL,
    updated_at      TIMESTAMP    NOT NULL,
    CONSTRAINT checklists_personal_has_owner
        CHECK ((scope = 'personal' AND owner_member_id IS NOT NULL)
            OR (scope = 'shared' AND owner_member_id IS NULL))
);

CREATE INDEX idx_checklists_trip ON checklists (trip_id, position);

CREATE TABLE checklist_items (
    id           VARCHAR(36)  PRIMARY KEY,
    checklist_id VARCHAR(36)  NOT NULL REFERENCES checklists (id) ON DELETE CASCADE,
    title        VARCHAR(160) NOT NULL,
    notes        VARCHAR(500) NOT NULL DEFAULT '',
    completed    BOOLEAN      NOT NULL DEFAULT FALSE,
    completed_by VARCHAR(36)  REFERENCES trip_members (id) ON DELETE SET NULL,
    completed_at TIMESTAMP,
    assigned_to  VARCHAR(36)  REFERENCES trip_members (id) ON DELETE SET NULL,
    due_at       TIMESTAMP,
    position     INTEGER      NOT NULL DEFAULT 0,
    created_at   TIMESTAMP    NOT NULL,
    updated_at   TIMESTAMP    NOT NULL
);

CREATE INDEX idx_checklist_items ON checklist_items (checklist_id, position);
CREATE INDEX idx_checklist_items_open_assignee
    ON checklist_items (assigned_to) WHERE completed = FALSE;

-- ---------------------------------------------------------------- expenses --

CREATE TABLE expenses (
    id           VARCHAR(36)  PRIMARY KEY,
    trip_id      VARCHAR(36)  NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    title        VARCHAR(120) NOT NULL,
    amount_minor BIGINT       NOT NULL CHECK (amount_minor > 0),
    currency     VARCHAR(3)   NOT NULL,
    category     VARCHAR(20)  NOT NULL DEFAULT 'other'
                 CHECK (category IN ('accommodation', 'transport', 'fuel', 'food',
                                     'parking', 'registration', 'equipment', 'other')),
    paid_by      VARCHAR(36)  NOT NULL REFERENCES trip_members (id),
    split_type   VARCHAR(20)  NOT NULL DEFAULT 'equal'
                 CHECK (split_type IN ('equal', 'custom_amount', 'percentage')),
    spent_at     TIMESTAMP    NOT NULL,
    notes        VARCHAR(1000) NOT NULL DEFAULT '',
    created_by   VARCHAR(36)  NOT NULL REFERENCES users (id),
    created_at   TIMESTAMP    NOT NULL,
    updated_at   TIMESTAMP    NOT NULL
);

CREATE INDEX idx_expenses_trip ON expenses (trip_id, spent_at);

-- One row per participant of an expense. The rows always sum to
-- expenses.amount_minor whatever the split type, which is what makes the
-- balance query a plain SUM.
CREATE TABLE expense_participants (
    expense_id  VARCHAR(36) NOT NULL REFERENCES expenses (id) ON DELETE CASCADE,
    member_id   VARCHAR(36) NOT NULL REFERENCES trip_members (id) ON DELETE CASCADE,
    share_minor BIGINT      NOT NULL CHECK (share_minor >= 0),
    -- The input the share was derived from: minor units for a custom split,
    -- basis points for a percentage split, NULL for an equal one.
    weight      BIGINT,
    PRIMARY KEY (expense_id, member_id)
);

CREATE INDEX idx_expense_participants_member ON expense_participants (member_id);

-- A transfer between two members. Only 'settled' rows move a balance;
-- 'pending' rows are plans. TripOps records money, it never moves it.
CREATE TABLE settlements (
    id             VARCHAR(36) PRIMARY KEY,
    trip_id        VARCHAR(36) NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    from_member_id VARCHAR(36) NOT NULL REFERENCES trip_members (id) ON DELETE CASCADE,
    to_member_id   VARCHAR(36) NOT NULL REFERENCES trip_members (id) ON DELETE CASCADE,
    amount_minor   BIGINT      NOT NULL CHECK (amount_minor > 0),
    currency       VARCHAR(3)  NOT NULL,
    status         VARCHAR(16) NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'settled', 'cancelled')),
    note           VARCHAR(200) NOT NULL DEFAULT '',
    created_by     VARCHAR(36) NOT NULL REFERENCES users (id),
    created_at     TIMESTAMP   NOT NULL,
    settled_at     TIMESTAMP,
    settled_by     VARCHAR(36) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT settlements_distinct_parties CHECK (from_member_id <> to_member_id)
);

CREATE INDEX idx_settlements_trip ON settlements (trip_id, status);

-- ------------------------------------------------------------- attachments --

CREATE TABLE attachments (
    id             VARCHAR(36) PRIMARY KEY,
    trip_id        VARCHAR(36) NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    -- Polymorphic owner, kept as (type, id): attachments are read by owner and
    -- never joined across owners.
    owner_type     VARCHAR(24) NOT NULL
                   CHECK (owner_type IN ('trip', 'event', 'expense', 'accommodation', 'checklist_item')),
    owner_id       VARCHAR(36) NOT NULL,
    kind           VARCHAR(16) NOT NULL CHECK (kind IN ('telegram_file', 'url')),
    -- Telegram keeps the bytes; we only store the handles it gives us.
    file_id        VARCHAR(256) NOT NULL DEFAULT '',
    file_unique_id VARCHAR(128) NOT NULL DEFAULT '',
    url            VARCHAR(1024) NOT NULL DEFAULT '',
    file_name      VARCHAR(200) NOT NULL DEFAULT '',
    mime_type      VARCHAR(128) NOT NULL DEFAULT '',
    size_bytes     BIGINT      NOT NULL DEFAULT 0,
    caption        VARCHAR(300) NOT NULL DEFAULT '',
    uploaded_by    VARCHAR(36) NOT NULL REFERENCES users (id),
    created_at     TIMESTAMP   NOT NULL,
    CONSTRAINT attachments_payload_present
        CHECK ((kind = 'telegram_file' AND file_id <> '') OR (kind = 'url' AND url <> ''))
);

CREATE INDEX idx_attachments_owner ON attachments (owner_type, owner_id);
CREATE INDEX idx_attachments_trip_id ON attachments (trip_id);

-- ----------------------------------------------------------- notifications --

CREATE TABLE notification_preferences (
    user_id      VARCHAR(36) PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    trip_updates BOOLEAN     NOT NULL DEFAULT TRUE,
    decisions    BOOLEAN     NOT NULL DEFAULT TRUE,
    reminders    BOOLEAN     NOT NULL DEFAULT TRUE,
    checklist    BOOLEAN     NOT NULL DEFAULT TRUE,
    expenses     BOOLEAN     NOT NULL DEFAULT FALSE,
    updated_at   TIMESTAMP   NOT NULL
);

-- The outbox. Domain code enqueues rows; the Telegram worker drains them. The
-- unique dedupe_key is what makes every scheduled reminder idempotent, and so
-- what lets the scheduler run on a dumb interval without spamming anybody.
CREATE TABLE notifications (
    id           VARCHAR(36)  PRIMARY KEY,
    trip_id      VARCHAR(36)  REFERENCES trips (id) ON DELETE CASCADE,
    user_id      VARCHAR(36)  NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category     VARCHAR(32)  NOT NULL
                 CHECK (category IN ('trip_updates', 'decisions', 'reminders', 'checklist', 'expenses')),
    title        VARCHAR(200) NOT NULL DEFAULT '',
    body         VARCHAR(2000) NOT NULL,
    payload      TEXT,
    dedupe_key   VARCHAR(200) UNIQUE,
    status       VARCHAR(16)  NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'sending', 'sent', 'failed', 'skipped')),
    scheduled_at TIMESTAMP    NOT NULL,
    sent_at      TIMESTAMP,
    claimed_at   TIMESTAMP,
    attempts     INTEGER      NOT NULL DEFAULT 0,
    last_error   VARCHAR(500) NOT NULL DEFAULT '',
    created_at   TIMESTAMP    NOT NULL
);

CREATE INDEX idx_notifications_due ON notifications (status, scheduled_at);
CREATE INDEX idx_notifications_user ON notifications (user_id);
CREATE INDEX idx_notifications_trip_id ON notifications (trip_id);
CREATE INDEX idx_notifications_pending
    ON notifications (scheduled_at) WHERE status = 'pending';

-- ------------------------------------------------------------ activity log --

-- Sentences for the group, not structured diffs for an operator.
CREATE TABLE activity_log (
    id              VARCHAR(36)  PRIMARY KEY,
    trip_id         VARCHAR(36)  NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    actor_user_id   VARCHAR(36),
    actor_member_id VARCHAR(36),
    actor_name      VARCHAR(120) NOT NULL DEFAULT '',
    kind            VARCHAR(64)  NOT NULL,
    message         VARCHAR(500) NOT NULL,
    meta            TEXT,
    created_at      TIMESTAMP    NOT NULL
);

CREATE INDEX idx_activity_trip ON activity_log (trip_id, created_at);
