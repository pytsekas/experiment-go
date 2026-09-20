output "url" {
  value = google_cloud_run_v2_service.api.uri
}

output "service_account_email" {
  description = "Identity the service runs as: caller-supplied via service_account_email, or the one created here"
  value       = local.service_account_email
}
