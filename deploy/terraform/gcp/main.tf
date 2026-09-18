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
