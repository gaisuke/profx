package database

import (
	"database/sql"
	"fmt"
	"log"
	_ "github.com/lib/pq"
)

type Config struct {
	Host    string
	Port    int
	User    string
	Pass    string
	DBName  string
	SSLMode string
}

func NewConnection(cfg Config) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Pass, cfg.DBName, cfg.SSLMode,
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	log.Println("Database connection established")
	return db, nil
}
