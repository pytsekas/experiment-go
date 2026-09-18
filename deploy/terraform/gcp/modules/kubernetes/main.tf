data "google_project" "current" {}

# Zonal cluster (no regional control-plane fee multiplier), default pool
# replaced by an explicit spot pool.
resource "google_container_cluster" "main" {
  name     = var.name
  location = var.zone

  remove_default_node_pool = true
  initial_node_count       = 1

  # Disposable experiment cluster; the provider defaults this to true.
  deletion_protection = false

  release_channel {
    channel = "REGULAR"
  }

  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }

  # Managed Prometheus off: it is the expensive part of cluster observability
  # and the experiments only need `kubectl top` / Cloud Logging.
  monitoring_config {
    enable_components = ["SYSTEM_COMPONENTS"]
    managed_prometheus {
      enabled = false
    }
  }
}

resource "google_container_node_pool" "spot" {
  name    = "spot-pool"
  cluster = google_container_cluster.main.id

  initial_node_count = var.node_count

  autoscaling {
    min_node_count = 1
    max_node_count = 4
  }

  node_config {
    machine_type = var.node_type
    spot         = true
    disk_type    = "pd-standard"
    disk_size_gb = 32

    oauth_scopes = ["https://www.googleapis.com/auth/cloud-platform"]

    workload_metadata_config {
      mode = "GKE_METADATA"
    }
  }
}

# Workload Identity Federation: the pods' Kubernetes ServiceAccount gets
# Cloud SQL access directly, no key files anywhere.
resource "google_project_iam_member" "workload_identity_cloudsql" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "principal://iam.googleapis.com/projects/${data.google_project.current.number}/locations/global/workloadIdentityPools/${var.project_id}.svc.id.goog/subject/ns/${var.k8s_namespace}/sa/${var.k8s_service_account}"
}
