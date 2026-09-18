variable "name" {
  description = "Application name; the instance identifier is <name>-pg"
  type        = string
}

variable "vpc_id" {
  type = string
}

variable "subnet_ids" {
  description = "Private subnets for the DB subnet group"
  type        = list(string)
}

variable "db_name" {
  type = string
}

variable "db_user" {
  type = string
}

variable "engine_version" {
  description = "Postgres major version, e.g. 17"
  type        = string
}
