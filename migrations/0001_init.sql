-- 0001_init.sql — Section 3 of the technical requirements doc.
-- This is the real target schema for a PostgreSQL deployment. In this sandbox
-- (no live Postgres instance / no module-proxy access for pgx+sqlc), the running
-- app uses internal/store.FileStore instead — see progress.md Section 0.

CREATE EXTENSION IF NOT EXISTS pgcrypto; -- for gen_random_uuid()

CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  game_name VARCHAR(20) UNIQUE NOT NULL,
  secret_number_hash TEXT NOT NULL,       -- bcrypt hash, never plaintext
  session_secret TEXT NOT NULL,           -- rotated on logout to invalidate old cookies
  avatar_url TEXT,
  wins INTEGER NOT NULL DEFAULT 0,
  losses INTEGER NOT NULL DEFAULT 0,
  matches_played INTEGER NOT NULL DEFAULT 0,
  rank VARCHAR(20) NOT NULL DEFAULT 'Rookie',
  level INTEGER NOT NULL DEFAULT 1,
  win_rate NUMERIC(5,1) NOT NULL DEFAULT 0,
  favorite_game_mode VARCHAR(30),
  sound_enabled BOOLEAN NOT NULL DEFAULT true,
  notify_enabled BOOLEAN NOT NULL DEFAULT true,
  deleted BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_active_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_users_wins ON users (wins DESC);   -- leaderboard sort

CREATE TABLE matches (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  player_a_id UUID NOT NULL REFERENCES users(id),
  player_b_id UUID NOT NULL REFERENCES users(id),
  winner_id UUID NOT NULL REFERENCES users(id),
  loser_id UUID NOT NULL REFERENCES users(id),
  ranked BOOLEAN NOT NULL DEFAULT true,
  difficulty VARCHAR(10) NOT NULL,
  mode VARCHAR(10) NOT NULL,             -- standard | blitz
  final_score_a INTEGER NOT NULL,
  final_score_b INTEGER NOT NULL,
  started_at TIMESTAMPTZ NOT NULL,
  ended_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE object_catalog (
  id SERIAL PRIMARY KEY,
  category VARCHAR(30) NOT NULL,
  name VARCHAR(50) NOT NULL,
  emoji VARCHAR(10) NOT NULL
);
CREATE INDEX idx_object_catalog_category ON object_catalog (category);
