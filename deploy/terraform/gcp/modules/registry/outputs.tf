output "repository_url" {
  description = "Docker registry path of the image repo"
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.containers.repository_id}"
}
