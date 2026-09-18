# ---------------------------------------------------------------- shared contract
# These names and meanings are identical in deploy/terraform/aws.

variable "name" {
  description = "Application name, prefix for every resource"
  type        = string
  default     = "experiment-go"
}

variable "region" {
  description = "Region for regional resources"
  type        = string
  default     = "europe-north1"
}

variable "image" {
  description = "Full container image reference. Empty means no serverless service. Set by make run-deploy via serverless.auto.tfvars"
  type        = string
  default     = ""
}

variable "container_port" {
  description = "Port the container listens on"
  type        = number
  default     = 8080
}

variable "health_check_path" {
  description = "HTTP path the platform probes"
  type        = string
  default     = "/healthz"
}

variable "env" {
  description = "Extra plain environment variables, merged over the app defaults in locals.tf"
  type        = map(string)
  default     = {}
}

variable "create_db" {
  description = "Create the managed Postgres. Set by make sql-create via db.auto.tfvars"
  type        = bool
  default     = false
}

variable "db_name" {
  type    = string
  default = "experiment"
}

variable "db_user" {
  type    = string
  default = "app"
}

variable "db_engine_version" {
  description = "Postgres major version"
  type        = string
  default     = "17"
}

variable "create_k8s" {
  description = "Create the Kubernetes cluster. Set by make gke-create via k8s.auto.tfvars"
  type        = bool
  default     = false
}

variable "k8s_namespace" {
  description = "Namespace of the app (must match deploy/k8s)"
  type        = string
  default     = "experiment-go"
}

variable "k8s_service_account" {
  description = "Kubernetes ServiceAccount of the app (must match deploy/k8s)"
  type        = string
  default     = "experiment-go"
}

variable "k8s_node_count" {
  type    = number
  default = 2
}

variable "k8s_node_type" {
  description = "Node machine type (cloud-specific value)"
  type        = string
  default     = "e2-standard-2"
}

variable "serverless_min_instances" {
  type    = number
  default = 0
}

variable "serverless_max_instances" {
  type    = number
  default = 5
}

variable "serverless_cpu" {
  description = "Cloud Run CPU limit"
  type        = string
  default     = "1"
}

variable "serverless_memory" {
  description = "Cloud Run memory limit"
  type        = string
  default     = "512Mi"
}

# ---------------------------------------------------------------- GCP only

variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "zone" {
  description = "Zone for zonal resources (GKE)"
  type        = string
  default     = "europe-north1-b"
}

variable "registry_repo" {
  description = "Artifact Registry repository id"
  type        = string
  default     = "containers"
}
