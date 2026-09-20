# Dedicated runtime identity: only what the service needs, nothing the default
# compute service account carries. Skipped when the caller supplies its own
# service_account_email, e.g. one it also grants other IAM roles outside this
# module before the service exists.
resource "google_service_account" "run" {
  count        = var.service_account_email == "" ? 1 : 0
  account_id   = "${var.name}-run"
  display_name = "${var.name} Cloud Run runtime"
}

locals {
  # Whichever identity actually runs the service: the caller-supplied one, or
  # the one created above when none is supplied. Secret access follows this
  # identity rather than always the internal SA, so a caller-supplied
  # account gets exactly the grants it needs too.
  service_account_email  = var.service_account_email != "" ? var.service_account_email : google_service_account.run[0].email
  service_account_member = var.service_account_email != "" ? "serviceAccount:${var.service_account_email}" : google_service_account.run[0].member
}

# Only granted when the service actually has a database: an account that
# will never open a Cloud SQL connection has no business holding a
# project-level database role.
resource "google_project_iam_member" "run_cloudsql" {
  count   = var.requires_database ? 1 : 0
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = local.service_account_member
}

resource "google_secret_manager_secret_iam_member" "env" {
  for_each = var.secret_env

  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = local.service_account_member
}

# Exists only while var.image is set (make run-deploy writes serverless.auto.tfvars,
# make teardown-run removes it).
resource "google_cloud_run_v2_service" "api" {
  name     = var.name
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  # Disposable experiment service; the provider defaults this to true.
  deletion_protection = false

  custom_audiences = var.custom_audiences

  template {
    service_account                  = local.service_account_email
    max_instance_request_concurrency = var.concurrency
    timeout                          = "${var.request_timeout_seconds}s"

    scaling {
      min_instance_count = var.min_instances # 0 = scale to zero, cold starts when not idle
      max_instance_count = var.max_instances
    }

    dynamic "volumes" {
      for_each = var.requires_database ? [1] : []
      content {
        name = "cloudsql"
        cloud_sql_instance {
          instances = [var.cloudsql_connection_name]
        }
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

      dynamic "volume_mounts" {
        for_each = var.requires_database ? [1] : []
        content {
          name       = "cloudsql"
          mount_path = "/cloudsql"
        }
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
      condition     = var.requires_database ? var.cloudsql_connection_name != "" : true
      error_message = "The serverless service requires the database (create_db = true). Tear down serverless before the database."
    }
  }

  depends_on = [
    google_project_iam_member.run_cloudsql,
    google_secret_manager_secret_iam_member.env,
  ]
}

resource "google_cloud_run_v2_service_iam_member" "public" {
  count    = var.allow_public_access ? 1 : 0
  name     = google_cloud_run_v2_service.api.name
  location = var.region
  role     = "roles/run.invoker"
  member   = "allUsers"
}
