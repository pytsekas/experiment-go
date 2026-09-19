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
  default     = ""
}

variable "requires_database" {
  description = "Whether this service needs the Cloud SQL volume; false skips the database precondition and the /cloudsql mount entirely"
  type        = bool
  default     = true
}

variable "allow_public_access" {
  description = "Grant allUsers the invoker role; false leaves the service private so only an explicitly granted identity may call it"
  type        = bool
  default     = true
}

variable "custom_audiences" {
  description = "Extra OIDC/JWT audiences the service accepts, beyond its default run.app URL"
  type        = list(string)
  default     = []
}

variable "service_account_email" {
  description = "Pre-created service account to run as; empty creates a dedicated one here, as every existing caller does today"
  type        = string
  default     = ""
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

variable "concurrency" {
  description = "Max concurrent requests per instance"
  type        = number
  default     = 80
}
