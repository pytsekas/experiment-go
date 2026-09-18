output "connection_name" {
  description = "Cloud SQL connection name PROJECT:REGION:INSTANCE"
  value       = google_sql_database_instance.main.connection_name
}

output "database" {
  value = google_sql_database.app.name
}

output "user" {
  value = google_sql_user.app.name
}

output "password" {
  value     = random_password.db.result
  sensitive = true
}

output "password_secret_id" {
  description = "Secret Manager secret id holding the database password"
  value       = google_secret_manager_secret.db_password.secret_id
}
