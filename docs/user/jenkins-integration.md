# Integrasi Repo Backend Jenkins

Repository backend tidak menyimpan file SQL migrasi, tidak meng-install `pg-migrate`, dan tidak meng-clone repo ini. Setiap pipeline backend cukup memicu job Jenkins terpusat **`database-migrator-service-run`** tepat **sebelum tahap deploy / restart aplikasi**.

## Cara 1: Jenkins Pipeline (`Jenkinsfile`)

Gunakan snippet dari [`jenkins/backend-pipeline-snippet.groovy`](../../jenkins/backend-pipeline-snippet.groovy):

```groovy
stage('Database Migration') {
    steps {
        build job: 'database-migrator-service-run',
              wait: true,
              propagate: true,
              parameters: [
                  string(name: 'TARGET_ENV', value: params.TARGET_ENV), // 'development', 'staging', atau 'production'
                  string(name: 'TARGET_DB', value: 'all'),              // atau spesifik, mis. 'db_name'
                  string(name: 'ACTION', value: 'up'),
                  string(name: 'TRIGGERED_BY', value: "${env.JOB_NAME}#${env.BUILD_NUMBER}")
              ]
    }
}
```

- `wait: true` memastikan aplikasi backend tidak di-deploy sebelum migrasi selesai.
- `propagate: true` membatalkan build/deploy backend jika validasi atau eksekusi migrasi gagal.
- `TRIGGERED_BY` otomatis mencatat nama job dan build number backend ke kolom `triggered_by` di tabel `public.schema_migrations`.

## Cara 2: Jenkins Freestyle Job

Ikuti panduan [`jenkins/backend-freestyle-guide.md`](../../jenkins/backend-freestyle-guide.md):

1. Tambahkan build step **Trigger/call builds on other projects** dan pindahkan ke posisi **sebelum** langkah deploy/restart service.
2. Isi **Projects to build**: `database-migrator-service-run`.
3. Centang **Block until the triggered projects finish their builds**, serta set fail build jika status downstream `FAILURE`.
4. Tambahkan **Predefined parameters**:

   ```properties
   TARGET_ENV=staging
   TARGET_DB=all
   ACTION=up
   TRIGGERED_BY=${JOB_NAME}#${BUILD_NUMBER}
   ```

   *(Ganti `TARGET_ENV=development`, `staging`, atau `production` sesuai environment job backend, dan ubah `TARGET_DB` jika ingin membatasi ke database tertentu saja.)*

## Perilaku di Runtime

| Kondisi | Perilaku Jenkins |
|---|---|
| Ada file migrasi pending di branch `development`/`staging`/`production` | `database-migrator-service-run` mem-build `.bin/pg-migrate`, memvalidasi linter, mengambil advisory lock, menjalankan SQL pending, lalu deploy backend berlanjut |
| Tidak ada migrasi baru (`0 pending`) | Job selesai *no-op* (`exit 0`) dan deploy backend tetap berlanjut |
| Migrasi gagal (linter error, checksum mismatch, SQL error, lock timeout) | Job gagal dan build backend otomatis dihentikan sebelum kode baru naik |
| Dua atau lebih repo backend deploy bersamaan | `disableConcurrentBuilds` di Jenkins dan `pg_try_advisory_lock` di PostgreSQL menjaga eksekusi tetap terurut dan aman |

> Jika organisasi Anda menduplikasi repo template ini untuk beberapa server PostgreSQL yang terpisah, beri nama job berbeda per server (mis. `erp-migrator-run`, `billing-migrator-run`) lalu arahkan masing-masing repo backend ke job server DB-nya.
