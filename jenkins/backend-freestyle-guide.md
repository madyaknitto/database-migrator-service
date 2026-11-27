# Panduan Integrasi Jenkins Freestyle Job (Repo Backend)

Jika salah satu repository backend Anda masih menggunakan **Jenkins Freestyle Job** (bukan Pipeline `Jenkinsfile`), ikuti langkah berikut agar migrasi selalu dijalankan sebelum proses deploy:

## Prasyarat
- Plugin Jenkins **Parameterized Trigger Plugin** terpasang (umumnya sudah terpasang di Jenkins).
- Job terpusat **`database-migrator-service-run`** sudah dibuat menggunakan [`Jenkinsfile`](./Jenkinsfile).

## Langkah Konfigurasi pada Job Backend

1. Buka konfigurasi Freestyle Job repo backend (misalnya `svc-order-deploy-staging`).
2. Di bagian **Build Steps**, klik **Add build step** → pilih **Trigger/call builds on other projects**.
3. Pindahkan build step ini agar berada **SEBELUM** langkah deploy / restart aplikasi (PM2 / Docker / SSH).
4. Isi konfigurasi step:
   - **Projects to build**: `database-migrator-service-run`
   - Centang **Block until the triggered projects finish their builds**
   - **Fail this build step if the triggered build is worse or equal to**: `FAILURE`
   - **Mark this build as failure if the triggered build is unstable or worse**: `FAILURE`
5. Klik **Add Parameters** → pilih **Predefined parameters**, lalu isi:
   ```properties
   TARGET_ENV=staging
   TARGET_DB=all
   ACTION=up
   TRIGGERED_BY=${JOB_NAME}#${BUILD_NUMBER}
   ```
   *(Untuk job production, ubah `TARGET_ENV=production`. Jika ingin membatasi ke database tertentu, ubah `TARGET_DB=db_name` atau daftar dipisah koma).*
6. Klik **Save**.

## Hasil Saat Job Backend Dijalankan (Manual Trigger)
- Jenkins otomatis memanggil `database-migrator-service-run` terlebih dahulu.
- Jika ada antrian migrasi di branch `staging`/`production` repo `database-migrator-service`, migrasi dieksekusi sampai tuntas.
- Jika tidak ada antrian (`0 pending`), `database-migrator-service-run` selesai sukses dalam beberapa detik dan step deploy backend tetap berlanjut seperti biasa.
- Jika migrasi error (mis. SQL invalid atau melanggar linter), job backend otomatis berhenti sebelum kode baru ter-deploy.
