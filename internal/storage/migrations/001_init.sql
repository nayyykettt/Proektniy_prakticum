-- Время хранится в Unix-секундах (UTC).

CREATE TABLE projects (
    id                  INTEGER PRIMARY KEY,
    name                TEXT    NOT NULL UNIQUE,
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
    created_at          INTEGER NOT NULL
);

CREATE TABLE authors (
    id         INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    email      TEXT    NOT NULL DEFAULT '',
    login      TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX idx_authors_project ON authors(project_id);

-- Все известные идентичности автора (email из Git, логин и имя из Т1 Код).
CREATE TABLE author_aliases (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL, -- email | login | name
    value      TEXT    NOT NULL, -- в нижнем регистре
    author_id  INTEGER NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, kind, value)
);

CREATE TABLE commits (
    project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    sha           TEXT    NOT NULL,
    author_id     INTEGER NOT NULL REFERENCES authors(id),
    authored_at   INTEGER NOT NULL,
    tz_offset     INTEGER NOT NULL DEFAULT 0,
    message       TEXT    NOT NULL,
    is_merge      INTEGER NOT NULL DEFAULT 0,
    additions     INTEGER NOT NULL DEFAULT 0,
    deletions     INTEGER NOT NULL DEFAULT 0,
    files_changed INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, sha)
);
CREATE INDEX idx_commits_time ON commits(project_id, authored_at);

CREATE TABLE file_changes (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    sha        TEXT    NOT NULL,
    path       TEXT    NOT NULL,
    additions  INTEGER NOT NULL,
    deletions  INTEGER NOT NULL,
    is_test    INTEGER NOT NULL DEFAULT 0,
    excluded   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, sha, path)
);

CREATE TABLE merge_requests (
    project_id      INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    iid             INTEGER NOT NULL,
    title           TEXT    NOT NULL,
    author_id       INTEGER NOT NULL REFERENCES authors(id),
    state           TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    merged_at       INTEGER,
    closed_at       INTEGER,
    first_review_at INTEGER,
    additions       INTEGER NOT NULL DEFAULT -1,
    deletions       INTEGER NOT NULL DEFAULT -1,
    comments        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, iid)
);

CREATE TABLE mr_reviews (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    iid        INTEGER NOT NULL,
    author_id  INTEGER NOT NULL REFERENCES authors(id),
    at         INTEGER NOT NULL,
    comments   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, iid, author_id)
);

CREATE TABLE issues (
    project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    iid          INTEGER NOT NULL,
    title        TEXT    NOT NULL,
    labels       TEXT    NOT NULL DEFAULT '[]', -- JSON-массив
    state        TEXT    NOT NULL,
    author_login TEXT    NOT NULL DEFAULT '',
    assignee_id  INTEGER REFERENCES authors(id),
    created_at   INTEGER NOT NULL,
    closed_at    INTEGER,
    PRIMARY KEY (project_id, iid)
);

CREATE TABLE teams (
    id         INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    UNIQUE (project_id, name)
);

CREATE TABLE team_members (
    team_id   INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    author_id INTEGER NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    PRIMARY KEY (team_id, author_id)
);

CREATE TABLE reports (
    id           INTEGER PRIMARY KEY,
    project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    team_id      INTEGER NOT NULL DEFAULT 0,
    from_ts      INTEGER NOT NULL,
    to_ts        INTEGER NOT NULL,
    generated_at INTEGER NOT NULL,
    trigger      TEXT    NOT NULL,
    payload      BLOB    NOT NULL
);
CREATE INDEX idx_reports_project ON reports(project_id, generated_at);
