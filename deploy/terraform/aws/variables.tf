# ---------------------------------------------------------------- shared contract
# These names and meanings are identical in deploy/terraform/gcp.

variable "name" {
  description = "Application name, prefix for every resource"
  type        = string
  default     = "experiment-go"
}

variable "region" {
  description = "Region for regional resources"
  type        = string
  default     = "eu-north-1"
}

variable "image" {
  description = "Full container image reference. Empty means no serverless service. Set by make aws-ecs-deploy via serverless.auto.tfvars"
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
  description = "Create the managed Postgres. Set by make aws-sql-create via db.auto.tfvars"
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
  description = "Create the Kubernetes cluster. Set by make aws-eks-create via k8s.auto.tfvars"
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
  default     = "t3.medium"
}

variable "serverless_min_instances" {
  type    = number
  default = 1 # Fargate cannot scale to zero
}

variable "serverless_max_instances" {
  type    = number
  default = 5
}

variable "serverless_cpu" {
  description = "Fargate task CPU units"
  type        = string
  default     = "256"
}

variable "serverless_memory" {
  description = "Fargate task memory in MiB"
  type        = string
  default     = "512"
}

# ---------------------------------------------------------------- AWS only

variable "vpc_cidr" {
  description = "CIDR of the VPC created for this application"
  type        = string
  default     = "10.0.0.0/16"
}
