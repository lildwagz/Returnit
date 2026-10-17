#!/bin/sh
# Runs once, when the database volume is created: gives Keycloak its own database and user so it
# can never read the app's data (and vice versa). KEYCLOAK_DB_PASSWORD is generated hex by init.sh.
set -e
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres <<SQL
CREATE ROLE keycloak LOGIN PASSWORD '${KEYCLOAK_DB_PASSWORD}';
CREATE DATABASE keycloak OWNER keycloak;
SQL
