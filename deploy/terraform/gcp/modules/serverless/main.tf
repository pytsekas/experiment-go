# Dedicated runtime identity: only what the service needs, nothing the default
# compute service account carries.
resource "google_service_account" "run" {
  account_id   = "${var.name}-run"
  display_name = "${var.name} Cloud Run runtime"
}

resource "google_project_iam_member" "run_cloudsql" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = google_service_account.run.member
}

resource "google_secret_manager_secret_iam_member" "env" {
  for_each = var.secret_env

  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.run.member
}

# Exists only while var.image is set (make run-deploy writes serverless.auto.tfvars,
# make teardown-run removes it).
resource "google_cloud_run_v2_service" "api" {
  name     = var.name
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  # Disposable experiment service; the provider defaults this to true.
  deletion_protection = false

  template {
    service_account                  = google_service_account.run.email
    max_instance_request_concurrency = 80

    scaling {
      min_instance_count = var.min_instances # 0 = scale to zero, cold starts when not idle
      max_instance_count = var.max_instances
    }

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [var.cloudsql_connection_name]
      }
    }

    containers {
      image = var.image

      ports {
        container_port = var.container_port
      }

      resources {
        limits = {
          cpu    = var.cpu
          memory = var.memory
        }
      }

      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }

      dynamic "env" {
        for_each = var.env
        content {
          name  = env.key
          value = env.value
        }
      }

      dynamic "env" {
        for_each = var.secret_env
        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }

      startup_probe {
        http_get {
          path = var.health_check_path
          port = var.container_port
        }
        period_seconds    = 2
        timeout_seconds   = 2
        failure_threshold = 30
      }
    }
  }

  lifecycle {
    precondition {
      condition     = var.cloudsql_connection_name != ""
      error_message = "The serverless service requires the database (create_db = true). Tear down serverless before the database."
    }
  }

  depends_on = [
    google_project_iam_member.run_cloudsql,
    google_secret_manager_secret_iam_member.env,
  ]
}

resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.api.name
  location = var.region
  role     = "roles/run.invoker"
  member   = "allUsers"
}
