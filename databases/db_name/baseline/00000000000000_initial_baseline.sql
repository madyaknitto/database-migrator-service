-- Baseline Schema Awal untuk database: db_name
-- ============================================================================
-- INSTRUKSI ADOPSI DATABASE EXISTING:
-- 1. Ganti isi file ini dengan hasil dump struktur schema production saat ini
--    menggunakan script: ./scripts/dump-baseline.sh db_name
--    (atau pg_dump --schema-only --no-owner --no-privileges --no-comments).
-- 2. Pada DB staging & production yang SUDAH memiliki tabel, jalankan:
--      pg-migrate baseline --db=db_name
--    (Perintah tersebut hanya mencatat marker baseline tanpa mengeksekusi SQL ini).
-- 3. Pada DB lokal baru yang masih kosong, jalankan:
--      pg-migrate baseline --db=db_name --apply-sql
-- ============================================================================

-- Contoh DDL PostgreSQL minimal untuk validasi awal (hapus/ganti dengan hasil pg_dump Anda):
CREATE TABLE IF NOT EXISTS public.orders (
    id          BIGSERIAL PRIMARY KEY,
    order_code  VARCHAR(64)    NOT NULL UNIQUE,
    total_price NUMERIC(15, 2) NOT NULL DEFAULT 0,
    status      VARCHAR(32)    NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);
