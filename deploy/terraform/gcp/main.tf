provider "google" {
  project = var.project_id
  region  = var.region
  zone    = var.zone
}

module "registry" {
  source = "./modules/registry"

  project_id    = var.project_id
  region        = var.region
  repository_id = var.registry_repo
}

module "database" {
  count  = var.create_db ? 1 : 0
  source = "./modules/database"

  region         = var.region
  name           = var.name
  db_name        = var.db_name
  db_user        = var.db_user
  engine_version = var.db_engine_version

  depends_on = [module.registry]
}

# The connection string handed to the serverless platform. Lives in the root
# because Terraform cannot for_each over sensitive values inside a module.
resource "google_secret_manager_secret" "database_url" {
  count = var.image != "" ? 1 : 0

  secret_id = "${var.name}-database-url"

  replication {
    auto {}
  }

  depends_on = [module.registry]
}

resource "google_secret_manager_secret_version" "database_url" {
  count = var.image != "" ? 1 : 0

  secret      = google_secret_manager_secret.database_url[0].id
  secret_data = local.database_url
}

module "serverless" {
  count  = var.image != "" ? 1 : 0
  source = "./modules/serverless"

  project_id               = var.project_id
  region                   = var.region
  name                     = var.name
  image                    = var.image
  container_port           = var.container_port
  health_check_path        = var.health_check_path
  env                      = local.app_env
  secret_env               = local.serverless_secret_env
  cloudsql_connection_name = local.db_connection_name
  min_instances            = var.serverless_min_instances
  max_instances            = var.serverless_max_instances
  cpu                      = var.serverless_cpu
  memory                   = var.serverless_memory

  depends_on = [module.registry, google_secret_manager_secret_version.database_url]
}

module "kubernetes" {
  count  = var.create_k8s ? 1 : 0
  source = "./modules/kubernetes"

  project_id          = var.project_id
  zone                = var.zone
  name                = var.name
  node_count          = var.k8s_node_count
  node_type           = var.k8s_node_type
  k8s_namespace       = var.k8s_namespace
  k8s_service_account = var.k8s_service_account

  depends_on = [module.registry]
}

# ----------------------------------------------------------------- ingest
# Everything below is behind create_ingest, so the existing deployment plans
# clean while the toggle is off. The dataset, bucket and this service account
# can be provisioned ahead of the image, mirroring how create_db works today;
# the service, its invoker and the subscription additionally wait for image.

resource "google_service_account" "ingest" {
  count        = var.create_ingest ? 1 : 0
  account_id   = "${var.name}-ingest"
  display_name = "Consumption ingest service"
}

resource "google_storage_bucket" "quarantine" {
  count                       = var.create_ingest ? 1 : 0
  name                        = "${var.project_id}-${var.name}-quarantine"
  location                    = var.region
  force_destroy               = false
  uniform_bucket_level_access = true

  # A persistent upstream defect must not accumulate cost forever.
  lifecycle_rule {
    condition {
      age = 90
    }
    action {
      type = "Delete"
    }
  }
}

resource "google_storage_bucket_iam_member" "ingest_writer" {
  count  = var.create_ingest ? 1 : 0
  bucket = google_storage_bucket.quarantine[0].name
  role   = "roles/storage.objectCreator"
  member = "serviceAccount:${google_service_account.ingest[0].email}"
}

module "warehouse" {
  count  = var.create_ingest ? 1 : 0
  source = "./modules/warehouse"

  project_id             = var.project_id
  region                 = var.region
  name                   = var.name
  writer_service_account = google_service_account.ingest[0].email

  depends_on = [module.registry]
}

# The ingest role is the same image with a different configuration: it runs
# as google_service_account.ingest (granted BigQuery + quarantine access
# above, ahead of the service existing), has no database, and is private —
# only Pub/Sub's push identity below may invoke it.
module "ingest_service" {
  count  = var.create_ingest && var.image != "" ? 1 : 0
  source = "./modules/serverless"

  project_id            = var.project_id
  region                = var.region
  name                  = "${var.name}-ingest"
  image                 = var.image
  container_port        = var.container_port
  health_check_path     = var.health_check_path
  env                   = local.ingest_env
  secret_env            = {}
  service_account_email = google_service_account.ingest[0].email
  requires_database     = false
  allow_public_access   = false
  custom_audiences      = [var.ingest_audience]
  min_instances         = 0
  max_instances         = var.serverless_max_instances
  cpu                   = var.ingest_cpu
  memory                = var.ingest_memory
  concurrency           = 1 # parsing is CPU-bound: one request at a time per instance

  # Above local.ingest_env's HTTP_WRITE_TIMEOUT (120s): Cloud Run must not cut
  # the request off before the handler itself times out and responds.
  request_timeout_seconds = 180

  depends_on = [module.registry, module.warehouse]
}

# Pub/Sub authenticates as this account; only it may invoke the service.
resource "google_service_account" "pubsub_push" {
  count        = var.create_ingest ? 1 : 0
  account_id   = "${var.name}-push"
  display_name = "Pub/Sub push identity for the ingest service"
}

resource "google_cloud_run_v2_service_iam_member" "push_invoker" {
  count    = var.create_ingest && var.image != "" ? 1 : 0
  project  = var.project_id
  location = var.region
  name     = "${var.name}-ingest"
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.pubsub_push[0].email}"

  depends_on = [module.ingest_service]
}

module "messaging" {
  count  = var.create_ingest && var.image != "" ? 1 : 0
  source = "./modules/messaging"

  project_id           = var.project_id
  name                 = var.name
  topic_name           = var.ingest_topic
  topic_project        = var.ingest_topic_project
  create_topic         = var.ingest_create_topic
  push_endpoint        = "${module.ingest_service[0].url}/internal/pubsub/consumption"
  push_service_account = google_service_account.pubsub_push[0].email
  audience             = var.ingest_audience

  depends_on = [google_cloud_run_v2_service_iam_member.push_invoker]
}
