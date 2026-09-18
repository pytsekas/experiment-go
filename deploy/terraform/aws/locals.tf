# The only file that knows this particular application: its env map and how its
# database URL and secrets are shaped. Copying this root for another app means
# editing this file and terraform.tfvars.
locals {
  # Environment the app needs on ECS. var.env overrides these.
  app_env = merge({
    APP_ENV              = "production"
    LOG_FORMAT           = "json"
    MIGRATE_ON_START     = "true" # safe with N tasks: golang-migrate takes an advisory lock
    ENABLE_BURN_ENDPOINT = "true" # CPU sink for load tests; off for real workloads
    DB_MAX_CONNS         = "5"
  }, var.env)

  db_security_group_id = var.create_db ? module.database[0].security_group_id : ""

  # RDS Postgres 15+ forces TLS, hence sslmode=require. Empty when there is no
  # database: the serverless module's precondition then fails with a clear message.
  database_url = var.create_db ? "postgres://${module.database[0].user}:${module.database[0].password}@${module.database[0].address}:${module.database[0].port}/${module.database[0].database}?sslmode=require" : ""

  # Secrets the serverless platform injects as env vars: name => Secrets Manager secret ARN.
  # Empty when serverless is off (the secret only exists while var.image is set).
  serverless_secret_env = var.image != "" ? { DATABASE_URL = aws_secretsmanager_secret.database_url[0].arn } : {}
}
