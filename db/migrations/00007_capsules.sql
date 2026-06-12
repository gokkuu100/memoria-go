-- +goose Up
CREATE TABLE capsules (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id          uuid        NOT NULL REFERENCES users (id),
    name                text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    description         text        CHECK (description IS NULL OR char_length(description) <= 200),
    type                text        NOT NULL CHECK (type IN ('solo', 'group')),
    state               text        NOT NULL CHECK (state IN (
        'pending', 'active', 'frozen', 'unlocked', 'archived', 'disintegrated'
    )) DEFAULT 'pending',
    unlock_at           timestamptz NOT NULL,
    invite_expires_at   timestamptz,
    frozen_at           timestamptz,
    unlocked_at         timestamptz,
    viewable_until      timestamptz,
    streak_current      int         NOT NULL DEFAULT 0,
    streak_perfect      boolean     NOT NULL DEFAULT true,
    last_contribution_at timestamptz,
    freeze_warning_sent boolean     NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX capsules_creator_idx ON capsules (creator_id);
CREATE INDEX capsules_state_idx ON capsules (state);
CREATE INDEX capsules_unlock_at_idx ON capsules (state, unlock_at)
    WHERE state IN ('active', 'frozen');
CREATE INDEX capsules_invite_expires_idx ON capsules (state, invite_expires_at)
    WHERE state = 'pending';
CREATE INDEX capsules_viewable_until_idx ON capsules (state, viewable_until)
    WHERE state = 'unlocked';

CREATE TABLE capsule_members (
    capsule_id     uuid        NOT NULL REFERENCES capsules (id) ON DELETE CASCADE,
    user_id        uuid        NOT NULL REFERENCES users (id),
    role           text        NOT NULL CHECK (role IN ('admin', 'member')) DEFAULT 'member',
    invite_status  text        NOT NULL CHECK (invite_status IN ('pending', 'accepted', 'declined')),
    accepted_at    timestamptz,
    view_blocked   boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (capsule_id, user_id)
);

CREATE INDEX capsule_members_user_idx ON capsule_members (user_id);

CREATE TABLE capsule_unfreeze_votes (
    capsule_id uuid        NOT NULL REFERENCES capsules (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id),
    voted_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (capsule_id, user_id)
);

CREATE TABLE streak_days (
    capsule_id        uuid NOT NULL REFERENCES capsules (id) ON DELETE CASCADE,
    day               date NOT NULL,
    contributor_count int  NOT NULL DEFAULT 1,
    PRIMARY KEY (capsule_id, day)
);

-- +goose Down
DROP TABLE streak_days;
DROP TABLE capsule_unfreeze_votes;
DROP TABLE capsule_members;
DROP TABLE capsules;
