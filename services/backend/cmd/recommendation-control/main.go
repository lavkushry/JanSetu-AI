package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/control"
)

func run() error {
	mode := flag.String("mode", "get", "get, disable, or enable ranked serving")
	version := flag.Int64("if-version", 0, "expected live version, required for changes")
	flag.Parse()
	if (*mode != "get" && *mode != "disable" && *mode != "enable") || flag.NArg() != 0 || (*mode != "get" && *version < 1) {
		return errors.New("use -mode get|disable|enable; changes require -if-version")
	}
	if environment := os.Getenv("JANSETU_ENV"); environment != "" && environment != "local" && environment != "test" {
		return errors.New("serving control release requires local/test mode")
	}
	dsn := os.Getenv("JANSETU_RECOMMENDATION_CONTROL_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://js_recommendation_control:js_recommendation_control-local@localhost:5438/jansetu?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := platform.RuntimePool(ctx, dsn, "js_recommendation_control")
	if err != nil {
		return errors.New("restricted serving control database unavailable")
	}
	defer db.Close()
	var state control.State
	if *mode == "get" {
		state, err = control.Load(ctx, db)
	} else {
		state, err = control.Set(ctx, db, *mode == "disable", *version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("serving control version changed; read current state before retrying")
	}
	if err != nil {
		return errors.New("serving control operation failed")
	}
	return json.NewEncoder(os.Stdout).Encode(state)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
