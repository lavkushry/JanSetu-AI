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
	"github.com/pressly/goose/v3"
)

func main() {
	dir := flag.String("dir", "../../db/migrations", "application migration directory")
	seed := flag.String("seed", "", "optional explicitly synthetic seed SQL")
	flag.Parse()
	c, err := platform.Load()
	if err != nil {
		log.Fatal(err)
	}
	if err = goose.SetDialect("postgres"); err != nil {
		log.Fatal(err)
	}
	for _, item := range []struct{ dsn, directory string }{{c.DatabaseURL, *dir}, {c.VaultURL, filepath.Join(filepath.Dir(*dir), "vault")}} {
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
		conn, e := pgx.Connect(context.Background(), c.DatabaseURL)
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
}
