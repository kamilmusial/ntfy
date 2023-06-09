package server

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

const (
	createWebPushSubscriptionsTableQuery = `
		BEGIN;
		CREATE TABLE IF NOT EXISTS subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			topic TEXT NOT NULL,
			user_id TEXT,
			endpoint TEXT NOT NULL,
			key_auth TEXT NOT NULL,
			key_p256dh TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			warning_sent BOOLEAN DEFAULT FALSE
		);
		CREATE TABLE IF NOT EXISTS schemaVersion (
			id INT PRIMARY KEY,
			version INT NOT NULL
		);	
		CREATE INDEX IF NOT EXISTS idx_topic ON subscriptions (topic);
		CREATE INDEX IF NOT EXISTS idx_endpoint ON subscriptions (endpoint);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_topic_endpoint ON subscriptions (topic, endpoint);
		COMMIT;
	`

	insertWebPushSubscriptionQuery = `
		INSERT OR REPLACE INTO subscriptions (topic, user_id, endpoint, key_auth, key_p256dh)
		VALUES (?, ?, ?, ?, ?)
	`
	deleteWebPushSubscriptionByEndpointQuery = `DELETE FROM subscriptions WHERE endpoint = ?`
	deleteWebPushSubscriptionByUserIDQuery   = `DELETE FROM subscriptions WHERE user_id = ?`
	deleteWebPushSubscriptionsByAgeQuery     = `DELETE FROM subscriptions WHERE warning_sent = 1 AND updated_at <= datetime('now', ?)`

	selectWebPushSubscriptionsForTopicQuery     = `SELECT endpoint, key_auth, key_p256dh, user_id FROM subscriptions WHERE topic = ?`
	selectWebPushSubscriptionsExpiringSoonQuery = `SELECT DISTINCT endpoint, key_auth, key_p256dh, user_id FROM subscriptions WHERE warning_sent = 0 AND updated_at <= datetime('now', ?)`

	updateWarningSentQuery = `UPDATE subscriptions SET warning_sent = true WHERE warning_sent = 0 AND updated_at <= datetime('now', ?)`
)

// Schema management queries
const (
	currentWebPushSchemaVersion     = 1
	insertWebPushSchemaVersion      = `INSERT INTO schemaVersion VALUES (1, ?)`
	selectWebPushSchemaVersionQuery = `SELECT version FROM schemaVersion WHERE id = 1`
)

type webPushStore struct {
	db *sql.DB
}

func newWebPushStore(filename string) (*webPushStore, error) {
	db, err := sql.Open("sqlite3", filename)
	if err != nil {
		return nil, err
	}
	if err := setupWebPushDB(db); err != nil {
		return nil, err
	}
	return &webPushStore{
		db: db,
	}, nil
}

func setupWebPushDB(db *sql.DB) error {
	// If 'schemaVersion' table does not exist, this must be a new database
	rows, err := db.Query(selectWebPushSchemaVersionQuery)
	if err != nil {
		return setupNewWebPushDB(db)
	}
	return rows.Close()
}

func setupNewWebPushDB(db *sql.DB) error {
	if _, err := db.Exec(createWebPushSubscriptionsTableQuery); err != nil {
		return err
	}
	if _, err := db.Exec(insertWebPushSchemaVersion, currentWebPushSchemaVersion); err != nil {
		return err
	}
	return nil
}

// UpsertSubscription adds or updates Web Push subscriptions for the given topics and user ID. It always first deletes all
// existing entries for a given endpoint.
func (c *webPushStore) UpsertSubscription(endpoint string, topics []string, userID, auth, p256dh string) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(deleteWebPushSubscriptionByEndpointQuery, endpoint); err != nil {
		return err
	}
	for _, topic := range topics {
		if _, err = tx.Exec(insertWebPushSubscriptionQuery, topic, userID, endpoint, auth, p256dh); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *webPushStore) SubscriptionsForTopic(topic string) ([]*webPushSubscription, error) {
	rows, err := c.db.Query(selectWebPushSubscriptionsForTopicQuery, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subscriptions := make([]*webPushSubscription, 0)
	for rows.Next() {
		var endpoint, auth, p256dh, userID string
		if err = rows.Scan(&endpoint, &auth, &p256dh, &userID); err != nil {
			return nil, err
		}
		subscriptions = append(subscriptions, &webPushSubscription{
			Endpoint: endpoint,
			Auth:     auth,
			P256dh:   p256dh,
			UserID:   userID,
		})
	}
	return subscriptions, nil
}

func (c *webPushStore) ExpireAndGetExpiringSubscriptions(warningDuration time.Duration, expiryDuration time.Duration) ([]*webPushSubscription, error) {
	// TODO this should be two functions
	tx, err := c.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(deleteWebPushSubscriptionsByAgeQuery, fmt.Sprintf("-%.2f seconds", expiryDuration.Seconds()))
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(selectWebPushSubscriptionsExpiringSoonQuery, fmt.Sprintf("-%.2f seconds", warningDuration.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	subscriptions := make([]*webPushSubscription, 0)
	for rows.Next() {
		var endpoint, auth, p256dh, userID string
		if err = rows.Scan(&endpoint, &auth, &p256dh, &userID); err != nil {
			return nil, err
		}
		subscriptions = append(subscriptions, &webPushSubscription{
			Endpoint: endpoint,
			Auth:     auth,
			P256dh:   p256dh,
			UserID:   userID,
		})
	}

	// also set warning as sent
	_, err = tx.Exec(updateWarningSentQuery, fmt.Sprintf("-%.2f seconds", warningDuration.Seconds()))
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return subscriptions, nil
}

func (c *webPushStore) RemoveSubscriptionsByEndpoint(endpoint string) error {
	_, err := c.db.Exec(deleteWebPushSubscriptionByEndpointQuery, endpoint)
	return err
}

func (c *webPushStore) RemoveSubscriptionsByUserID(userID string) error {
	_, err := c.db.Exec(deleteWebPushSubscriptionByUserIDQuery, userID)
	return err
}

func (c *webPushStore) Close() error {
	return c.db.Close()
}