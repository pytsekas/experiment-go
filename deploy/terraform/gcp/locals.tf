# The only file that knows this particular application: its env map and how its
# database URL and secrets are shaped. Copying this root for another app means
# editing this file and terraform.tfvars.
locals {
  # Environment the app needs on Cloud Run. var.env overrides these.
  app_env = merge({
    APP_ENV              = "production"
    LOG_FORMAT           = "json"
    MIGRATE_ON_START     = "true" # safe with N instances: golang-migrate takes an advisory lock
    ENABLE_BURN_ENDPOINT = "true" # CPU sink for load tests; off for real workloads
    DB_MAX_CONNS         = "5"
  }, var.env)

  db_connection_name = var.create_db ? module.database[0].connection_name : ""

  # Cloud Run mounts the Cloud SQL unix socket; pgx accepts it via host=.
  # Empty when there is no database: the serverless module's precondition then
  # fails with a clear message instead of an index error.
  database_url = var.create_db ? "postgres://${module.database[0].user}:${module.database[0].password}@/${module.database[0].database}?host=/cloudsql/${module.database[0].connection_name}" : ""

  # Secrets the serverless platform injects as env vars: name => Secret Manager secret id.
  # Empty when serverless is off (the secret only exists while var.image is set).
  serverless_secret_env = var.image != "" ? { DATABASE_URL = google_secret_manager_secret.database_url[0].secret_id } : {}

  # Environment for the ingest role. PUBSUB_AUDIENCE is the constant
  # var.ingest_audience, not the ingest service's own URL: deriving it from
  # module.ingest_service[0].url here would make the service's environment
  # depend on the service's own output, a dependency cycle Terraform cannot
  # resolve. The same constant is passed as the service's custom_audiences
  # and as the messaging module's audience, so all three agree.
  ingest_env = var.create_ingest && var.image != "" ? {
    APP_ENV                     = "production"
    LOG_FORMAT                  = "json"
    ENABLE_INGEST_ENDPOINT      = "true"
    PUBSUB_AUDIENCE             = var.ingest_audience
    PUBSUB_PUSH_SERVICE_ACCOUNT = google_service_account.pubsub_push[0].email
    BQ_PROJECT                  = var.project_id
    BQ_DATASET                  = module.warehouse[0].dataset_id
    BQ_TABLE                    = module.warehouse[0].table_id
    QUARANTINE_BUCKET           = google_storage_bucket.quarantine[0].name
  } : {}
}
