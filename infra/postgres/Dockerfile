FROM postgres:18@sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722
RUN apt-get update && apt-get install -y --no-install-recommends postgresql-18-postgis-3 && rm -rf /var/lib/apt/lists/*
COPY infra/postgres/init.sql /docker-entrypoint-initdb.d/10-vault.sql
