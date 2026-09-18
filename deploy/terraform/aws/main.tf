provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Project   = var.name
      ManagedBy = "terraform"
    }
  }
}

module "network" {
  source = "./modules/network"

  name     = var.name
  vpc_cidr = var.vpc_cidr
}

module "registry" {
  source = "./modules/registry"

  name = var.name
}

module "database" {
  count  = var.create_db ? 1 : 0
  source = "./modules/database"

  name           = var.name
  vpc_id         = module.network.vpc_id
  subnet_ids     = module.network.private_subnet_ids
  db_name        = var.db_name
  db_user        = var.db_user
  engine_version = var.db_engine_version
}

# The connection string handed to the serverless platform. Lives in the root
# because Terraform cannot for_each over sensitive values inside a module.
resource "aws_secretsmanager_secret" "database_url" {
  count = var.image != "" ? 1 : 0

  name                    = "${var.name}-database-url"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "database_url" {
  count = var.image != "" ? 1 : 0

  secret_id     = aws_secretsmanager_secret.database_url[0].id
  secret_string = local.database_url
}

module "serverless" {
  count  = var.image != "" ? 1 : 0
  source = "./modules/serverless"

  name                 = var.name
  region               = var.region
  image                = var.image
  container_port       = var.container_port
  health_check_path    = var.health_check_path
  env                  = local.app_env
  secret_env           = local.serverless_secret_env
  vpc_id               = module.network.vpc_id
  public_subnet_ids    = module.network.public_subnet_ids
  db_security_group_id = local.db_security_group_id
  min_instances        = var.serverless_min_instances
  max_instances        = var.serverless_max_instances
  cpu                  = var.serverless_cpu
  memory               = var.serverless_memory

  depends_on = [aws_secretsmanager_secret_version.database_url]
}

module "kubernetes" {
  count  = var.create_k8s ? 1 : 0
  source = "./modules/kubernetes"

  name                 = var.name
  subnet_ids           = module.network.public_subnet_ids
  node_count           = var.k8s_node_count
  node_type            = var.k8s_node_type
  db_security_group_id = local.db_security_group_id
}
