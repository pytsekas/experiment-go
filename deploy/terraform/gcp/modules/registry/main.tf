# The service APIs everything else depends on. disable_on_destroy = false:
# destroying the infra should not switch APIs off for the whole project.
locals {
  services = [
    "artifactregistry.googleapis.com",
    "run.googleapis.com",
    "sqladmin.googleapis.com",
    "container.googleapis.com",
    "cloudbuild.googleapis.com",
    "secretmanager.googleapis.com",
    "iam.googleapis.com",
  ]
}

resource "google_project_service" "apis" {
  for_each = toset(local.services)

  service            = each.value
  disable_on_destroy = false
}

# The image repo survives `make teardown-all` on purpose (pennies per month,
# saves a rebuild next time). `terraform destroy` removes it, images included.
resource "google_artifact_registry_repository" "containers" {
  location      = var.region
  repository_id = var.repository_id
  format        = "DOCKER"
  description   = "experiment images"

  depends_on = [google_project_service.apis]
}
