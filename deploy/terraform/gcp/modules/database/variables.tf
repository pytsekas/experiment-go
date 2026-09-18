variable "region" {
  description = "Region of the Cloud SQL instance"
  type        = string
}

variable "name" {
  description = "Application name; the instance is called <name>-pg"
  type        = string
}

variable "db_name" {
  description = "Application database name"
  type        = string
}

variable "db_user" {
  description = "Application database user"
  type        = string
}

variable "engine_version" {
  description = "Postgres major version, e.g. 17"
  type        = string
}
