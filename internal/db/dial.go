package db

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"sync/atomic"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Every engine reaches database/sql the same way when it dials directly; the
// differences only appear once a DialFunc has to be threaded into the driver,
// and each driver has its own way of accepting one:
//
//   - MySQL registers a named "network" globally and the DSN refers to it,
//     so the DSN has to be rewritten as well as the dialer registered.
//   - pgx takes a DialFunc on a parsed *pgx.ConnConfig, which stdlib hands
//     back as an opaque DSN name.
//   - SQLite and DuckDB are files; there is nothing to tunnel.
//
// Both registrations are process-global maps keyed by a name, so each
// connection gets a unique one and drops it again on Close.

// tunnelSeq numbers the per-connection registrations.
var tunnelSeq atomic.Uint64

func tunnelName() string {
	return fmt.Sprintf("lazysql-tunnel-%d", tunnelSeq.Add(1))
}

// openStandard is the no-tunnel path shared by every engine.
func openStandard(driverName, dsn string) (*sql.DB, func(), error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, nil, err
	}
	return db, nil, nil
}

// errNoTunnel is what a file-based engine says when handed a dialer.
func errNoTunnel(d Dialect) error {
	return fmt.Errorf("db: %s is a local file engine and cannot use an SSH tunnel", d.DisplayName())
}

func (d mysqlDialect) openDB(dsn string, dial DialFunc) (*sql.DB, func(), error) {
	if dial == nil {
		return openStandard(d.driverName(), dsn)
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("db: parse mysql dsn: %w", err)
	}
	if cfg.Net == "unix" {
		return nil, nil, fmt.Errorf("db: a unix socket host cannot be reached through an SSH tunnel")
	}
	name := tunnelName()
	// The registered dialer is handed only the address; the network is
	// always TCP, since that is the only thing the DSN can name here.
	mysql.RegisterDialContext(name, func(ctx context.Context, addr string) (net.Conn, error) {
		return dial(ctx, "tcp", addr)
	})
	cfg.Net = name
	db, err := sql.Open(d.driverName(), cfg.FormatDSN())
	if err != nil {
		mysql.DeregisterDialContext(name)
		return nil, nil, err
	}
	return db, func() { mysql.DeregisterDialContext(name) }, nil
}

func (d postgresDialect) openDB(dsn string, dial DialFunc) (*sql.DB, func(), error) {
	if dial == nil {
		return openStandard(d.driverName(), dsn)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("db: parse postgres dsn: %w", err)
	}
	cfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dial(ctx, network, addr)
	}
	// A tunnelled connection must not fall back to a unix socket or to a
	// second host: both would bypass the tunnel.
	cfg.LookupFunc = func(ctx context.Context, host string) ([]string, error) {
		// Name resolution belongs on the far side of the tunnel, so the
		// host travels through unresolved.
		return []string{host}, nil
	}
	name := stdlib.RegisterConnConfig(cfg)
	db, err := sql.Open(d.driverName(), name)
	if err != nil {
		stdlib.UnregisterConnConfig(name)
		return nil, nil, err
	}
	return db, func() { stdlib.UnregisterConnConfig(name) }, nil
}

func (d sqliteDialect) openDB(dsn string, dial DialFunc) (*sql.DB, func(), error) {
	if dial != nil {
		return nil, nil, errNoTunnel(d)
	}
	return openStandard(d.driverName(), dsn)
}

func (d duckdbDialect) openDB(dsn string, dial DialFunc) (*sql.DB, func(), error) {
	if dial != nil {
		return nil, nil, errNoTunnel(d)
	}
	return openStandard(d.driverName(), dsn)
}
