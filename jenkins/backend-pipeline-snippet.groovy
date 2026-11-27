// Snippet untuk Jenkinsfile di setiap repository backend (lintas stack: Node.js, Go, Python, Java, dll.)
// Letakkan stage 'Database Migration' tepat SEBELUM stage 'Deploy' / restart service.
//
// Perilaku:
// - Setiap kali repo backend di-build/deploy (baik ada antrian migrasi atau tidak),
//   job 'database-migrator-service-run' akan dipanggil dan ditunggu (wait: true).
// - Jika tidak ada antrian migrasi -> pg-migrate keluar dengan exit 0 (~1 detik) dan deploy berlanjut.
// - Jika ada antrian migrasi -> migrasi dijalankan dulu sampai sukses, baru aplikasi backend naik.
// - Jika migrasi gagal -> deploy backend otomatis dibatalkan (propagate: true).

pipeline {
    agent any

    parameters {
        choice(name: 'TARGET_ENV', choices: ['development', 'staging', 'production'], description: 'Environment deploy target')
    }

    stages {
        stage('Build & Test Backend') {
            steps {
                // Sesuaikan dengan stack layanan backend Anda (mis. npm ci && npm test, go test ./..., pytest, mvn test, dll.)
                sh 'echo "Jalankan build & test layanan backend Anda di sini"'
            }
        }

        // OPSI 1 (Direkomendasikan): Jalankan seluruh database aktif ('all')
        // Menjamin seluruh DB di shared server selalu sinkron sebelum layanan mana pun naik.
        stage('Database Migration') {
            steps {
                build job: 'database-migrator-service-run',
                      wait: true,
                      propagate: true,
                      parameters: [
                          string(name: 'TARGET_ENV', value: params.TARGET_ENV),
                          string(name: 'TARGET_DB', value: 'all'),
                          string(name: 'ACTION', value: 'up'),
                          string(name: 'TRIGGERED_BY', value: "${env.JOB_NAME}#${env.BUILD_NUMBER}")
                      ]
            }
        }

        // OPSI 2 (Jika suatu repo backend hanya memakai 1 atau beberapa DB tertentu):
        // stage('Database Migration (Scoped DB)') {
        //     steps {
        //         build job: 'database-migrator-service-run',
        //               wait: true,
        //               propagate: true,
        //               parameters: [
        //                   string(name: 'TARGET_ENV', value: params.TARGET_ENV),
        //                   string(name: 'TARGET_DB', value: 'db_name'),
        //                   string(name: 'ACTION', value: 'up'),
        //                   string(name: 'TRIGGERED_BY', value: "${env.JOB_NAME}#${env.BUILD_NUMBER}")
        //               ]
        //     }
        // }

        stage('Deploy Backend') {
            steps {
                echo "Deploy aplikasi ke ${params.TARGET_ENV}..."
                // Langkah deploy container / service / Kubernetes Anda di sini
            }
        }
    }
}
