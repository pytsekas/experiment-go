output "image_base" {
  description = "Image path without a tag (the Makefile appends :VERSION)"
  value       = "${module.registry.repository_url}/${var.name}"
}

output "db_host" {
  description = "Cloud SQL connection name PROJECT:REGION:INSTANCE (null until create_db)"
  value       = var.create_db ? module.database[0].connection_name : null
}

output "db_password_secret" {
  description = "Secret Manager secret id holding the database password (null until create_db)"
  value       = var.create_db ? module.database[0].password_secret_id : null
}

output "serverless_url" {
  description = "Cloud Run URL (null until image is set)"
  value       = var.image != "" ? module.serverless[0].url : null
}

output "k8s_cluster_name" {
  value = var.create_k8s ? module.kubernetes[0].cluster_name : null
}

output "k8s_credentials_command" {
  value = var.create_k8s ? "gcloud container clusters get-credentials ${module.kubernetes[0].cluster_name} --zone=${var.zone} --project=${var.project_id}" : null
}

output "ingest_url" {
  description = "Ingest service URL (empty until create_ingest and image are set)"
  value       = var.create_ingest && var.image != "" ? module.ingest_service[0].url : ""
}

output "ingest_subscription" {
  description = "Push subscription feeding the ingest service (empty until create_ingest and image are set)"
  value       = var.create_ingest && var.image != "" ? module.messaging[0].subscription : ""
}

output "readings_view" {
  description = "Deduplicating BigQuery view to query (empty until create_ingest)"
  value       = var.create_ingest ? "${var.project_id}.${module.warehouse[0].dataset_id}.${module.warehouse[0].view_id}" : ""
}
