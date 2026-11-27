-- Inisialisasi Role dan Database Lokal (PostgreSQL) untuk database-migrator-service
-- Dijalankan otomatis oleh container PostgreSQL pada docker-compose.local.yml

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'migrator_user') THEN
        CREATE ROLE migrator_user WITH LOGIN PASSWORD 'change_me';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        CREATE ROLE app_user WITH LOGIN PASSWORD 'app_local_password';
    END IF;
END
$$;

CREATE DATABASE db_name OWNER migrator_user;

-- Setup Default Privileges pada db_name agar tabel baru dari migrator_user
-- otomatis bisa diakses DML (SELECT/INSERT/UPDATE/DELETE) oleh layanan backend (app_user).
\connect db_name

GRANT USAGE, CREATE ON SCHEMA public TO migrator_user;
GRANT USAGE ON SCHEMA public TO app_user;

ALTER DEFAULT PRIVILEGES FOR ROLE migrator_user IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_user;

ALTER DEFAULT PRIVILEGES FOR ROLE migrator_user IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO app_user;
