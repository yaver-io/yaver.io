-- Yaver Edge deliberately stores only identity, revocable session hashes,
-- public device routing identity, access grants, and subscription entitlement.
-- Project metadata, prompts, task output, file paths, vault values, tunnel
-- payloads, and user content are forbidden here and remain on user devices.

PRAGMA foreign_keys = ON;

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  full_name TEXT NOT NULL DEFAULT '',
  avatar_url TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX users_email ON users(lower(email));

CREATE TABLE auth_identities (
  provider TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  email TEXT,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL,
  PRIMARY KEY (provider, provider_id)
);

CREATE INDEX auth_identities_user ON auth_identities(user_id);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id TEXT,
  expires_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  refreshed_at INTEGER NOT NULL
);

CREATE INDEX sessions_user ON sessions(user_id);
CREATE INDEX sessions_expiry ON sessions(expires_at);

CREATE TABLE devices (
  device_id TEXT PRIMARY KEY,
  owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  platform TEXT NOT NULL,
  device_class TEXT,
  public_key TEXT NOT NULL,
  agent_version TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  last_seen_at INTEGER
);

CREATE INDEX devices_owner ON devices(owner_user_id);

CREATE TABLE device_access (
  device_id TEXT NOT NULL REFERENCES devices(device_id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('owner', 'member', 'guest')),
  expires_at INTEGER,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (device_id, user_id)
);

CREATE INDEX device_access_user ON device_access(user_id);

CREATE TABLE subscriptions (
  user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  plan TEXT NOT NULL CHECK (plan IN ('free', 'relay-pro')),
  status TEXT NOT NULL,
  period_ends_at INTEGER,
  updated_at INTEGER NOT NULL
);
