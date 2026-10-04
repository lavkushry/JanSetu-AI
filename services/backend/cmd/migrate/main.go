package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
	"github.com/pressly/goose/v3"
)

func main() {
	dir := flag.String("dir", "../../db/migrations", "application migration directory")
	seed := flag.String("seed", "", "optional explicitly synthetic seed SQL")
	flag.Parse()
	_, err := platform.Load()
	if err != nil {
		log.Fatal(err)
	}
	appURL := setting("JANSETU_MIGRATION_DATABASE_URL", "postgres://jansetu:jansetu-local@localhost:5438/jansetu?sslmode=disable")
	vaultURL := setting("JANSETU_MIGRATION_VAULT_DATABASE_URL", "postgres://jansetu:jansetu-local@localhost:5438/jansetu_vault?sslmode=disable")
	keys, err := vault.LoadKeys(setting("JANSETU_VAULT_KEYS_FILE", "../../infra/vault/local-keys.json"))
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	bootstrap, err := pgx.Connect(ctx, appURL)
	if err != nil {
		log.Fatal("Migration database unavailable")
	}
	err = platform.BootstrapRoles(ctx, bootstrap)
	bootstrap.Close(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if err = goose.SetDialect("postgres"); err != nil {
		log.Fatal(err)
	}
	for _, item := range []struct{ dsn, directory string }{{appURL, *dir}, {vaultURL, filepath.Join(filepath.Dir(*dir), "vault")}} {
		db, e := sql.Open("pgx", item.dsn)
		if e != nil {
			log.Fatal(e)
		}
		e = goose.Up(db, item.directory)
		db.Close()
		if e != nil {
			log.Fatal(e)
		}
	}
	if *seed != "" {
		data, e := os.ReadFile(*seed)
		if e != nil {
			log.Fatal(e)
		}
		conn, e := pgx.Connect(context.Background(), appURL)
		if e != nil {
			log.Fatal(e)
		}
		_, e = conn.Exec(context.Background(), string(data))
		conn.Close(context.Background())
		if e != nil {
			log.Fatal(e)
		}
		log.Println("synthetic fixture seed complete")
	}
	for _, item := range []struct{ url, kind string }{{appURL, "app"}, {vaultURL, "vault"}} {
		conn, err := pgx.Connect(ctx, item.url)
		if err != nil {
			log.Fatal("Migration database unavailable")
		}
		if item.kind == "vault" {
			err = vault.UpgradeLegacy(ctx, conn, keys)
		}
		if err == nil {
			err = platform.InstallPrivileges(ctx, conn, item.kind, keys.SigningKey)
		}
		conn.Close(ctx)
		if err != nil {
			log.Fatal(err)
		}
	}

}

func setting(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
