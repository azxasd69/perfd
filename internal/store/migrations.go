package store

// migrations - схема БД по приложению Б КПР v3.4.
// Правило: миграции только добавляющие; применённая версия фиксируется в schema_meta.
var migrations = []string{
	// 001: базовая схема (стенды, паспорта, замеры, история, агрегаты, журнал приёма, ключи, команды)
	`
CREATE TABLE IF NOT EXISTS schema_meta (
  version    INTEGER NOT NULL PRIMARY KEY,
  applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS stands (
  id               TEXT PRIMARY KEY,
  fingerprint      TEXT NOT NULL,
  parent_id        TEXT REFERENCES stands(id),
  name             TEXT NOT NULL DEFAULT '',
  platform         TEXT NOT NULL DEFAULT '',
  os               TEXT NOT NULL DEFAULT '',
  cpu              TEXT NOT NULL DEFAULT '',
  deploy_mode      TEXT NOT NULL DEFAULT '',
  visibility       TEXT NOT NULL DEFAULT 'public',
  passport_missing INTEGER NOT NULL DEFAULT 1,
  first_seen       INTEGER NOT NULL,
  last_seen        INTEGER NOT NULL,
  UNIQUE (id, fingerprint)
);

CREATE TABLE IF NOT EXISTS stand_passports (
  stand_id    TEXT NOT NULL REFERENCES stands(id) ON DELETE CASCADE,
  fingerprint TEXT NOT NULL,
  schema      INTEGER NOT NULL,
  props_json  TEXT NOT NULL,
  captured_at INTEGER NOT NULL,
  PRIMARY KEY (stand_id, fingerprint, captured_at)
);

CREATE TABLE IF NOT EXISTS results (
  stand_id    TEXT NOT NULL REFERENCES stands(id) ON DELETE CASCADE,
  platform    TEXT NOT NULL,
  scenario    TEXT NOT NULL,
  median_ms   REAL NOT NULL,
  per_unit_ms REAL NOT NULL,
  unit        TEXT NOT NULL,
  portion     REAL NOT NULL,
  repeats     INTEGER NOT NULL DEFAULT 1,
  min_ms      REAL,
  max_ms      REAL,
  measured_at INTEGER NOT NULL,
  received_at INTEGER NOT NULL,
  PRIMARY KEY (stand_id, platform, scenario)
);

CREATE TABLE IF NOT EXISTS history (
  stand_id    TEXT NOT NULL REFERENCES stands(id) ON DELETE CASCADE,
  scenario    TEXT NOT NULL,
  measured_at INTEGER NOT NULL,
  median_ms   REAL NOT NULL,
  per_unit_ms REAL NOT NULL,
  min_ms      REAL,
  max_ms      REAL,
  portion     REAL NOT NULL,
  unit        TEXT NOT NULL,
  repeats     INTEGER NOT NULL DEFAULT 1,
  received_at INTEGER NOT NULL,
  PRIMARY KEY (stand_id, scenario, measured_at)
);

CREATE TABLE IF NOT EXISTS aggregates (
  stand_id       TEXT NOT NULL REFERENCES stands(id) ON DELETE CASCADE,
  base_id        TEXT NOT NULL,
  formula        INTEGER NOT NULL DEFAULT 1,
  index_value    REAL,
  scenarios_used INTEGER NOT NULL DEFAULT 0,
  computed_at    INTEGER NOT NULL,
  PRIMARY KEY (stand_id, base_id, formula)
);

CREATE TABLE IF NOT EXISTS ingest_log (
  id          TEXT PRIMARY KEY,
  stand_id    TEXT,
  kind        TEXT NOT NULL,
  status      TEXT NOT NULL,
  http_code   INTEGER NOT NULL,
  bytes       INTEGER NOT NULL,
  error       TEXT,
  remote_addr TEXT,
  request_id  TEXT,
  received_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS api_keys (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,
  role         TEXT NOT NULL,
  prefix       TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER,
  revoked_at   INTEGER
);

CREATE TABLE IF NOT EXISTS cmd_queue (
  id         TEXT PRIMARY KEY,
  stand_id   TEXT NOT NULL REFERENCES stands(id) ON DELETE CASCADE,
  action     TEXT NOT NULL,
  args_json  TEXT,
  status     TEXT NOT NULL DEFAULT 'new',
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  acked_at   INTEGER
);

CREATE INDEX IF NOT EXISTS idx_history_recent   ON history (measured_at DESC, per_unit_ms DESC);
CREATE INDEX IF NOT EXISTS idx_history_scenario ON history (stand_id, scenario, measured_at DESC);
CREATE INDEX IF NOT EXISTS idx_results_scenario ON results (scenario, per_unit_ms);
CREATE INDEX IF NOT EXISTS idx_ingest_received  ON ingest_log (received_at DESC);
CREATE INDEX IF NOT EXISTS idx_cmd_pending      ON cmd_queue (stand_id, status, expires_at);
`,

	// 002: схема ТЗ § 8 — поколения стенда, свёртки, справочник сценариев, аудит.
	// Правило ТЗ Р-10: замеры и паспорта ссылаются на ПОКОЛЕНИЕ, а не на стенд.
	`
CREATE TABLE IF NOT EXISTS stand_generations (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  stand_id             TEXT NOT NULL REFERENCES stands(id) ON DELETE CASCADE,
  fingerprint          TEXT NOT NULL UNIQUE,
  stand_key            TEXT NOT NULL,
  parent_generation_id INTEGER REFERENCES stand_generations(id),
  platform             TEXT NOT NULL DEFAULT '',
  os                   TEXT NOT NULL DEFAULT '',
  cpu                  TEXT NOT NULL DEFAULT '',
  threads              INTEGER,
  ram_mb               INTEGER,
  deploy_mode          TEXT NOT NULL DEFAULT '',
  dbms                 TEXT NOT NULL DEFAULT '',
  server_name          TEXT NOT NULL DEFAULT '',
  passport_missing     INTEGER NOT NULL DEFAULT 1,
  first_seen           INTEGER NOT NULL,
  last_seen            INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_gen_stand ON stand_generations (stand_id, last_seen DESC);

CREATE TABLE IF NOT EXISTS history_monthly (
  generation_id INTEGER NOT NULL REFERENCES stand_generations(id) ON DELETE CASCADE,
  scenario      TEXT NOT NULL,
  month         INTEGER NOT NULL,
  samples       INTEGER NOT NULL,
  median_ms     REAL NOT NULL,
  per_unit_ms   REAL NOT NULL,
  min_ms        REAL NOT NULL,
  max_ms        REAL NOT NULL,
  covered_from  INTEGER NOT NULL,
  covered_to    INTEGER NOT NULL,
  rolled_at     INTEGER NOT NULL,
  PRIMARY KEY (generation_id, scenario, month)
);

CREATE TABLE IF NOT EXISTS scenarios (
  code            TEXT PRIMARY KEY,
  representation  TEXT NOT NULL DEFAULT '',
  unit            TEXT NOT NULL DEFAULT '',
  default_portion REAL NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS audit_log (
  id     INTEGER PRIMARY KEY AUTOINCREMENT,
  at     INTEGER NOT NULL,
  actor  TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  target TEXT,
  detail TEXT
);

CREATE TABLE IF NOT EXISTS tokens (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  role       TEXT NOT NULL DEFAULT 'ingest',
  prefix     TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  revoked_at INTEGER
);

-- Замеры и история переходят на поколения (ТЗ Р-10). Старые таблицы
-- перестраиваются; перенос данных идёт ниже, поэтому колонка generation_id
-- в результатах появляется только здесь.
CREATE TABLE results_v2 (
  generation_id INTEGER NOT NULL REFERENCES stand_generations(id) ON DELETE CASCADE,
  scenario      TEXT NOT NULL,
  platform      TEXT NOT NULL DEFAULT '',
  median_ms     REAL NOT NULL,
  per_unit_ms   REAL NOT NULL,
  unit          TEXT NOT NULL,
  portion       REAL NOT NULL,
  repeats       INTEGER NOT NULL DEFAULT 1,
  min_ms        REAL,
  max_ms        REAL,
  measured_at   INTEGER NOT NULL,
  received_at   INTEGER NOT NULL,
  PRIMARY KEY (generation_id, scenario)
);

CREATE TABLE history_v2 (
  generation_id INTEGER NOT NULL REFERENCES stand_generations(id) ON DELETE CASCADE,
  scenario      TEXT NOT NULL,
  measured_at   INTEGER NOT NULL,
  median_ms     REAL NOT NULL,
  per_unit_ms   REAL NOT NULL,
  min_ms        REAL,
  max_ms        REAL,
  portion       REAL NOT NULL,
  unit          TEXT NOT NULL,
  repeats       INTEGER NOT NULL DEFAULT 1,
  received_at   INTEGER NOT NULL,
  PRIMARY KEY (generation_id, scenario, measured_at)
);

CREATE TABLE aggregates_v2 (
  generation_id      INTEGER NOT NULL REFERENCES stand_generations(id) ON DELETE CASCADE,
  base_generation_id INTEGER NOT NULL DEFAULT 0,
  formula            INTEGER NOT NULL DEFAULT 1,
  index_value        REAL NOT NULL DEFAULT 0,
  scenarios_used     INTEGER NOT NULL DEFAULT 0,
  best_scenario      TEXT NOT NULL DEFAULT '',
  best_delta_pct     REAL,
  worst_scenario     TEXT NOT NULL DEFAULT '',
  worst_delta_pct    REAL,
  last_measured_at   INTEGER,
  computed_at        INTEGER NOT NULL,
  PRIMARY KEY (generation_id, base_generation_id, formula)
);

-- Перенос старых данных: один стенд = одно поколение (fingerprint уже есть в stands).
INSERT OR IGNORE INTO stand_generations (stand_id, fingerprint, stand_key, platform, os, cpu, deploy_mode, passport_missing, first_seen, last_seen)
SELECT id, fingerprint, id, platform, os, cpu, deploy_mode, passport_missing, first_seen, last_seen FROM stands;

INSERT OR IGNORE INTO results_v2 (generation_id, scenario, platform, median_ms, per_unit_ms, unit, portion, repeats, min_ms, max_ms, measured_at, received_at)
SELECT g.id, r.scenario, r.platform, r.median_ms, r.per_unit_ms, r.unit, r.portion, r.repeats, r.min_ms, r.max_ms, r.measured_at, r.received_at
FROM results r JOIN stand_generations g ON g.stand_id = r.stand_id;

INSERT OR IGNORE INTO history_v2 (generation_id, scenario, measured_at, median_ms, per_unit_ms, min_ms, max_ms, portion, unit, repeats, received_at)
SELECT g.id, h.scenario, h.measured_at, h.median_ms, h.per_unit_ms, h.min_ms, h.max_ms, h.portion, h.unit, h.repeats, h.received_at
FROM history h JOIN stand_generations g ON g.stand_id = h.stand_id;

DROP TABLE IF EXISTS results;
DROP TABLE IF EXISTS history;
DROP TABLE IF EXISTS aggregates;
ALTER TABLE results_v2 RENAME TO results;
ALTER TABLE history_v2 RENAME TO history;
ALTER TABLE aggregates_v2 RENAME TO aggregates;

CREATE INDEX IF NOT EXISTS idx_history_top ON history (measured_at DESC, per_unit_ms DESC);
CREATE INDEX IF NOT EXISTS idx_history_ser ON history (generation_id, scenario, measured_at DESC);

-- Новые колонки журнала приёма (ТЗ § 8): канал, хеш тела, замечание, поколение.
ALTER TABLE ingest_log ADD COLUMN channel TEXT NOT NULL DEFAULT '';
ALTER TABLE ingest_log ADD COLUMN body_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE ingest_log ADD COLUMN issue TEXT NOT NULL DEFAULT '';
ALTER TABLE ingest_log ADD COLUMN generation_id INTEGER;

-- Паспорта ссылаются на поколение; старую строку сохраняем как справочник.
ALTER TABLE stand_passports ADD COLUMN generation_id INTEGER;
ALTER TABLE stand_passports ADD COLUMN body_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE stand_passports ADD COLUMN is_current INTEGER NOT NULL DEFAULT 0;
UPDATE stand_passports SET generation_id = (SELECT g.id FROM stand_generations g WHERE g.stand_id = stand_passports.stand_id LIMIT 1);
CREATE INDEX IF NOT EXISTS idx_passport_current ON stand_passports (generation_id, is_current);
`,
}
