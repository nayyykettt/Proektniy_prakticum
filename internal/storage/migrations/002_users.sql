-- Учётные записи и владельцы проектов: каждый пользователь видит только свои проекты.
-- Миграции выполняются с выключенными внешними ключами (см. Store.migrate),
-- поэтому пересоздание projects не удаляет каскадно связанные данные.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    login         TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT    NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE projects_new (
    id                  INTEGER PRIMARY KEY,
    owner_id            INTEGER REFERENCES users(id) ON DELETE CASCADE, -- NULL — проект создан до появления учётных записей
    name                TEXT    NOT NULL,
    repo_url            TEXT    NOT NULL,
    branch              TEXT    NOT NULL DEFAULT '',
    provider            TEXT    NOT NULL DEFAULT 'none',
    provider_url        TEXT    NOT NULL DEFAULT '',
    provider_project_id TEXT    NOT NULL DEFAULT '',
    token_enc           BLOB,
    last_synced_sha     TEXT    NOT NULL DEFAULT '',
    sync_status         TEXT    NOT NULL DEFAULT 'never',
    sync_error          TEXT    NOT NULL DEFAULT '',
    last_sync_at        INTEGER,
    created_at          INTEGER NOT NULL,
    UNIQUE (owner_id, name)
);

INSERT INTO projects_new (id, owner_id, name, repo_url, branch, provider, provider_url, provider_project_id,
    token_enc, last_synced_sha, sync_status, sync_error, last_sync_at, created_at)
SELECT id, NULL, name, repo_url, branch, provider, provider_url, provider_project_id,
    token_enc, last_synced_sha, sync_status, sync_error, last_sync_at, created_at
FROM projects;

DROP TABLE projects;
ALTER TABLE projects_new RENAME TO projects;
CREATE INDEX idx_projects_owner ON projects(owner_id);
-- В UNIQUE (owner_id, name) значения NULL различны — уникальность имён проектов без владельца задаём отдельно.
CREATE UNIQUE INDEX idx_projects_unowned_name ON projects(name) WHERE owner_id IS NULL;
