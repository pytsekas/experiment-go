# Generated once, stored in Secret Manager, never written to a tfvars file.
resource "random_password" "db" {
  length  = 32
  special = false # keeps the value safe inside a URL
}

# The cheapest useful shape: shared-core, zonal, HDD, no backups.
# Fine for experiments, none of it appropriate for production.
resource "google_sql_database_instance" "main" {
  name             = "${var.name}-pg"
  database_version = "POSTGRES_${var.engine_version}"
  region           = var.region

  settings {
    tier              = "db-f1-micro"
    edition           = "ENTERPRISE"
    availability_type = "ZONAL"
    disk_type         = "PD_HDD"
    disk_size         = 10
    disk_autoresize   = false

    backup_configuration {
      enabled = false
    }
  }

  # This whole setup is disposable; teardown must actually work.
  deletion_protection = false
}

resource "google_sql_database" "app" {
  name     = var.db_name
  instance = google_sql_database_instance.main.name

  # The instance is deleted right after anyway; skip the SQL-level DROP
  # DATABASE, which fails whenever anything still holds a connection.
  deletion_policy = "ABANDON"
}

resource "google_sql_user" "app" {
  name     = var.db_user
  instance = google_sql_database_instance.main.name
  password = random_password.db.result

  # A postgres user that owns tables cannot be dropped in-place.
  deletion_policy = "ABANDON"
}

resource "google_secret_manager_secret" "db_password" {
  secret_id = "${var.name}-db-password"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "db_password" {
  secret      = google_secret_manager_secret.db_password.id
  secret_data = random_password.db.result
}
