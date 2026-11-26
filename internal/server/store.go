package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"probex/internal/protocol"
	"strconv"
	"sync"
	"time"

	"modernc.org/sqlite"
)

const timestampFormat = "2006-01-02T15:04:05.000000000Z"

type Store struct {
	db *sql.DB
	mu sync.Mutex
}
type nodeRow struct {
	protocol.Node
	KeyHash string
}

func OpenStore(path string) (*Store, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version, exists int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='nodes'").Scan(&exists); err != nil {
		db.Close()
		return nil, err
	}
	if exists != 0 && version == 0 || version != 0 && (version < 2 || version > 5) {
		db.Close()
		return nil, fmt.Errorf("unsupported database schema: use a new --db file; existing database was not changed")
	}
	migration := ""
	if version == 3 {
		migration = `ALTER TABLE forwards ADD COLUMN name TEXT NOT NULL DEFAULT ''; UPDATE forwards SET name=listen_addr;`
	}
	if version < 5 {
		migration += `DROP INDEX IF EXISTS nodes_active_name;`
	}
	_, err = db.Exec(`PRAGMA foreign_keys=ON;
BEGIN;
CREATE TABLE IF NOT EXISTS nodes(id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, admission TEXT NOT NULL, key_hash TEXT NOT NULL, body TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS nodes_key ON nodes(key_hash);
CREATE TABLE IF NOT EXISTS commands(id TEXT PRIMARY KEY, node_id INTEGER NOT NULL REFERENCES nodes(id), status TEXT NOT NULL, created_at TEXT NOT NULL, body TEXT NOT NULL);
	CREATE INDEX IF NOT EXISTS command_history ON commands(node_id, created_at DESC);
CREATE TABLE IF NOT EXISTS command_summaries(id TEXT PRIMARY KEY REFERENCES commands(id) ON DELETE CASCADE, body TEXT NOT NULL);
CREATE TRIGGER IF NOT EXISTS command_summary_insert AFTER INSERT ON commands BEGIN
	INSERT INTO command_summaries(id,body) VALUES(NEW.id,json_set(NEW.body,'$.stdout',NULL,'$.stderr',NULL));
END;
CREATE TRIGGER IF NOT EXISTS command_summary_update AFTER UPDATE OF body ON commands BEGIN
	UPDATE command_summaries SET body=json_set(NEW.body,'$.stdout',NULL,'$.stderr',NULL) WHERE id=NEW.id;
END;
INSERT INTO command_summaries(id,body)
	SELECT c.id,json_set(c.body,'$.stdout',NULL,'$.stderr',NULL) FROM commands c
	WHERE NOT EXISTS (SELECT 1 FROM command_summaries WHERE id=c.id);
	CREATE TABLE IF NOT EXISTS enrollments(node_id INTEGER PRIMARY KEY REFERENCES nodes(id), token_hash TEXT NOT NULL, expires_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS forwards(id INTEGER PRIMARY KEY AUTOINCREMENT, node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE, name TEXT NOT NULL DEFAULT '', protocol TEXT NOT NULL, listen_addr TEXT NOT NULL, target_host TEXT NOT NULL, target_port INTEGER NOT NULL, created_at TEXT NOT NULL, UNIQUE(protocol,listen_addr));
` + migration + `
CREATE UNIQUE INDEX IF NOT EXISTS nodes_active_name ON nodes(name) WHERE id != 0 AND admission IN ('invited','review','approved');
PRAGMA user_version=5;
COMMIT;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	_, err = db.Exec(`BEGIN;
DELETE FROM commands WHERE id IN (SELECT id FROM command_summaries WHERE json_extract(body,'$.command')='[interactive shell]' AND json_extract(body,'$.timeout_seconds')=0) OR node_id IN (SELECT id FROM nodes WHERE admission='deleted');
DELETE FROM enrollments WHERE node_id IN (SELECT id FROM nodes WHERE admission='deleted');
DELETE FROM nodes WHERE admission='deleted';
COMMIT;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	rows, err := db.Query(`SELECT body FROM commands WHERE status IN ('queued','running')`)
	if err != nil {
		db.Close()
		return nil, err
	}
	var pending []protocol.Command
	for rows.Next() {
		var body string
		var c protocol.Command
		if err = rows.Scan(&body); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(body), &c); err != nil {
			break
		}
		pending = append(pending, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, c := range pending {
		stamp := time.Now().UTC()
		c.Status = "unknown"
		c.Error = "server restarted before result"
		c.FinishedAt = &stamp
		if err = s.saveCommand(c); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) deleteNode(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.QueryRow(`SELECT id FROM nodes WHERE id=?`, id).Scan(&id); err != nil {
		return err
	}
	if id == protocol.LocalNodeID {
		return errAdmission
	}
	for _, query := range []string{
		`DELETE FROM commands WHERE node_id=?`,
		`DELETE FROM enrollments WHERE node_id=?`,
		`DELETE FROM nodes WHERE id=?`,
	} {
		if _, err := tx.Exec(query, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) clearHistory(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM commands WHERE node_id=? AND status NOT IN ('queued','running')`, id)
	return err
}

var errNameExists = errors.New("node name already exists")
var errAdmission = errors.New("node admission state does not allow this operation")
var errIdentity = errors.New("invalid node identity")

func readNode(row *sql.Row) (nodeRow, error) {
	var n nodeRow
	var body string
	err := row.Scan(&body, &n.KeyHash)
	if err == nil {
		err = json.Unmarshal([]byte(body), &n.Node)
	}
	return n, err
}

func persistNode(tx *sql.Tx, n *nodeRow, create bool) error {
	if create {
		result, err := tx.Exec(`INSERT INTO nodes(name,admission,key_hash,body) VALUES(?,?,?,'{}')`, n.Name, n.Admission, n.KeyHash)
		if err != nil {
			var constraint *sqlite.Error
			if errors.As(err, &constraint) && constraint.Code() == 2067 {
				return errNameExists
			}
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		n.ID = strconv.FormatInt(id, 10)
	}
	body, err := json.Marshal(n.Node)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE nodes SET name=?,admission=?,key_hash=?,body=? WHERE id=?`, n.Name, n.Admission, n.KeyHash, string(body), n.ID)
	var constraint *sqlite.Error
	if errors.As(err, &constraint) && constraint.Code() == 2067 {
		return errNameExists
	}
	return err
}

func expireKeys(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT nodes.body,nodes.key_hash FROM nodes JOIN enrollments ON nodes.id=enrollments.node_id WHERE nodes.admission='invited' AND enrollments.expires_at<=?`, time.Now().UTC().Format(timestampFormat))
	if err != nil {
		return err
	}
	var expired []nodeRow
	for rows.Next() {
		var n nodeRow
		var body string
		if err = rows.Scan(&body, &n.KeyHash); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(body), &n.Node); err != nil {
			break
		}
		n.Admission = "expired"
		expired = append(expired, n)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, n := range expired {
		if err = persistNode(tx, &n, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) node(id string) (nodeRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nodeRow{}, err
	}
	defer tx.Rollback()
	if err = expireKeys(tx); err != nil {
		return nodeRow{}, err
	}
	n, err := readNode(tx.QueryRow(`SELECT body,key_hash FROM nodes WHERE id=?`, id))
	if err != nil {
		return n, err
	}
	return n, tx.Commit()
}

func (s *Store) nodes(includeLocal bool) ([]protocol.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = expireKeys(tx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT body FROM nodes WHERE admission!='deleted' AND (id != 0 OR ?) ORDER BY id != 0, name, id`, includeLocal)
	if err != nil {
		return nil, err
	}
	result := []protocol.Node{}
	for rows.Next() {
		var body string
		var n protocol.Node
		if err = rows.Scan(&body); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(body), &n); err != nil {
			break
		}
		result = append(result, n)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

func (s *Store) insertNode(n *nodeRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = expireKeys(tx); err != nil {
		return err
	}
	if err = persistNode(tx, n, true); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) changeNode(id string, change func(*sql.Tx, *nodeRow) error) (nodeRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nodeRow{}, err
	}
	defer tx.Rollback()
	if err = expireKeys(tx); err != nil {
		return nodeRow{}, err
	}
	n, err := readNode(tx.QueryRow(`SELECT body,key_hash FROM nodes WHERE id=?`, id))
	if err != nil {
		return n, err
	}
	if n.Admission == "deleted" {
		return n, sql.ErrNoRows
	}
	if err = change(tx, &n); err != nil {
		return n, err
	}
	if err = persistNode(tx, &n, false); err != nil {
		return n, err
	}
	return n, tx.Commit()
}

func (s *Store) updateNode(id string, update func(*protocol.Node)) error {
	_, err := s.changeNode(id, func(_ *sql.Tx, n *nodeRow) error { update(&n.Node); return nil })
	return err
}

func (s *Store) command(id string) (protocol.Command, error) {
	var c protocol.Command
	var body string
	err := s.db.QueryRow(`SELECT body FROM commands WHERE id=?`, id).Scan(&body)
	if err == nil {
		err = json.Unmarshal([]byte(body), &c)
	}
	return c, err
}
func (s *Store) insertCommand(c protocol.Command) error {
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO commands(id,node_id,status,created_at,body) VALUES(?,?,?,?,?)`, c.ID, c.NodeID, c.Status, c.CreatedAt.Format(time.RFC3339Nano), string(body))
	return err
}
func (s *Store) saveCommand(c protocol.Command) error {
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE commands SET status=?,body=? WHERE id=?`, c.Status, string(body), c.ID)
	return err
}
func (s *Store) updateCommand(id string, update func(*protocol.Command)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.command(id)
	if err != nil {
		return err
	}
	update(&c)
	return s.saveCommand(c)
}
func (s *Store) history(id string, offset int) ([]protocol.Command, error) {
	rows, err := s.db.Query(`SELECT s.body FROM commands c JOIN command_summaries s ON s.id=c.id
		WHERE c.node_id=? ORDER BY c.created_at DESC LIMIT 20 OFFSET ?`, id, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.Command{}
	for rows.Next() {
		var body string
		var c protocol.Command
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func isUniqueConstraint(err error) bool {
	var constraint *sqlite.Error
	return errors.As(err, &constraint) && constraint.Code() == 2067
}
