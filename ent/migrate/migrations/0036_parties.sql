-- Membership survives disconnects and world restarts. Presence, coordinates,
-- current HP and channel are never persisted in this aggregate.
CREATE TABLE game_parties (
    id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    world_id integer NOT NULL,
    leader_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    leader_epoch bigint NOT NULL DEFAULT 1 CHECK (leader_epoch > 0),
    setting boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE game_party_members (
    character_id bigint PRIMARY KEY REFERENCES game_characters(id) ON DELETE CASCADE,
    party_id integer NOT NULL REFERENCES game_parties(id) ON DELETE CASCADE,
    member_slot integer NOT NULL CHECK (member_slot BETWEEN 0 AND 5),
    UNIQUE (party_id, member_slot)
);
CREATE TABLE game_party_invitations (
    party_id integer NOT NULL REFERENCES game_parties(id) ON DELETE CASCADE,
    target_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    inviter_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    leader_epoch bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (party_id, target_id),
    CHECK (target_id <> inviter_id)
);
CREATE INDEX game_party_invite_target_idx ON game_party_invitations(target_id, expires_at);
