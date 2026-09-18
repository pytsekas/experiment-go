variable "project_id" {
  type = string
}

variable "region" {
  type = string
}

variable "name" {
  description = "Service name"
  type        = string
}

variable "image" {
  description = "Full container image reference"
  type        = string
}

variable "container_port" {
  type = number
}

variable "health_check_path" {
  description = "HTTP path for the startup probe"
  type        = string
}

variable "env" {
  description = "Plain environment variables"
  type        = map(string)
  default     = {}
}

variable "secret_env" {
  description = "Environment variables sourced from Secret Manager: name => secret_id (latest version)"
  type        = map(string)
  default     = {}
}

variable "cloudsql_connection_name" {
  description = "Cloud SQL connection name mounted at /cloudsql; empty means no database"
  type        = string
}

variable "min_instances" {
  type = number
}

variable "max_instances" {
  type = number
}

variable "cpu" {
  description = "CPU limit, e.g. \"1\""
  type        = string
}

variable "memory" {
  description = "Memory limit, e.g. \"512Mi\""
  type        = string
}
